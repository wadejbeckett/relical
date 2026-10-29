<?php

declare(strict_types=1);

use Noiz\Relical\Command;
use Noiz\Relical\PleskDomains;
use Noiz\Relical\RestoreAccess;
use Noiz\Relical\RestoreFlow;
use Noiz\Relical\RestoreRecord;
use Noiz\Relical\RestoreRequest;
use Noiz\Relical\RootScripts;
use Noiz\Relical\Status;

/**
 * The customer's restore screens, opened from the "Backups" button on a
 * subscription: the restore points, then a restore of mail into a new IMAP
 * folder, of website files into `restored-<date>-<id>/` in the
 * subscription's home directory, or of a database as an `.sql` file in the
 * same kind of folder. Nothing live is ever changed.
 *
 * Every request is bound to the viewer's own subscription before anything
 * else happens (RestoreAccess, from the `dom_id` or `site_id` the button
 * passes): a domain the viewer cannot access is refused with "Permission
 * denied"; a subscription whose plan does not include Relical gets a plain
 * page that says so. The subscription comes from the domain, never from
 * the form, and `_server` (the Plesk configuration) is never reachable.
 *
 * Like the administrator screens, the controller never runs relical or
 * restic itself: it reads through `restore-read.php` (cached, one read per
 * subscription at a time) and starts a restore detached through
 * `restore-run.php` (one per subscription, two on the server), which
 * records the outcome; the status page
 * reads that record (`restore-result.php`) and reloads itself with a meta
 * refresh until the restore has finished. No JavaScript of its own.
 */
class RestoreController extends pm_Controller_Action
{
    /** How long the screens wait for a root script (snapshots and ls read the Storage Box). */
    private const SCRIPT_TIMEOUT = 60.0;

    /** Seconds between reloads of the status page while a restore runs. */
    private const REFRESH_SECONDS = 5;

    private RestoreAccess $access;
    private int $domainId = 0;

    public function init()
    {
        parent::init();

        require_once __DIR__ . '/../library/autoload.php';

        $params = $this->getRequest()->getParams();
        $this->domainId = PleskDomains::domainId($params);
        $this->access = PleskDomains::access($params);
        if (!$this->access->allowed() && $this->access->refusal() !== RestoreAccess::NOT_COVERED) {
            throw new pm_Exception('Permission denied');
        }

        $this->view->pageTitle = 'Backups of ' . $this->access->subscription();
        $this->view->subscription = $this->access->subscription();
        $this->view->backUrl = $this->url('index');
    }

    /** The restore points and what can be restored. */
    public function indexAction()
    {
        if (!$this->gate('index')) {
            return;
        }
        $points = $this->restorePoints();
        $this->view->error = $points['error'];
        $this->view->points = $points['points'];
        $this->view->features = $this->access->features();
        $this->view->running = $this->restoreRunning();
        $this->view->mailUrl = $this->url('mail');
        $this->view->databaseUrl = $this->url('database');
        $this->view->browseUrls = [];
        foreach ($points['points']['files'] ?? [] as $point) {
            $this->view->browseUrls[$point['id']] = $this->url('files', ['snapshot' => $point['id']]);
        }
    }

    /** GET: the mail restore form. POST: starts the restore. */
    public function mailAction()
    {
        if (!$this->gate('mail')) {
            return;
        }
        $this->view->pageTitle = 'Restore mail: ' . $this->access->subscription();

        $points = $this->restorePoints();
        $mailboxes = $this->mailboxes();
        $this->view->error = $points['error'] !== '' ? $points['error'] : $mailboxes['error'];
        $this->view->form = null;
        if ($this->view->error !== '' || ($points['points']['mail'] ?? []) === [] || $mailboxes['mailboxes'] === []) {
            $this->view->empty = $this->view->error === '';
            return;
        }

        $form = new pm_Form_Simple();
        $form->setAction($this->url('mail'));
        $form->addElement('select', 'mailbox', [
            'label' => 'Mailbox',
            'multiOptions' => array_combine(array_keys($mailboxes['mailboxes']), array_keys($mailboxes['mailboxes'])),
            'required' => true,
        ]);
        $form->addElement('select', 'snapshot', [
            'label' => 'Restore point',
            'multiOptions' => self::pointOptions($points['points']['mail']),
            'required' => true,
        ]);
        $form->addElement('text', 'folder', [
            'label' => 'Only this folder',
            'description' => 'For example INBOX or Sent. Leave it empty to restore the whole mailbox.',
        ]);
        $form->addControlButtons(['sendTitle' => 'Restore', 'cancelLink' => $this->url('index')]);

        if ($this->getRequest()->isPost() && $form->isValid($this->getRequest()->getPost())) {
            try {
                $args = RestoreRequest::mail($this->subscription(), $form->getValues(), $mailboxes['mailboxes'], $points['points']);
            } catch (InvalidArgumentException $e) {
                $form->getElement(str_contains($e->getMessage(), 'folder') ? 'folder' : 'snapshot')->addError($e->getMessage());
                $args = null;
            }
            if ($args !== null) {
                $this->start($args);
                return;
            }
        }
        $this->view->form = $form;
    }

    /**
     * GET `?snapshot=ID&path=P`: one folder of a files restore point, with a
     * breadcrumb and the restore form. POST: starts the restore of that
     * folder or of the whole webspace.
     */
    public function filesAction()
    {
        if (!$this->gate('files')) {
            return;
        }
        $request = $this->getRequest();
        $this->view->pageTitle = 'Restore website files: ' . $this->access->subscription();
        $this->view->form = null;
        $this->view->entries = [];
        $this->view->truncated = false;
        $this->view->crumbs = [];

        $points = $this->restorePoints();
        $this->view->error = $points['error'];
        if ($points['error'] !== '') {
            return;
        }
        $snapshot = (string) $request->getParam('snapshot');
        $point = null;
        foreach ($points['points']['files'] ?? [] as $candidate) {
            if ($candidate['id'] === $snapshot) {
                $point = $candidate;
            }
        }
        try {
            if ($point === null) {
                throw new InvalidArgumentException('Choose one of the restore points in the list.');
            }
            $path = RestoreRequest::path((string) $request->getParam('path'));
        } catch (InvalidArgumentException $e) {
            $this->_status->addMessage('error', $e->getMessage());
            $this->redirectTo($this->url('index'));
            return;
        }

        $form = new pm_Form_Simple();
        $form->setAction($this->url('files', ['snapshot' => $point['id'], 'path' => $path]));
        $form->addElement('hidden', 'snapshot', ['value' => $point['id']]);
        $form->addElement('hidden', 'path', ['value' => $path]);
        if ($path !== '') {
            $form->addElement('radio', 'what', [
                'label' => 'Restore',
                'multiOptions' => ['folder' => 'This folder (' . $path . ')', 'all' => 'Everything in the subscription\'s home directory'],
                'value' => 'folder',
                'required' => true,
            ]);
        } else {
            $form->addElement('hidden', 'what', ['value' => 'all']);
        }
        $form->addControlButtons(['sendTitle' => 'Restore', 'cancelLink' => $this->url('index')]);

        if ($request->isPost() && $form->isValid($request->getPost())) {
            try {
                $this->start(RestoreRequest::files($this->subscription(), $form->getValues(), $points['points']));
                return;
            } catch (InvalidArgumentException $e) {
                $this->_status->addMessage('error', $e->getMessage());
            }
        }

        RestoreRequest::lsArgs($this->subscription(), $point['id'], $path); // validates, as restore-read.php does again
        $result = RootScripts::run('restore-read.php', array_values(array_filter(['ls', $this->subscription(), $point['id'], $path], 'strlen')), self::SCRIPT_TIMEOUT);
        try {
            $listing = RestoreRequest::listing($result['data'], $path);
        } catch (RuntimeException $e) {
            $this->view->error = $result['data'] !== null ? $this->safeError($e->getMessage()) : $this->safeError(RootScripts::error($result));
            return;
        }
        $this->view->point = $point;
        $this->view->path = $path;
        $this->view->crumbs = array_map(fn (array $c): array => $c + ['url' => $this->url('files', ['snapshot' => $point['id'], 'path' => $c['path']])], RestoreRequest::breadcrumb($path));
        $this->view->entries = array_map(fn (array $entry): array => $entry + [
            'url' => $entry['browse'] !== '' ? $this->url('files', ['snapshot' => $point['id'], 'path' => $entry['browse']]) : '',
            'size_label' => $entry['type'] === 'file' ? Status::bytes($entry['size']) : '',
        ], $listing['entries']);
        $this->view->truncated = $listing['truncated'];
        $this->view->form = $form;
    }

    /** GET: the database restore form. POST: starts the restore. */
    public function databaseAction()
    {
        if (!$this->gate('database')) {
            return;
        }
        $this->view->pageTitle = 'Restore a database: ' . $this->access->subscription();
        $this->view->form = null;

        $points = $this->restorePoints();
        $this->view->error = $points['error'];
        $this->view->empty = $points['error'] === '' && ($points['points']['db'] ?? []) === [];
        if ($points['error'] !== '' || $this->view->empty) {
            return;
        }

        $options = [];
        foreach ($points['points']['db'] as $point) {
            $options[$point['id']] = sprintf('%s, %s (%s)', $point['database'], $point['label'], $point['short']);
        }
        $form = new pm_Form_Simple();
        $form->setAction($this->url('database'));
        $form->addElement('select', 'snapshot', [
            'label' => 'Database and restore point',
            'multiOptions' => $options,
            'required' => true,
        ]);
        $form->addControlButtons(['sendTitle' => 'Restore', 'cancelLink' => $this->url('index')]);

        if ($this->getRequest()->isPost() && $form->isValid($this->getRequest()->getPost())) {
            try {
                $this->start(RestoreRequest::database($this->subscription(), $form->getValues(), $points['points']));
                return;
            } catch (InvalidArgumentException $e) {
                $form->getElement('snapshot')->addError($e->getMessage());
            }
        }
        $this->view->form = $form;
    }

    /** GET `?id=ID`: how a restore is getting on; reloads itself until it has finished. */
    public function statusAction()
    {
        if (!$this->gate('status')) {
            return;
        }
        $this->view->pageTitle = 'Restore: ' . $this->access->subscription();
        $id = (string) $this->getRequest()->getParam('id');
        $record = null;
        $this->view->readError = '';
        try {
            Command::restoreId($id);
            $result = RootScripts::run('restore-result.php', [$id], self::SCRIPT_TIMEOUT);
            if ($result['code'] === 0 && ($result['data']['found'] ?? false) === true && is_array($result['data']['record'] ?? null)) {
                $record = $result['data']['record'];
            } elseif ($result['code'] !== 0) {
                $this->view->readError = $this->safeError(RootScripts::error($result));
            }
        } catch (InvalidArgumentException $e) {
            $id = '';
        }

        $view = RestoreRecord::view($record, $this->subscription(), RestoreRequest::startedAt($id), time(), $this->access->isAdmin());
        if ($this->view->readError !== '') {
            $view['refresh'] = true; // try again: the record may just be being written
        }
        $this->view->id = $id;
        $this->view->restore = $view;
        $this->view->refreshUrl = $id !== '' ? $this->url('status', ['id' => $id]) : '';
        $this->view->refreshSeconds = self::REFRESH_SECONDS;
    }

    // ----- helpers -----

    /**
     * RestoreFlow's decision for this action, before anything else runs:
     * "Permission denied", the "not included in your plan" page (false), or
     * go on (true).
     */
    private function gate(string $action): bool
    {
        $decision = RestoreFlow::gate($this->access, $action);
        if ($decision === RestoreFlow::DENY) {
            throw new pm_Exception('Permission denied');
        }
        if ($decision === RestoreFlow::NOT_COVERED) {
            $this->view->pageTitle = 'Backups';
            $this->_helper->viewRenderer->setRender('not-covered');
            return false;
        }

        return true;
    }

    private function subscription(): string
    {
        return (string) $this->access->subscription();
    }

    /**
     * Starts a restore detached and goes to its status page; refuses while
     * another restore of the subscription runs.
     *
     * @param list<string> $args a customer restore, from RestoreRequest
     */
    private function start(array $args): void
    {
        $unit = $args[array_search('--unit', $args, true) + 1] ?? '';
        $state = RootScripts::run('restore-running.php', [$this->subscription()], self::SCRIPT_TIMEOUT);
        $refusal = RestoreRecord::startRefusal($state['code'] === 0 ? $state['data'] : null, $unit, $this->access->isAdmin());
        if ($refusal !== null) {
            $this->_status->addMessage('warning', $refusal);
            $this->_helper->json(['redirect' => $this->url('index')]);
            return;
        }
        $id = RestoreRequest::newId(time(), random_bytes(12));
        $marker = $this->access->isAdmin() ? [Command::ADMIN_FLAG] : [];
        if (!RootScripts::startScriptInBackground('restore-run.php', array_merge([$id], $marker, $args))) {
            $this->_status->addMessage('error', RestoreRecord::CANNOT_START);
            $this->_helper->json(['redirect' => $this->url('index')]);
            return;
        }
        $this->_helper->json(['redirect' => $this->url('status', ['id' => $id])]);
    }

    private function restoreRunning(): bool
    {
        $result = RootScripts::run('restore-running.php', [$this->subscription()], self::SCRIPT_TIMEOUT);

        return ($result['data']['running'] ?? false) === true;
    }

    /** @return array{points: array<string, list<array<string, string>>>, error: string} */
    private function restorePoints(): array
    {
        $result = RootScripts::run('restore-read.php', ['snapshots', $this->subscription()], self::SCRIPT_TIMEOUT);
        try {
            return ['points' => RestoreRequest::restorePoints($result['data']), 'error' => ''];
        } catch (RuntimeException $e) {
            $message = $result['data'] !== null ? $e->getMessage() : RootScripts::error($result);

            return ['points' => ['mail' => [], 'files' => [], 'db' => []], 'error' => $this->safeError($message)];
        }
    }

    /** @return array{mailboxes: array<string, array{domain: string, mailbox: string}>, error: string} */
    private function mailboxes(): array
    {
        $result = RootScripts::run('restore-read.php', ['mailboxes', $this->subscription()], self::SCRIPT_TIMEOUT);
        try {
            return ['mailboxes' => RestoreRequest::mailboxes($result['data']), 'error' => ''];
        } catch (RuntimeException $e) {
            $message = $result['data'] !== null ? $e->getMessage() : RootScripts::error($result);

            return ['mailboxes' => [], 'error' => $this->safeError($message)];
        }
    }

    /** The core's own words for an administrator; a plain sentence for a customer. */
    private function safeError(string $message): string
    {
        return $this->access->isAdmin() ? $message : RestoreRecord::customerReadError($message);
    }

    /**
     * @param list<array<string, string>> $points
     * @return array<string, string> id => label
     */
    private static function pointOptions(array $points): array
    {
        $options = [];
        foreach ($points as $point) {
            $options[$point['id']] = sprintf('%s (%s)', $point['label'], $point['short']);
        }

        return $options;
    }

    /**
     * A URL of this controller for the viewer's domain.
     *
     * @param array<string, string> $query
     */
    private function url(string $action, array $query = []): string
    {
        return pm_Context::getActionUrl('restore', $action) . '?' . http_build_query(['dom_id' => $this->domainId] + $query, '', '&', PHP_QUERY_RFC3986);
    }

    private function redirectTo(string $url): void
    {
        $this->_helper->viewRenderer->setNoRender(true);
        $this->_redirect($url, ['prependBase' => false]);
    }
}

<?php

declare(strict_types=1);

use Noiz\Relical\Command;
use Noiz\Relical\Coverage;
use Noiz\Relical\Destination;
use Noiz\Relical\HostKey;
use Noiz\Relical\Overview;
use Noiz\Relical\Paths;
use Noiz\Relical\Report;
use Noiz\Relical\RootScripts;
use Noiz\Relical\Settings;
use Noiz\Relical\Status;

/**
 * The administrator's screens: settings, the Storage Box connection, the
 * status of every subscription's backups, "Back up now", and the log.
 *
 * Administrators only. The controller never runs relical, restic or ssh
 * itself: every privileged step goes through a root script
 * (`plesk bin extension --exec relical <script>`, argv, validated by
 * Command), and backups start detached in the background. Every change is a
 * POST through a pm_Form (with Plesk's forgery token); GET pages only read.
 */
class IndexController extends pm_Controller_Action
{
    /** How long the screens wait for a root script (keyscan 15 s, probe 20 s inside). */
    private const SCRIPT_TIMEOUT = 60.0;

    public function init()
    {
        parent::init();

        if (!pm_Session::getClient()->isAdmin()) {
            throw new pm_Exception('Permission denied');
        }

        require_once __DIR__ . '/../library/autoload.php';

        $this->view->pageTitle = 'Relical Backup';
    }

    /** Settings, the public key, the connection, the status table and the last report. */
    public function indexAction()
    {
        $form = $this->settingsForm();

        if ($this->getRequest()->isPost()) {
            if ($form->isValid($this->getRequest()->getPost()) && $this->saveSettings($form)) {
                $this->_helper->json(['redirect' => pm_Context::getBaseUrl()]);
                return;
            }
            // Invalid: the form is shown again with its errors.
        }

        $settings = Settings::fromPlesk();
        try {
            $destination = Destination::fromInput($settings->configInput());
        } catch (InvalidArgumentException $e) {
            $destination = null;
        }

        $this->view->form = $form;
        $this->view->destination = $destination?->display();
        $overview = RootScripts::run('overview.php', [], Overview::TIMEOUT);
        $parts = Overview::parse($overview['data'], RootScripts::error($overview));
        $this->view->publicKey = $this->publicKey($parts['public_key']);
        $this->view->status = $this->statusRows($parts['status']);
        $this->view->report = Report::fromCli($parts['report']['code'], $parts['report']['stdout'], $parts['report']['stderr']);
        $this->view->connectionUrl = pm_Context::getActionUrl('index', 'connection');
        $this->view->probeUrl = pm_Context::getActionUrl('index', 'probe');
        $this->view->backupUrl = pm_Context::getActionUrl('index', 'backup');
        $this->view->logUrl = pm_Context::getActionUrl('index', 'log');
    }

    /**
     * GET: scans the Storage Box's host key and shows its fingerprint with a
     * "Trust this host key" form. POST: pins the key only if a fresh scan
     * still has the fingerprint shown, then tests the connection.
     */
    public function connectionAction()
    {
        $form = new pm_Form_Simple();
        $form->setAction(pm_Context::getActionUrl('index', 'connection'));
        $form->addElement('hidden', 'fingerprint', ['required' => true]);
        $form->addControlButtons([
            'sendTitle' => 'Trust this host key',
            'cancelLink' => pm_Context::getBaseUrl(),
        ]);

        if ($this->getRequest()->isPost()) {
            if ($form->isValid($this->getRequest()->getPost())) {
                $this->trustHostKey((string) $form->getValue('fingerprint'));
            }
            $this->_helper->json(['redirect' => pm_Context::getBaseUrl()]);
            return;
        }

        $this->view->pageTitle = 'Relical Backup: Storage Box connection';
        $this->view->backUrl = pm_Context::getBaseUrl();
        $this->view->scan = null;
        $this->view->scanError = '';
        $this->view->form = null;

        $result = RootScripts::run('hostkey.php', ['scan'], self::SCRIPT_TIMEOUT);
        $data = $result['data'];
        if ($result['code'] !== 0 || !is_array($data) || !is_string($data['fingerprint'] ?? null)) {
            $this->view->scanError = RootScripts::error($result);
            return;
        }
        try {
            $fingerprint = HostKey::fingerprintArg($data['fingerprint']);
        } catch (InvalidArgumentException $e) {
            $this->view->scanError = $e->getMessage();
            return;
        }
        $this->view->scan = [
            'host' => (string) ($data['host'] ?? ''),
            'port' => (int) ($data['port'] ?? 0),
            'fingerprint' => $fingerprint,
            'pinned' => ($data['pinned'] ?? false) === true,
        ];
        $form->getElement('fingerprint')->setValue($fingerprint);
        $this->view->form = $form;
    }

    /**
     * GET: offers to test the connection. POST: tests it (an sftp session
     * that lists the backup path and changes nothing) and returns to the
     * settings with the result. A POST, so a link from elsewhere cannot make
     * the server connect out (Task 8 review finding 9).
     */
    public function probeAction()
    {
        $form = new pm_Form_Simple();
        $form->setAction(pm_Context::getActionUrl('index', 'probe'));
        $form->addControlButtons([
            'sendTitle' => 'Test connection',
            'cancelLink' => pm_Context::getBaseUrl(),
        ]);

        if ($this->getRequest()->isPost()) {
            if ($form->isValid($this->getRequest()->getPost())) {
                $this->reportProbe();
            }
            $this->_helper->json(['redirect' => pm_Context::getBaseUrl()]);
            return;
        }

        $this->view->pageTitle = 'Relical Backup: test connection';
        $this->view->form = $form;
    }

    /**
     * GET: asks to confirm a backup of one subscription (`?subscription=S`)
     * or of everything (`?all=1`). POST: starts it in the background, unless
     * a run already holds the lock.
     */
    public function backupAction()
    {
        $request = $this->getRequest();
        $all = (string) $request->getParam('all') === '1';
        $subscription = trim((string) $request->getParam('subscription'));
        try {
            $args = $all ? ['backup', '--all'] : ['backup', '--subscription', Command::subscription($subscription)];
            Command::parse($args);
        } catch (InvalidArgumentException $e) {
            $this->_status->addMessage('error', 'Back up now: ' . $e->getMessage());
            $this->_helper->viewRenderer->setNoRender(true);
            $this->_redirect(pm_Context::getBaseUrl());
            return;
        }

        $form = new pm_Form_Simple();
        $form->setAction(pm_Context::getActionUrl('index', 'backup'));
        if ($all) {
            $form->addElement('hidden', 'all', ['value' => '1']);
        } else {
            $form->addElement('hidden', 'subscription', ['value' => $subscription]);
        }
        $form->addControlButtons([
            'sendTitle' => $all ? 'Run everything now' : 'Back up now',
            'cancelLink' => pm_Context::getBaseUrl(),
        ]);

        if ($request->isPost()) {
            if ($form->isValid($request->getPost())) {
                if ($this->runInProgress()) {
                    $this->_status->addMessage('warning', 'A backup run is already in progress (from the nightly timer or an earlier "Back up now"). Start this one when it has finished; the status table shows when it is done.');
                } else {
                    RootScripts::startInBackground($args);
                    $this->_status->addMessage('info', ($all ? 'A backup of every subscription' : sprintf('A backup of %s', $subscription))
                        . ' has started in the background. The status table shows the result when it has finished.');
                }
            }
            $this->_helper->json(['redirect' => pm_Context::getBaseUrl()]);
            return;
        }

        $this->view->pageTitle = 'Relical Backup: back up now';
        $this->view->what = $all ? 'every subscription, then the Plesk configuration' : $subscription;
        $this->view->running = $this->runInProgress();
        $this->view->form = $form;
        $this->view->backUrl = pm_Context::getBaseUrl();
    }

    /**
     * GET `?subscription=S`: asks to confirm adopting the subscription's
     * repository. POST: runs `cli.php adopt --subscription S`, which records
     * the subscription's Plesk id with its repository key so the core backs
     * it up again. Offered on a status row whose last error is the core's
     * ownerless-key refusal (Status::ADOPT_HINT).
     */
    public function adoptAction()
    {
        $request = $this->getRequest();
        $subscription = trim((string) $request->getParam('subscription'));
        try {
            $args = ['adopt', '--subscription', Command::domain($subscription)];
            Command::parse($args);
        } catch (InvalidArgumentException $e) {
            $this->_status->addMessage('error', 'Adopt repository: ' . $e->getMessage());
            $this->_helper->viewRenderer->setNoRender(true);
            $this->_redirect(pm_Context::getBaseUrl());
            return;
        }

        $form = new pm_Form_Simple();
        $form->setAction(pm_Context::getActionUrl('index', 'adopt'));
        $form->addElement('hidden', 'subscription', ['value' => $subscription]);
        $form->addControlButtons([
            'sendTitle' => 'Adopt repository',
            'cancelLink' => pm_Context::getBaseUrl(),
        ]);

        if ($request->isPost()) {
            if ($form->isValid($request->getPost())) {
                if ($this->runInProgress()) {
                    $this->_status->addMessage('warning', 'A backup run is in progress. Adopt the repository when it has finished.');
                } else {
                    $this->reportAdopt($subscription, RootScripts::run('cli.php', $args, self::SCRIPT_TIMEOUT));
                }
            }
            $this->_helper->json(['redirect' => pm_Context::getBaseUrl()]);
            return;
        }

        $this->view->pageTitle = 'Relical Backup: adopt repository';
        $this->view->subscription = $subscription;
        $this->view->form = $form;
        $this->view->backUrl = pm_Context::getBaseUrl();
    }

    /** GET: the last 200 lines of the core's log. */
    public function logAction()
    {
        $this->view->pageTitle = 'Relical Backup: log';
        $this->view->backUrl = pm_Context::getBaseUrl();
        $this->view->logPath = Paths::LOG;
        $this->view->lines = [];
        $this->view->logError = '';
        $this->view->logExists = false;

        $result = RootScripts::run('log-tail.php', [], self::SCRIPT_TIMEOUT);
        $data = $result['data'];
        if ($result['code'] !== 0 || !is_array($data) || !is_array($data['lines'] ?? null)) {
            $this->view->logError = RootScripts::error($result);
            return;
        }
        $this->view->logExists = ($data['exists'] ?? false) === true;
        $this->view->lines = array_values(array_filter($data['lines'], 'is_string'));
    }

    // ----- settings -----

    private function settingsForm(): pm_Form_Simple
    {
        $settings = Settings::fromPlesk();
        $retention = $settings->retention();

        $form = new pm_Form_Simple();
        $form->addElement('radio', Settings::SCOPE, [
            'label' => 'Back up',
            'multiOptions' => [
                Coverage::SCOPE_ALL => 'Every subscription',
                Coverage::SCOPE_PLAN_ITEMS => 'Subscriptions whose plan includes a Relical item',
            ],
            'value' => $settings->scope(),
            'required' => true,
        ]);
        $form->addElement('text', Settings::SFTP_HOST, [
            'label' => 'Storage Box host',
            'value' => $settings->sftpHost(),
            'required' => true,
            'description' => 'For example u000000-sub1.your-storagebox.de.',
        ]);
        $form->addElement('text', Settings::SFTP_PORT, [
            'label' => 'SFTP port',
            'value' => (string) $settings->sftpPort(),
            'description' => 'A Storage Box takes SFTP with keys on port 23.',
        ]);
        $form->addElement('text', Settings::SFTP_USER, [
            'label' => 'Storage Box user',
            'value' => $settings->sftpUser(),
            'required' => true,
        ]);
        $form->addElement('text', Settings::SFTP_PATH, [
            'label' => 'Path on the Storage Box',
            'value' => $settings->sftpPath(),
            'description' => 'Each subscription gets its own encrypted repository under this directory.',
        ]);
        $form->addElement('text', Settings::RETENTION_DAILY, ['label' => 'Daily backups kept', 'value' => (string) $retention['daily']]);
        $form->addElement('text', Settings::RETENTION_WEEKLY, ['label' => 'Weekly backups kept', 'value' => (string) $retention['weekly']]);
        $form->addElement('text', Settings::RETENTION_MONTHLY, ['label' => 'Monthly backups kept', 'value' => (string) $retention['monthly']]);
        $form->addElement('text', Settings::BACKUP_TIME, [
            'label' => 'Backup time',
            'value' => $settings->backupTime(),
            'description' => 'HH:MM, server time. The weekly check runs on Sundays twelve hours later.',
        ]);
        $form->addElement('text', Settings::LIMIT_UPLOAD_KIB, [
            'label' => 'Upload limit (KiB/s)',
            'value' => (string) $settings->limitUploadKib(),
            'description' => '0 for no limit.',
        ]);
        $form->addControlButtons([
            'sendTitle' => 'Save',
            'cancelLink' => pm_Context::getModulesListUrl(),
        ]);

        return $form;
    }

    /**
     * Stores the settings, then has root write the configuration and
     * re-install the timers.
     *
     * @return bool false when a value was refused (the form then carries the errors)
     */
    private function saveSettings(pm_Form_Simple $form): bool
    {
        $values = [];
        foreach (Settings::KEYS as $key) {
            $values[$key] = $form->getValue($key);
        }
        $errors = Settings::errors($values);
        if ($errors !== []) {
            foreach ($errors as $key => $message) {
                $form->getElement($key)->addError(ucfirst($message) . '.');
            }
            return false;
        }

        $previousTime = Settings::fromPlesk()->backupTime();
        $previousHost = Settings::fromPlesk()->sftpHost() . ':' . Settings::fromPlesk()->sftpPort();
        $store = Settings::validated($values);
        foreach ($store as $key => $value) {
            pm_Settings::set($key, $value);
        }
        $this->_status->addMessage('info', 'Settings saved.');

        $result = RootScripts::run('write-config.php', [], self::SCRIPT_TIMEOUT);
        if ($result['code'] !== 0) {
            $this->_status->addMessage('error', 'The configuration could not be written: ' . RootScripts::error($result));
        }

        // Every save, not only a changed time: installing is idempotent
        // (only a changed unit is restarted), and a failed install is
        // retried by the next save instead of waiting for an upgrade.
        $result = RootScripts::run('install-timers.php', [], self::SCRIPT_TIMEOUT);
        if ($result['code'] !== 0) {
            $this->_status->addMessage('error', 'The timers could not be updated: ' . RootScripts::error($result));
        } elseif ($store[Settings::BACKUP_TIME] !== $previousTime) {
            $this->_status->addMessage('info', sprintf('The nightly backup now starts at %s.', $store[Settings::BACKUP_TIME]));
        }

        if ($store[Settings::SFTP_HOST] . ':' . $store[Settings::SFTP_PORT] !== $previousHost) {
            $this->_status->addMessage('warning', 'The Storage Box changed: check the connection and trust its host key before the next backup.');
        }

        return true;
    }

    // ----- connection -----

    /**
     * @param array{key: string, error: string} $part from overview.php
     * @return array{key: string, error: string}
     */
    private function publicKey(array $part): array
    {
        if ($part['key'] === '') {
            return ['key' => '', 'error' => $part['error'] !== '' ? $part['error'] : 'the public key could not be read'];
        }
        try {
            return ['key' => HostKey::publicKeyLine($part['key']), 'error' => ''];
        } catch (InvalidArgumentException $e) {
            return ['key' => '', 'error' => $e->getMessage()];
        }
    }

    private function trustHostKey(string $fingerprint): void
    {
        try {
            HostKey::fingerprintArg($fingerprint);
        } catch (InvalidArgumentException $e) {
            $this->_status->addMessage('error', $e->getMessage());
            return;
        }
        $result = RootScripts::run('hostkey.php', ['trust', $fingerprint], self::SCRIPT_TIMEOUT);
        if ($result['code'] !== 0) {
            $this->_status->addMessage('error', 'The host key was not pinned: ' . RootScripts::error($result));
            return;
        }
        $this->_status->addMessage('info', sprintf('The host key %s is pinned.', $fingerprint));
        $this->reportProbe();
    }

    private function reportProbe(): void
    {
        $result = RootScripts::run('probe.php', [], self::SCRIPT_TIMEOUT);
        $data = $result['data'];
        if ($result['code'] !== 0 || !is_array($data) || !is_string($data['message'] ?? null)) {
            $this->_status->addMessage('error', 'The connection test could not run: ' . RootScripts::error($result));
            return;
        }
        $ok = ($data['ok'] ?? false) === true;
        $this->_status->addMessage($ok ? 'info' : 'error', $data['message'] . ($ok ? '' : ' Is the public key in the Storage Box\'s authorized_keys?'));
    }

    // ----- status -----

    /**
     * @param array{code: int, stdout: string, stderr: string, timed_out: bool} $call `relical status`, from overview.php
     * @return array{rows: list<array<string, mixed>>, error: string}
     */
    private function statusRows(array $call): array
    {
        try {
            $rows = Status::rows(Status::parse($call['stdout']));
        } catch (RuntimeException $e) {
            // The core's own error when it printed one, else why the call failed.
            $printed = is_array(json_decode($call['stdout'], true));
            return ['rows' => [], 'error' => $printed ? $e->getMessage() : RootScripts::error($call + ['data' => null])];
        }
        foreach ($rows as &$row) {
            $row['stored'] = Status::bytes($row['stored_bytes']);
            $row['backup_url'] = $row['subscription'] === Paths::SERVER_REPO
                ? ''
                : pm_Context::getActionUrl('index', 'backup') . '?subscription=' . rawurlencode($row['subscription']);
            $row['adopt_url'] = $row['needs_adopt']
                ? pm_Context::getActionUrl('index', 'adopt') . '?subscription=' . rawurlencode($row['subscription'])
                : '';
        }
        unset($row);

        return ['rows' => $rows, 'error' => ''];
    }

    /** @param array{code: int, data: ?array<mixed>, stdout: string, stderr: string, timed_out: bool} $result */
    private function reportAdopt(string $subscription, array $result): void
    {
        $results = is_array($result['data']) && array_is_list($result['data']) ? $result['data'] : [];
        $ok = $result['code'] === 0 && $results !== [] && ($results[0]['ok'] ?? false) === true;
        if ($ok) {
            $this->_status->addMessage('info', sprintf('The repository of %s is adopted. The next backup uses it.', $subscription));
            return;
        }
        $error = is_string($results[0]['error'] ?? null) && $results[0]['error'] !== '' ? $results[0]['error'] : RootScripts::error($result);
        $this->_status->addMessage('error', sprintf('The repository of %s was not adopted: %s', $subscription, $error));
    }

    private function runInProgress(): bool
    {
        $result = RootScripts::run('running.php', [], self::SCRIPT_TIMEOUT);

        return ($result['data']['running'] ?? false) === true;
    }
}

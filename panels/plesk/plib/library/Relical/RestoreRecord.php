<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * The record of one customer restore, as restore-run.php writes it and the
 * status page reads it. Pure.
 *
 *     {id, subscription, unit, request: {...}, state: running|done|failed,
 *      started, finished, result: {...core JSON...}|null, error}
 *
 * The core's error goes into the record whole, for the administrator. A
 * customer sees it only when it is one of the plain messages below and
 * names nothing but their own subscription; any other error (it can name
 * the Storage Box, the repository, the host or another subscription) is
 * replaced by a sentence that sends them to their provider.
 */
final class RestoreRecord
{
    public const BUSY = 'Another restore of this subscription is still running. Start this one when it has finished.';
    public const STOPPED = 'The restore stopped before it finished.';
    public const SERVER_BUSY = 'The server is busy with other restores. Try again later.';
    public const CANNOT_START = 'The restore could not be started just now. Try again later; if it keeps happening, contact your hosting provider.';
    public const READ_BUSY = 'The backups of this subscription are being read for another page. Try again in a moment.';
    /** Files and database restores a customer may start per subscription in DAILY_WINDOW seconds. */
    public const MAX_RESTORES_PER_DAY = 3;
    public const DAILY_WINDOW = 86400;
    public const TOO_MANY_RESTORES = 'This subscription has had 3 website or database restores in the last 24 hours. Try again later, or ask your hosting provider.';
    public const READ_SERVER_BUSY = 'The server is busy reading backups for other customers. Try again in a moment.';

    /** A customer may not start a files or database restore while this many `restored-*` copies are in the home directory. */
    public const MAX_RESTORED_FOLDERS = 5;
    public const TOO_MANY_FOLDERS = 'There are already %d restored copies (restored-...) in the home directory. Delete the ones you no longer need in the File Manager, then restore again.';

    /**
     * Core refusals that come before anything is restored: the target is
     * there already (only the core's up-front wording, errTargetExists and
     * errPartialTarget in core/internal/restore/webspace.go), the request
     * is not in the backup, the disk is too full, the repository is locked,
     * or another run holds relical's lock. A restore refused like this does
     * not count towards MAX_RESTORES_PER_DAY. A folder that appeared while
     * the restore ran (errTargetAppeared) comes after restic ran in full,
     * so that restore counts.
     */
    private const NOTHING_RESTORED = [
        '/ already exists \(it may be from an earlier, possibly incomplete restore of this snapshot\); delete or rename it to restore this snapshot again/',
        '/ already exists and is still owned by root: it is left from a restore of this snapshot that was interrupted; /',
        '/is not in snapshot/',
        '/^no backup snapshots/',
        '/^snapshot [0-9a-f]+ not found/',
        '/has no backup of database/',
        '/not enough free space/',
        '/another relical run holds the lock/',
    ];

    /**
     * The core's words for a repository locked by a check or prune
     * (core/internal/restic LockedMessage). The rest of that error is
     * restic's lock notice, which names the repository and the host, so a
     * customer gets LOCKED instead of the error.
     */
    public const CORE_LOCKED = 'the backup is being checked or pruned, try again in a few minutes';
    public const LOCKED = 'The backup is being checked or pruned. Try again in a few minutes.';

    /** Core errors a customer may read as they are. */
    private const CUSTOMER_SAFE = [
        '/already exists/',
        '/^restored folder \/\S+ appeared in the webspace while the restore ran; nothing was replaced; ask the server\'s administrator$/',
        '/is not in snapshot/',
        '/^no backup snapshots/',
        '/^snapshot [0-9a-f]+ not found/',
        '/has no backup of database/',
    ];

    /**
     * @param list<string> $cliArgs a customer restore (Command::customerRestore)
     * @return array<string, mixed>
     */
    public static function running(string $id, array $cliArgs, int $now, bool $admin = false): array
    {
        Command::restoreId($id);
        Command::customerRestore($cliArgs);
        $request = [];
        for ($i = 1, $n = count($cliArgs); $i + 1 < $n; $i += 2) {
            $request[substr($cliArgs[$i], 2)] = $cliArgs[$i + 1];
        }
        $subscription = $request['subscription'];
        $unit = $request['unit'];
        unset($request['subscription'], $request['unit']);

        return [
            'id' => $id,
            'subscription' => $subscription,
            'unit' => $unit,
            'request' => $request,
            'state' => 'running',
            'by_admin' => $admin,
            'started' => gmdate(DATE_ATOM, $now),
            'finished' => '',
            'result' => null,
            'error' => '',
        ];
    }

    /**
     * The record once the core has answered.
     *
     * @param array<string, mixed> $record from running()
     * @return array<string, mixed>
     */
    public static function finished(array $record, int $code, string $stdout, string $stderr, bool $timedOut, int $now): array
    {
        $data = json_decode(trim($stdout), true);
        $record['finished'] = gmdate(DATE_ATOM, $now);
        if ($code === 0 && !$timedOut && is_array($data) && is_string($data['restored_to'] ?? null) && $data['restored_to'] !== '') {
            $record['state'] = 'done';
            $record['result'] = $data;
            $record['error'] = '';

            return $record;
        }

        $record['state'] = 'failed';
        $record['result'] = is_array($data) ? $data : null;
        if ($timedOut) {
            $record['error'] = 'the restore did not finish in time';
        } elseif (is_array($data) && is_string($data['error'] ?? null) && $data['error'] !== '') {
            $record['error'] = $data['error'];
        } else {
            $lines = array_values(array_filter(array_map('trim', explode("\n", $stderr)), 'strlen'));
            $record['error'] = $lines !== [] ? (string) end($lines) : sprintf('exit %d', $code);
        }

        return $record;
    }

    /**
     * @param array<string, mixed> $record from running()
     * @return array<string, mixed>
     */
    public static function failed(array $record, string $error, int $now): array
    {
        $record['state'] = 'failed';
        $record['finished'] = gmdate(DATE_ATOM, $now);
        $record['error'] = $error;

        return $record;
    }

    /**
     * What the status page shows.
     *
     * @param ?array<string, mixed> $record what restore-result.php returned (null: none yet)
     * @param string $subscription the viewer's subscription: a record of another one reads as missing
     * @param ?int   $startedAt    from the restore id
     * @return array{state: string, unit: string, request: array<string, string>, location: string, restored_to: string, count: string, error: string, refresh: bool}
     *         state: starting, running, done, failed or missing
     */
    public static function view(?array $record, string $subscription, ?int $startedAt, int $now, bool $admin): array
    {
        $view = ['state' => 'missing', 'unit' => '', 'request' => [], 'location' => '', 'restored_to' => '', 'count' => '', 'error' => '', 'refresh' => false];
        if ($record === null || ($record['subscription'] ?? null) !== $subscription) {
            // Just started: restore-run.php has not written the record yet.
            if ($record === null && $startedAt !== null && $now - $startedAt >= -60 && $now - $startedAt < 120) {
                $view['state'] = 'starting';
                $view['refresh'] = true;
            }

            return $view;
        }
        $view['unit'] = is_string($record['unit'] ?? null) ? $record['unit'] : '';
        $view['request'] = array_filter((array) ($record['request'] ?? []), 'is_string');
        $state = $record['state'] ?? '';
        if ($state === 'running') {
            $view['state'] = 'running';
            $view['refresh'] = true;
            return $view;
        }
        if ($state === 'done' && is_array($record['result'] ?? null)) {
            $result = $record['result'];
            $view['state'] = 'done';
            $view['restored_to'] = (string) ($result['restored_to'] ?? '');
            $view['location'] = self::location($view['unit'], $view['restored_to']);
            if (is_int($result['messages'] ?? null)) {
                $view['count'] = sprintf('%d message%s', $result['messages'], $result['messages'] === 1 ? '' : 's');
            } elseif (is_int($result['files'] ?? null)) {
                $view['count'] = sprintf('%d file%s, %s', $result['files'], $result['files'] === 1 ? '' : 's', Status::bytes((int) ($result['bytes'] ?? 0)));
            } elseif (is_int($result['bytes'] ?? null)) {
                $view['count'] = Status::bytes($result['bytes']);
            }
            return $view;
        }
        $view['state'] = 'failed';
        $error = is_string($record['error'] ?? null) ? $record['error'] : '';
        $view['error'] = $admin ? $error : self::customerError($error, $subscription);

        return $view;
    }

    /**
     * Why a new restore must not start, from restore-running.php's answer,
     * or null when it may. A missing or unreadable answer refuses (fails
     * closed). Administrators are not held to the count of restored copies.
     *
     * @param ?array<mixed> $state {running, server_busy, restored_folders, recent_restores}
     */
    public static function startRefusal(?array $state, string $unit, bool $admin): ?string
    {
        if (!is_array($state) || !is_bool($state['running'] ?? null) || !is_bool($state['server_busy'] ?? null) || !is_int($state['restored_folders'] ?? null)) {
            return self::CANNOT_START;
        }
        if ($state['running']) {
            return self::BUSY;
        }
        if ($state['server_busy']) {
            return self::SERVER_BUSY;
        }
        if (!$admin && $unit !== 'mail' && $state['restored_folders'] >= self::MAX_RESTORED_FOLDERS) {
            return sprintf(self::TOO_MANY_FOLDERS, $state['restored_folders']);
        }
        // restore-run.php enforces this as root; checked here too so the customer is told before a record is made.
        if (!$admin && $unit !== 'mail' && is_int($state['recent_restores'] ?? null) && $state['recent_restores'] >= self::MAX_RESTORES_PER_DAY) {
            return self::TOO_MANY_RESTORES;
        }

        return null;
    }

    /**
     * How many files or database restores customers started for the
     * subscription in the DAILY_WINDOW seconds before $now, from its
     * restore records. Only restores that ran count: restores started by an
     * administrator, starts refused before the core ran (busy, server busy,
     * over the limit) and restores the core refused before restoring
     * anything (NOTHING_RESTORED, a locked repository) do not.
     *
     * @param iterable<mixed> $records
     */
    public static function recentCustomerRestores(iterable $records, string $subscription, int $now): int
    {
        $count = 0;
        foreach ($records as $record) {
            if (!is_array($record) || ($record['subscription'] ?? null) !== $subscription
                || !in_array($record['unit'] ?? null, ['files', 'db'], true) || ($record['by_admin'] ?? false) === true
                || (($record['state'] ?? '') === 'failed' && self::nothingRestored(is_string($record['error'] ?? null) ? $record['error'] : ''))) {
                continue;
            }
            $started = is_string($record['started'] ?? null) ? strtotime($record['started']) : false;
            if ($started !== false && $started <= $now && $now - $started < self::DAILY_WINDOW) {
                $count++;
            }
        }

        return $count;
    }

    /** Whether a failed restore's error says it was refused before anything was restored. */
    private static function nothingRestored(string $error): bool
    {
        if (in_array($error, [self::BUSY, self::SERVER_BUSY, self::TOO_MANY_RESTORES], true) || str_contains($error, self::CORE_LOCKED)) {
            return true;
        }
        foreach (self::NOTHING_RESTORED as $pattern) {
            if (preg_match($pattern, $error) === 1) {
                return true;
            }
        }

        return false;
    }

    /**
     * The error a customer may see: one of the plain core errors, unless it
     * names anything beyond the customer's own subscription ($subscription,
     * its subdomains, paths under its home, its own mailboxes).
     */
    public static function customerError(string $error, string $subscription = ''): string
    {
        if (in_array($error, [self::BUSY, self::STOPPED, self::SERVER_BUSY, self::TOO_MANY_RESTORES], true)) {
            return $error;
        }
        if (str_contains($error, self::CORE_LOCKED)) {
            return self::LOCKED;
        }
        foreach (self::CUSTOMER_SAFE as $pattern) {
            if (preg_match($pattern, $error) === 1 && !self::namesMore($error, $subscription)) {
                return ucfirst($error) . (str_ends_with($error, '.') ? '' : '.');
            }
        }

        return 'The restore could not be completed. Nothing was changed. Contact your hosting provider and quote the reference on this page.';
    }

    /**
     * Whether a core error names anything a customer of $subscription must
     * not read: the repository or its sftp: address, the Storage Box, a
     * host, /etc/relical, a path outside the subscription's home, or a
     * domain other than the subscription and its subdomains. Quoted text is
     * the customer's own request echoed back (a path, a folder name) and is
     * not looked at; a mail login (user@domain) is one of the subscription's
     * mailboxes, as the core only restores those.
     */
    private static function namesMore(string $error, string $subscription): bool
    {
        if (preg_match('/sftp:|repository|repo\b|storagebox|storage box|\/etc\/|relical\/|\bhost\b|\bssh\b/i', $error) === 1) {
            return true;
        }
        $rest = (string) preg_replace('/"(?:[^"\\\\]|\\\\.)*"/', '""', $error);
        $sub = preg_quote($subscription, '#');
        foreach (preg_match_all('#/[^\s"]+#', $rest, $m) > 0 ? $m[0] : [] as $path) {
            // Under the home: no dotted name before the subscription's own.
            if ($subscription === '' || preg_match('#^(?:/[^/.]+)*/' . $sub . '(?:/|$)#', rtrim($path, '.,;:')) !== 1) {
                return true;
            }
        }
        $rest = (string) preg_replace('#/[^\s"]+#', '', $rest);
        $rest = (string) preg_replace('/[^\s@"]+@[a-z0-9.-]+/i', '', $rest);
        foreach (preg_match_all('/(?:[a-z0-9-]+\.)+[a-z][a-z0-9-]*/i', $rest, $m) > 0 ? $m[0] : [] as $domain) {
            $domain = strtolower($domain);
            if ($subscription === '' || ($domain !== $subscription && !str_ends_with($domain, '.' . $subscription))) {
                return true;
            }
        }

        return false;
    }

    public const NO_BACKUPS_YET = 'There are no backups of this subscription yet. The first one is made during the next nightly backup.';
    public const CANNOT_READ = 'The backups could not be read just now. Try again later; if it keeps happening, contact your hosting provider.';

    /**
     * What a customer sees when the restore points or mailboxes could not be
     * read: "no backups yet" for the core's words before a subscription's
     * first backup (no key yet, no repository, no snapshots), LOCKED while a
     * check or prune holds the repository, else a plain sentence. The core's
     * message itself is never shown to a customer.
     */
    public static function customerReadError(string $message): string
    {
        if ($message === self::READ_BUSY || $message === self::READ_SERVER_BUSY) {
            return $message;
        }
        if (str_contains($message, self::CORE_LOCKED)) {
            return self::LOCKED;
        }
        if (preg_match('/repository does not exist|no backup snapshots|no key for|no key file/i', $message) === 1) {
            return self::NO_BACKUPS_YET;
        }

        return self::CANNOT_READ;
    }

    /**
     * Where the restored copy is, as the customer finds it: the IMAP folder
     * for mail, without Plesk's `INBOX.` namespace prefix that webmail and
     * mail programs do not show, else the path from the
     * `restored-<date>-<id>` folder in the subscription's home directory
     * (above httpdocs) down.
     */
    public static function location(string $unit, string $restoredTo): string
    {
        if ($unit === 'mail') {
            return str_starts_with($restoredTo, ConfigWriter::MAIL_FOLDER_PREFIX)
                ? substr($restoredTo, strlen(ConfigWriter::MAIL_FOLDER_PREFIX))
                : $restoredTo;
        }
        $at = strpos($restoredTo, '/restored-');

        return $at === false ? basename($restoredTo) : substr($restoredTo, $at + 1);
    }
}

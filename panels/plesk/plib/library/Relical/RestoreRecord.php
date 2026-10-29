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
 * customer sees it only when it is one of the plain messages below; any
 * other error (it can name the Storage Box or the repository) is replaced
 * by a sentence that sends them to their provider.
 */
final class RestoreRecord
{
    public const BUSY = 'Another restore of this subscription is still running. Start this one when it has finished.';
    public const STOPPED = 'The restore stopped before it finished.';

    /** Core errors a customer may read as they are. */
    private const CUSTOMER_SAFE = [
        '/already exists/',
        '/is not in snapshot/',
        '/^no backup snapshots/',
        '/^snapshot [0-9a-f]+ not found/',
        '/has no backup of database/',
    ];

    /**
     * @param list<string> $cliArgs a customer restore (Command::customerRestore)
     * @return array<string, mixed>
     */
    public static function running(string $id, array $cliArgs, int $now): array
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
        $view['error'] = $admin ? $error : self::customerError($error);

        return $view;
    }

    /** The error a customer may see. */
    public static function customerError(string $error): string
    {
        if ($error === self::BUSY || $error === self::STOPPED) {
            return $error;
        }
        foreach (self::CUSTOMER_SAFE as $pattern) {
            if (preg_match($pattern, $error) === 1 && preg_match('/sftp:|repository|storagebox|\/etc\/relical/i', $error) !== 1) {
                return ucfirst($error) . (str_ends_with($error, '.') ? '' : '.');
            }
        }

        return 'The restore could not be completed. Nothing was changed. Contact your hosting provider and quote the reference on this page.';
    }

    public const NO_BACKUPS_YET = 'There are no backups of this subscription yet. The first one is made during the next nightly backup.';
    public const CANNOT_READ = 'The backups could not be read just now. Try again later; if it keeps happening, contact your hosting provider.';

    /**
     * What a customer sees when the restore points or mailboxes could not be
     * read: "no backups yet" for the core's words before a subscription's
     * first backup (no key yet, no repository, no snapshots), else a plain
     * sentence. The core's message itself is never shown to a customer.
     */
    public static function customerReadError(string $message): string
    {
        if (preg_match('/repository does not exist|no backup snapshots|no key for|no key file/i', $message) === 1) {
            return self::NO_BACKUPS_YET;
        }

        return self::CANNOT_READ;
    }

    /**
     * Where the restored copy is, as the customer finds it: the IMAP folder
     * for mail, else the path from the `restored-<date>-<id>` folder in the
     * subscription's home directory (above httpdocs) down.
     */
    public static function location(string $unit, string $restoredTo): string
    {
        if ($unit === 'mail') {
            return $restoredTo;
        }
        $at = strpos($restoredTo, '/restored-');

        return $at === false ? basename($restoredTo) : substr($restoredTo, $at + 1);
    }
}

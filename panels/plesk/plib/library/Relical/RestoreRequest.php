<?php

declare(strict_types=1);

namespace Noiz\Relical;

use DateTimeImmutable;
use DateTimeZone;
use Exception;
use InvalidArgumentException;
use RuntimeException;

/**
 * Builds the `cli.php` argument lists of the customer restore screens and
 * reads the core's answers. Pure.
 *
 * Nothing from the form is passed on as it came: a mailbox must be one the
 * core listed for the subscription (`mailboxes`), a restore point one the
 * core listed (`snapshots`) with the right unit, a database the one named by
 * that restore point's `db:<name>` tag, and a path must pass the core's rule
 * for paths inside the webspace. Every list ends in
 * Command::customerRestore(), so the allowlist checks it once more.
 */
final class RestoreRequest
{
    private const SNAPSHOT_ID = '/^[0-9a-f]{8,64}$/D';

    // ----- reading the screens' data -----

    /** @return list<string> */
    public static function snapshotsArgs(string $subscription): array
    {
        return ['snapshots', '--subscription', Command::domain($subscription)];
    }

    /** @return list<string> */
    public static function mailboxesArgs(string $subscription): array
    {
        return ['mailboxes', '--subscription', Command::domain($subscription)];
    }

    /** @return list<string> */
    public static function lsArgs(string $subscription, string $snapshot, string $path): array
    {
        $args = ['ls', '--subscription', Command::domain($subscription), '--snapshot', self::snapshotId($snapshot)];
        $path = self::path($path);
        if ($path !== '') {
            $args[] = '--path';
            $args[] = $path;
        }
        Command::parse($args);

        return $args;
    }

    /**
     * The restore points of each unit, newest first, from `snapshots`.
     * A db snapshot holds one database (tagged `db:<name>`); one without a
     * valid name tag is left out, as is anything that is not mail, files or
     * db.
     *
     * @param mixed $data the decoded output of `snapshots`
     * @return array{mail: list<array{id: string, short: string, time: string, label: string, database: string}>, files: list<array{id: string, short: string, time: string, label: string, database: string}>, db: list<array{id: string, short: string, time: string, label: string, database: string}>}
     * @throws RuntimeException when the output is not a list of snapshots
     */
    public static function restorePoints($data, ?DateTimeZone $zone = null): array
    {
        if (!is_array($data) || !array_is_list($data)) {
            throw new RuntimeException(is_array($data) && is_string($data['error'] ?? null) ? $data['error'] : 'the list of backups could not be read');
        }
        $zone ??= new DateTimeZone(date_default_timezone_get());
        $points = ['mail' => [], 'files' => [], 'db' => []];
        foreach ($data as $snapshot) {
            if (!is_array($snapshot) || !is_string($snapshot['id'] ?? null) || preg_match(self::SNAPSHOT_ID, $snapshot['id']) !== 1) {
                continue;
            }
            if (!is_string($snapshot['time'] ?? null) || $snapshot['time'] === '') {
                continue;
            }
            try {
                $time = new DateTimeImmutable($snapshot['time']);
            } catch (Exception $e) {
                continue;
            }
            $tags = array_values(array_filter((array) ($snapshot['tags'] ?? []), 'is_string'));
            $point = [
                'id' => $snapshot['id'],
                'short' => substr($snapshot['id'], 0, 8),
                'time' => $time->format(DATE_ATOM),
                'label' => $time->setTimezone($zone)->format('Y-m-d H:i'),
                'database' => '',
                'sort' => $time->getTimestamp(),
            ];
            if (in_array('mail', $tags, true)) {
                $points['mail'][] = $point;
            } elseif (in_array('files', $tags, true)) {
                $points['files'][] = $point;
            } elseif (in_array('db', $tags, true)) {
                foreach ($tags as $tag) {
                    if (str_starts_with($tag, 'db:')) {
                        try {
                            $point['database'] = Command::database(substr($tag, 3));
                        } catch (InvalidArgumentException $e) {
                            continue;
                        }
                        $points['db'][] = $point;
                        break;
                    }
                }
            }
        }
        foreach ($points as &$list) {
            usort($list, static fn (array $a, array $b): int => [$b['sort'], $a['database']] <=> [$a['sort'], $b['database']]);
            $list = array_map(static function (array $p): array {
                unset($p['sort']);
                return $p;
            }, $list);
        }
        unset($list);

        return $points;
    }

    /**
     * The subscription's mailboxes from `mailboxes`, keyed by address.
     *
     * @param mixed $data the decoded output of `mailboxes`
     * @return array<string, array{domain: string, mailbox: string}> address => parts, sorted
     * @throws RuntimeException when the output is not a list of mailboxes
     */
    public static function mailboxes($data): array
    {
        if (!is_array($data) || !array_is_list($data)) {
            throw new RuntimeException(is_array($data) && is_string($data['error'] ?? null) ? $data['error'] : 'the list of mailboxes could not be read');
        }
        $out = [];
        foreach ($data as $box) {
            if (!is_array($box) || !is_string($box['domain'] ?? null) || !is_string($box['mailbox'] ?? null)) {
                continue;
            }
            try {
                $domain = Command::domain($box['domain']);
                $mailbox = Command::mailbox($box['mailbox']);
            } catch (InvalidArgumentException $e) {
                continue;
            }
            $out[$mailbox . '@' . $domain] = ['domain' => $domain, 'mailbox' => $mailbox];
        }
        ksort($out, SORT_STRING);

        return $out;
    }

    /**
     * One directory level of a files restore point, from `ls`: folders
     * first, then files, by name. Each entry carries the path to browse to
     * (folders only, and only when the name makes a valid path).
     *
     * @param mixed  $data the decoded output of `ls`
     * @param string $path the path that was listed (relative, '' for the top)
     * @return array{entries: list<array{name: string, type: string, size: int, mtime: string, browse: string}>, truncated: bool}
     * @throws RuntimeException when the output is not a listing
     */
    public static function listing($data, string $path): array
    {
        if (!is_array($data) || !is_array($data['entries'] ?? null)) {
            throw new RuntimeException(is_array($data) && is_string($data['error'] ?? null) ? $data['error'] : 'the folder could not be listed');
        }
        $path = self::path($path);
        $entries = [];
        foreach ($data['entries'] as $entry) {
            if (!is_array($entry) || !is_string($entry['name'] ?? null) || $entry['name'] === '' || str_contains($entry['name'], '/')) {
                continue;
            }
            $type = is_string($entry['type'] ?? null) ? $entry['type'] : '';
            $browse = '';
            if ($type === 'dir') {
                try {
                    $browse = self::path($path === '' ? $entry['name'] : $path . '/' . $entry['name']);
                } catch (InvalidArgumentException $e) {
                    $browse = '';
                }
            }
            $entries[] = [
                'name' => $entry['name'],
                'type' => $type,
                'size' => is_int($entry['size'] ?? null) ? $entry['size'] : 0,
                'mtime' => is_string($entry['mtime'] ?? null) ? $entry['mtime'] : '',
                'browse' => $browse,
            ];
        }
        usort($entries, static fn (array $a, array $b): int => [$a['type'] !== 'dir', $a['name']] <=> [$b['type'] !== 'dir', $b['name']]);

        return ['entries' => $entries, 'truncated' => ($data['truncated'] ?? false) === true];
    }

    // ----- building a restore from the form -----

    /**
     * @param array<string, mixed> $input  mailbox (an address), snapshot, folder (optional)
     * @param array<string, array{domain: string, mailbox: string}> $mailboxes from mailboxes()
     * @param array{mail: list<array<string, string>>} $points from restorePoints()
     * @return list<string> the arguments after cli.php
     */
    public static function mail(string $subscription, array $input, array $mailboxes, array $points): array
    {
        $address = self::field($input, 'mailbox');
        if (!isset($mailboxes[$address])) {
            throw new InvalidArgumentException('Choose one of the mailboxes in the list.');
        }
        $point = self::point($points['mail'] ?? [], self::field($input, 'snapshot'));
        $args = [
            'restore', '--subscription', Command::domain($subscription), '--unit', 'mail',
            '--domain', $mailboxes[$address]['domain'], '--mailbox', $mailboxes[$address]['mailbox'],
            '--snapshot', $point['id'],
        ];
        $folder = trim(self::field($input, 'folder'));
        if ($folder !== '') {
            try {
                $args[] = '--folder';
                $args[] = Command::folder($folder);
            } catch (InvalidArgumentException $e) {
                throw new InvalidArgumentException('The folder name is not valid: ' . $e->getMessage() . '.');
            }
        }

        return Command::customerRestore($args);
    }

    /**
     * @param array<string, mixed> $input snapshot, path (relative), what: "folder" or "all"
     * @param array{files: list<array<string, string>>} $points from restorePoints()
     * @return list<string>
     */
    public static function files(string $subscription, array $input, array $points): array
    {
        $point = self::point($points['files'] ?? [], self::field($input, 'snapshot'));
        $args = ['restore', '--subscription', Command::domain($subscription), '--unit', 'files', '--snapshot', $point['id']];
        if (self::field($input, 'what') !== 'all') {
            $path = self::path(self::field($input, 'path'));
            if ($path !== '') {
                $args[] = '--path';
                $args[] = $path;
            }
        }

        return Command::customerRestore($args);
    }

    /**
     * The database comes from the restore point's own `db:<name>` tag, never
     * from the form.
     *
     * @param array<string, mixed> $input snapshot
     * @param array{db: list<array<string, string>>} $points from restorePoints()
     * @return list<string>
     */
    public static function database(string $subscription, array $input, array $points): array
    {
        $point = self::point($points['db'] ?? [], self::field($input, 'snapshot'));

        return Command::customerRestore([
            'restore', '--subscription', Command::domain($subscription), '--unit', 'db',
            '--database', $point['database'], '--snapshot', $point['id'],
        ]);
    }

    // ----- paths and references -----

    /**
     * A path inside the webspace as the core takes it: relative, without a
     * leading or trailing "/", no empty, "." or ".." segment. '' is the top.
     */
    public static function path(string $value): string
    {
        $value = trim($value, '/');
        if ($value === '') {
            return '';
        }
        if (str_contains($value, '//')) {
            throw new InvalidArgumentException('path must not contain empty segments');
        }

        return Command::path($value);
    }

    /**
     * The breadcrumb of a path: the top, then each folder down to it.
     *
     * @return list<array{name: string, path: string}>
     */
    public static function breadcrumb(string $path): array
    {
        $crumbs = [['name' => '', 'path' => '']];
        $so = '';
        foreach (self::path($path) === '' ? [] : explode('/', self::path($path)) as $part) {
            $so = $so === '' ? $part : $so . '/' . $part;
            $crumbs[] = ['name' => $part, 'path' => $so];
        }

        return $crumbs;
    }

    /**
     * A new restore's reference: its start time (UTC) and 96 random bits.
     *
     * @param string $random 12 random bytes (random_bytes(12))
     */
    public static function newId(int $now, string $random): string
    {
        if (strlen($random) !== 12) {
            throw new InvalidArgumentException('a restore id needs 12 random bytes');
        }

        return Command::restoreId(gmdate('YmdHis', $now) . '-' . bin2hex($random));
    }

    /** When a restore was started, from its reference (null when not a reference). */
    public static function startedAt(string $id): ?int
    {
        try {
            Command::restoreId($id);
        } catch (InvalidArgumentException $e) {
            return null;
        }
        $time = DateTimeImmutable::createFromFormat('!YmdHis', substr($id, 0, 14), new DateTimeZone('UTC'));

        return $time === false ? null : $time->getTimestamp();
    }

    public static function snapshotId(string $value): string
    {
        if (preg_match(self::SNAPSHOT_ID, $value) !== 1) {
            throw new InvalidArgumentException('Choose one of the restore points in the list.');
        }

        return $value;
    }

    // ----- helpers -----

    /**
     * @param list<array<string, string>> $points
     * @return array<string, string>
     */
    private static function point(array $points, string $id): array
    {
        foreach ($points as $point) {
            if (($point['id'] ?? null) === $id && $id !== '') {
                return $point;
            }
        }

        throw new InvalidArgumentException('Choose one of the restore points in the list.');
    }

    /** @param array<string, mixed> $input */
    private static function field(array $input, string $name): string
    {
        $value = $input[$name] ?? '';

        return is_string($value) ? $value : '';
    }
}

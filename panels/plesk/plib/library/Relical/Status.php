<?php

declare(strict_types=1);

namespace Noiz\Relical;

use DateTimeImmutable;
use DateTimeZone;
use RuntimeException;
use Throwable;

/**
 * The administrator's status table, from `relical status` (state.json:
 * subscription -> unit key -> action -> last result). Pure: the controller
 * runs `cli.php status` and hands the output here.
 */
final class Status
{
    /** The unit key the core uses for forget, check and stats results. */
    public const REPOSITORY_KEY = 'repository';

    /**
     * @return array<string, array<string, array<string, array<string, mixed>>>> the state
     * @throws RuntimeException with the core's error, or when the output is not a state
     */
    public static function parse(string $stdout): array
    {
        try {
            $data = json_decode($stdout, true, 16, JSON_THROW_ON_ERROR);
        } catch (Throwable $e) {
            throw new RuntimeException('relical status did not print JSON');
        }
        if (!is_array($data)) {
            throw new RuntimeException('relical status did not print a state');
        }
        if (isset($data['error']) && is_string($data['error'])) {
            throw new RuntimeException($data['error']);
        }
        $state = [];
        foreach ($data as $subscription => $units) {
            if (!is_string($subscription) || !is_array($units)) {
                continue;
            }
            foreach ($units as $unit => $actions) {
                if (!is_string($unit) || !is_array($actions)) {
                    continue;
                }
                foreach ($actions as $action => $result) {
                    if (is_string($action) && is_array($result)) {
                        $state[$subscription][$unit][$action] = $result;
                    }
                }
            }
        }

        return $state;
    }

    /**
     * One row per subscription, sorted by name with `_server` last.
     *
     * @param array<string, array<string, array<string, array<string, mixed>>>> $state
     * @return list<array{subscription: string, units: list<array{unit: string, ok: bool, time: string}>, last_success: string, last_error: string, stored_bytes: ?int}>
     */
    public static function rows(array $state, ?DateTimeZone $zone = null): array
    {
        $zone ??= new DateTimeZone(date_default_timezone_get());
        $names = array_keys($state);
        usort($names, static fn (string $a, string $b): int => [$a === Paths::SERVER_REPO, $a] <=> [$b === Paths::SERVER_REPO, $b]);

        $rows = [];
        foreach ($names as $subscription) {
            $units = [];
            $lastSuccess = null;
            $errors = [];
            $stored = null;
            $keys = array_keys($state[$subscription]);
            usort($keys, [self::class, 'unitOrder']);
            foreach ($keys as $unit) {
                foreach ($state[$subscription][$unit] as $action => $result) {
                    $time = self::time($result['time'] ?? null);
                    $ok = ($result['ok'] ?? false) === true;
                    if ($unit === self::REPOSITORY_KEY && $action === 'stats' && $ok && is_int($result['stored_bytes'] ?? null)) {
                        $stored = $result['stored_bytes'];
                    }
                    if ($unit !== self::REPOSITORY_KEY && $action === 'backup') {
                        $units[] = ['unit' => $unit, 'ok' => $ok, 'time' => self::show($time, $zone)];
                        if ($ok && $time !== null && ($lastSuccess === null || $time > $lastSuccess)) {
                            $lastSuccess = $time;
                        }
                    }
                    if (!$ok) {
                        $what = $unit === self::REPOSITORY_KEY ? $action : $unit . ' ' . $action;
                        $error = trim((string) ($result['error'] ?? '')) ?: 'failed';
                        $errors[] = [$time, sprintf('%s: %s (%s)', $what, $error, self::show($time, $zone))];
                    }
                }
            }
            usort($errors, static fn (array $a, array $b): int => $b[0] <=> $a[0]);
            $rows[] = [
                'subscription' => $subscription,
                'units' => $units,
                'last_success' => self::show($lastSuccess, $zone),
                'last_error' => implode('; ', array_column($errors, 1)),
                'stored_bytes' => $stored,
            ];
        }

        return $rows;
    }

    /** Bytes for people: 1023 B, 1.5 KiB, 2.0 GiB. */
    public static function bytes(?int $bytes): string
    {
        if ($bytes === null) {
            return '';
        }
        $units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB'];
        $value = (float) $bytes;
        $i = 0;
        while ($value >= 1024 && $i < count($units) - 1) {
            $value /= 1024;
            $i++;
        }

        return $i === 0 ? sprintf('%d B', $bytes) : sprintf('%.1f %s', $value, $units[$i]);
    }

    /**
     * Whether a process holds an flock on the file with inode $inode,
     * from the contents of /proc/locks. Reading /proc/locks takes no lock
     * itself, so asking never gets in the way of a run that is starting.
     */
    public static function lockHeld(string $procLocks, int $inode): bool
    {
        foreach (preg_split('/\R/', $procLocks) ?: [] as $line) {
            // "1: FLOCK  ADVISORY  WRITE 4242 08:01:131074 0 EOF"
            if (preg_match('/^\d+:\s+(?:->\s+)?FLOCK\s+\S+\s+\S+\s+\d+\s+[0-9a-f]+:[0-9a-f]+:(\d+)\s/i', $line, $m) === 1 && (int) $m[1] === $inode) {
                return true;
            }
        }

        return false;
    }

    private static function unitOrder(string $a, string $b): int
    {
        $rank = static function (string $unit): array {
            $order = ['mail' => 0, 'files' => 1, 'db' => 2, 'panel-config' => 4, self::REPOSITORY_KEY => 5];
            if (str_starts_with($unit, 'db:')) {
                return [3, $unit];
            }

            return [$order[$unit] ?? 3, $unit];
        };

        return $rank($a) <=> $rank($b);
    }

    /** @param mixed $value */
    private static function time($value): ?DateTimeImmutable
    {
        if (!is_string($value) || $value === '' || str_starts_with($value, '0001-01-01')) {
            return null;
        }
        try {
            return new DateTimeImmutable($value);
        } catch (Throwable $e) {
            return null;
        }
    }

    private static function show(?DateTimeImmutable $time, DateTimeZone $zone): string
    {
        return $time === null ? '' : $time->setTimezone($zone)->format('Y-m-d H:i');
    }
}

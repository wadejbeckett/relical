<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;
use Throwable;

/**
 * The settings page's read-only data in one root call (overview.php): the
 * extension's public key, `relical status`, `relical report` and the last
 * failure notify.php recorded. One
 * `extension --exec` instead of three in a row, each core call cut short
 * (timeout()) so a slow or hung core cannot hold the page.
 */
final class Overview
{
    /** `relical status` inside overview.php: it reads only the state file. */
    public const CORE_TIMEOUT = 6.0;

    /**
     * `relical report` inside overview.php: it also loads the inventory
     * (inventory_command, which enumerates every subscription through Plesk),
     * so it gets longer (Task 9 review finding 6; TEST-PLAN times it).
     */
    public const REPORT_TIMEOUT = 12.0;

    /** How long the page waits for overview.php as a whole (both calls in a row, plus PHP's start). */
    public const TIMEOUT = 21.0;

    /** @return float the limit of one core call inside overview.php */
    public static function timeout(string $command): float
    {
        return $command === 'report' ? self::REPORT_TIMEOUT : self::CORE_TIMEOUT;
    }

    /** The public key may be at most this long (an ed25519 line is about 100 bytes). */
    private const KEY_READ_LIMIT = 4096;

    /**
     * The public key as one validated `ssh-ed25519` line.
     *
     * @return array{key: string, error: string}
     */
    public static function publicKey(string $path): array
    {
        try {
            if (is_link($path) || !is_file($path)) {
                throw new RuntimeException(sprintf('%s is missing; run the installer again (plesk bin extension --exec relical post-install.php)', $path));
            }
            $content = @file_get_contents($path, false, null, 0, self::KEY_READ_LIMIT);
            if ($content === false) {
                throw new RuntimeException(sprintf('could not read %s', $path));
            }

            return ['key' => HostKey::publicKeyLine($content), 'error' => ''];
        } catch (Throwable $e) {
            return ['key' => '', 'error' => $e->getMessage()];
        }
    }

    /**
     * A core call's outcome as overview.php prints it.
     *
     * @param array{code: int, stdout: string, stderr: string, timed_out: bool} $result
     * @return array{code: int, stdout: string, stderr: string, timed_out: bool}
     */
    public static function call(array $result): array
    {
        return [
            'code' => $result['code'],
            'stdout' => $result['stdout'],
            'stderr' => $result['stderr'],
            'timed_out' => $result['timed_out'],
        ];
    }

    /**
     * Reads overview.php's answer, whatever came back: a part that is
     * missing or of the wrong shape reads as a failed call whose stderr says
     * why ($error: RootScripts::error() of the whole call).
     *
     * @param ?array<mixed> $data
     * @return array{public_key: array{key: string, error: string}, status: array{code: int, stdout: string, stderr: string, timed_out: bool}, report: array{code: int, stdout: string, stderr: string, timed_out: bool}, last_failure: ?array{unit: string, time: string}, config_destination: string}
     */
    public static function parse(?array $data, string $error): array
    {
        $key = $data['public_key'] ?? null;
        $out = [
            'public_key' => is_array($key) && is_string($key['key'] ?? null) && is_string($key['error'] ?? null)
                ? ['key' => $key['key'], 'error' => $key['error']]
                : ['key' => '', 'error' => $error],
        ];
        $out['last_failure'] = Notify::parseRecord($data['last_failure'] ?? null);
        $out['config_destination'] = is_string($data['config_destination'] ?? null) ? $data['config_destination'] : '';
        foreach (['status', 'report'] as $part) {
            $call = $data[$part] ?? null;
            $out[$part] = is_array($call) && is_int($call['code'] ?? null) && is_string($call['stdout'] ?? null) && is_string($call['stderr'] ?? null)
                ? ['code' => $call['code'], 'stdout' => $call['stdout'], 'stderr' => $call['stderr'], 'timed_out' => ($call['timed_out'] ?? false) === true]
                : ['code' => -1, 'stdout' => '', 'stderr' => $error, 'timed_out' => false];
            if ($out[$part]['timed_out']) {
                $out[$part]['stderr'] = trim($out[$part]['stderr'] . "\nrelical did not answer within " . (int) self::timeout($part) . ' seconds');
            }
        }

        return $out;
    }

    /**
     * The last failure notify.php recorded, or null when there is none or it
     * cannot be read.
     *
     * @return ?array{unit: string, time: string}
     */
    public static function lastFailure(string $path): ?array
    {
        if (is_link($path) || !is_file($path)) {
            return null;
        }

        return Notify::parseRecord(json_decode((string) @file_get_contents($path, false, null, 0, 4096), true));
    }

    /**
     * The line at the top of the settings page: when a backup last
     * succeeded, how many units failed or were skipped in the last report,
     * and the last failed run.
     *
     * @param list<array{last_success: string}>                          $rows        Status::rows()
     * @param array{available: bool, failed_count: int, skipped_count: int} $report      Report::fromCli()
     * @param ?array{unit: string, time: string}                         $lastFailure
     * @return array{last_success: string, failed: ?int, skipped: ?int, last_failure: string}
     */
    public static function headline(array $rows, array $report, ?array $lastFailure, ?\DateTimeZone $zone = null): array
    {
        $lastSuccess = '';
        foreach ($rows as $row) {
            // Status::show() gives 'Y-m-d H:i', which sorts as text.
            if (($row['last_success'] ?? '') > $lastSuccess) {
                $lastSuccess = (string) $row['last_success'];
            }
        }
        $failure = '';
        if ($lastFailure !== null) {
            $zone ??= new \DateTimeZone(date_default_timezone_get());
            $when = (new \DateTimeImmutable($lastFailure['time']))->setTimezone($zone)->format('Y-m-d H:i');
            $failure = sprintf('%s at %s', $lastFailure['unit'] === Units::BACKUP_SERVICE ? 'the nightly backup' : 'the weekly check', $when);
        }

        return [
            'last_success' => $lastSuccess,
            'failed' => $report['available'] ? $report['failed_count'] : null,
            'skipped' => $report['available'] ? $report['skipped_count'] : null,
            'last_failure' => $failure,
        ];
    }

    /**
     * The destination config.json sends backups to, as Destination::display()
     * shows it, or '' when there is no configuration or it cannot be read.
     * After a reinstall Plesk's settings are empty while the old config.json
     * is still in force; the settings page says so.
     */
    public static function configuredDestination(string $path): string
    {
        if (is_link($path) || !is_file($path)) {
            return '';
        }
        $config = json_decode((string) @file_get_contents($path, false, null, 0, 65536), true);
        if (!is_array($config)) {
            return '';
        }
        $name = $config['default_destination'] ?? null;
        $destination = is_string($name) ? ($config['destinations'][$name] ?? null) : null;
        if (!is_array($destination) || !is_string($destination['repository'] ?? null)
            || preg_match('#^sftp:([^@/\s]+)@([^:/\s]+):(\S*?)/\{subscription\}$#D', $destination['repository'], $m) !== 1) {
            return '';
        }
        $port = 22;
        if (is_string($destination['sftp_args'] ?? null) && preg_match('/(?:^|\s)-p (\d{1,5})(?:\s|$)/', $destination['sftp_args'], $p) === 1) {
            $port = (int) $p[1];
        }

        return sprintf('%s@%s:%s (port %d)', $m[1], $m[2], $m[3], $port);
    }
}

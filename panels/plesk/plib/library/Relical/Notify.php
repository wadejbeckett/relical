<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;
use SimpleXMLElement;

/**
 * What notify.php does when the backup or check service fails
 * (`OnFailure=relical-notify@%n.service`): a line in the run log, the
 * failure kept for the settings page, and a mail to the Plesk
 * administrator. Pure apart from nothing; notify.php does the writing.
 *
 * The administrator's address comes from API-RPC
 * `<server><get><admin/></get></server>` (`admin/admin_email`), with
 * `pm_Client::getByLogin('admin')` as the fallback. Without either (no
 * Plesk SDK) the failure is logged only.
 */
final class Notify
{
    /** The units whose failure is reported; notify.php refuses anything else. */
    public const UNITS = [Units::BACKUP_SERVICE, Units::CHECK_SERVICE];

    /** The API-RPC request for the administrator's contact details. */
    public const ADMIN_REQUEST = '<server><get><admin/></get></server>';

    /** Log lines included in the mail. */
    public const MAIL_LOG_LINES = 30;

    public static function unit(string $unit): string
    {
        if (!in_array($unit, self::UNITS, true)) {
            throw new InvalidArgumentException(sprintf('notify.php takes one of %s', implode(', ', self::UNITS)));
        }

        return $unit;
    }

    public static function logLine(string $unit, int $now): string
    {
        return sprintf("%s relical-notify: %s failed; see journalctl -u %s\n", gmdate(DATE_ATOM, $now), self::unit($unit), $unit);
    }

    /** @return array{unit: string, time: string} what the settings page shows */
    public static function record(string $unit, int $now): array
    {
        return ['unit' => self::unit($unit), 'time' => gmdate(DATE_ATOM, $now)];
    }

    /**
     * A failure record read back from disk, or null when it is missing or malformed.
     *
     * @param mixed $data
     * @return ?array{unit: string, time: string}
     */
    public static function parseRecord($data): ?array
    {
        if (!is_array($data) || !is_string($data['unit'] ?? null) || !in_array($data['unit'], self::UNITS, true)
            || !is_string($data['time'] ?? null) || strtotime($data['time']) === false) {
            return null;
        }

        return ['unit' => $data['unit'], 'time' => $data['time']];
    }

    /**
     * @param list<string> $logTail the last lines of the run log
     * @return array{subject: string, body: string}
     */
    public static function message(string $unit, string $host, int $now, array $logTail): array
    {
        $what = self::unit($unit) === Units::BACKUP_SERVICE ? 'nightly backup' : 'weekly repository check';
        $host = preg_replace('/[^A-Za-z0-9.-]/', '', $host) ?: 'this server';
        $lines = array_slice($logTail, -self::MAIL_LOG_LINES);
        $body = sprintf("The Relical %s (%s) failed on %s at %s.\n\n", $what, $unit, $host, gmdate('Y-m-d H:i', $now) . ' UTC')
            . "Open Extensions > Relical Backup in Plesk for the status and the last report, or run\n"
            . sprintf("    journalctl -u %s\n\n", $unit)
            . ($lines === [] ? "The run log is empty.\n" : "The last lines of the run log:\n\n" . implode("\n", $lines) . "\n");

        return ['subject' => sprintf('Relical: the %s failed on %s', $what, $host), 'body' => $body];
    }

    /** The administrator's address from ADMIN_REQUEST's answer, or '' when there is none. */
    public static function adminEmail(?SimpleXMLElement $response): string
    {
        if ($response === null) {
            return '';
        }
        $found = $response->xpath('//server/get/result/admin/admin_email');

        return self::validEmail(is_array($found) && $found !== [] ? (string) $found[0] : '');
    }

    /** The address when it is a single plain address, else ''. */
    public static function validEmail(string $email): string
    {
        $email = trim($email);
        if ($email === '' || preg_match('/[\s,;<>]/', $email) === 1 || filter_var($email, FILTER_VALIDATE_EMAIL) === false) {
            return '';
        }

        return $email;
    }
}

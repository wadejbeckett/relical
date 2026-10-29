<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;

/**
 * Renders the core's /etc/relical/config.json (version 2) from the
 * extension's settings.
 *
 * Pure: write-config.php reads pm_Settings and writes the files; this class
 * validates the values and builds the document. The configuration holds no
 * secrets, only the paths of the files that do (the SSH key, the pinned host
 * key, the MySQL option file and the repository keys).
 */
final class ConfigWriter
{
    public const DESTINATION = 'storagebox';

    /**
     * Plesk's Dovecot keeps every folder in the inbox namespace
     * (prefix INBOX., separator .), so a mail restore's new folder is
     * INBOX.Restored <date> <id> (core config key mail_folder_prefix).
     */
    public const MAIL_FOLDER_PREFIX = 'INBOX.';

    private const HOST = '/^[a-z0-9][a-z0-9.-]{0,252}$/D';
    private const USER = '/^[a-z0-9_][a-z0-9_-]{0,63}$/D';
    private const PATH = '/^[A-Za-z0-9_.\/][A-Za-z0-9_.\/-]{0,254}$/D';
    private const TIME = '/^([01][0-9]|2[0-3]):[0-5][0-9]$/D';

    /**
     * @param array<string, mixed> $input  Settings::configInput()
     * @param string               $serverId this server's host name
     * @return array<string, mixed>
     * @throws InvalidArgumentException naming the first bad value
     */
    public static function render(array $input, string $serverId): array
    {
        $host = self::host((string) ($input['sftp_host'] ?? ''), 'Storage Box host');
        $serverId = self::host(strtolower($serverId), 'server name');
        $port = self::port($input['sftp_port'] ?? null);
        $user = self::user((string) ($input['sftp_user'] ?? ''));
        $path = self::path((string) ($input['sftp_path'] ?? ''));
        $retention = self::retention($input['retention'] ?? null);
        self::time((string) ($input['backup_time'] ?? ''));
        $limit = $input['limit_upload_kib'] ?? 0;
        if (!is_int($limit) || $limit < 0) {
            throw new InvalidArgumentException('upload limit must be a whole number of KiB/s, 0 for no limit');
        }

        return [
            'version' => 2,
            'server_id' => $serverId,
            'restic_bin' => Paths::RESTIC_BIN,
            'doveadm_bin' => Paths::DOVEADM_BIN,
            'state_dir' => Paths::STATE_DIR,
            'keys_dir' => Paths::KEYS_DIR,
            'mailbox_pattern' => Paths::MAILBOX_PATTERN,
            'mail_folder_prefix' => self::MAIL_FOLDER_PREFIX,
            'roots' => ['files' => [Paths::FILES_ROOT], 'mail' => [Paths::MAIL_ROOT]],
            'mysql' => ['defaults_file' => Paths::MYSQL_CNF],
            'inventory_command' => Paths::inventoryCommand(),
            'default_destination' => self::DESTINATION,
            'destinations' => [
                self::DESTINATION => [
                    'repository' => sprintf('sftp:%s@%s:%s/{subscription}', $user, $host, $path),
                    'env' => new \stdClass(),
                    'sftp_args' => self::sftpArgs($port),
                    'prune' => 'local',
                ],
            ],
            'retention' => $retention,
            'limit_upload_kib' => $limit,
            'low_priority' => true,
        ];
    }

    /** @param array<string, mixed> $config */
    public static function toJson(array $config): string
    {
        return json_encode($config, JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR) . "\n";
    }

    /**
     * The ssh options restic passes to sftp: key auth with the extension's
     * key, only the host key the administrator confirmed, and timeouts, so
     * an unreachable or stalled Storage Box fails the call instead of
     * hanging the night (core review finding 3; the core's runner adds the
     * same options when they are missing, and ssh keeps the first of a
     * repeated option).
     */
    public static function sftpArgs(int $port): string
    {
        return sprintf(
            '-p %d -i %s -o UserKnownHostsFile=%s -o StrictHostKeyChecking=yes'
            . ' -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=4 -o BatchMode=yes',
            $port,
            Paths::SSH_KEY,
            Paths::KNOWN_HOSTS
        );
    }

    /**
     * The MySQL option file the core's mysqldump reads. The password is
     * Plesk's admin password; it is quoted so `#`, `;` and spaces survive.
     */
    public static function mysqlOptionFile(string $password): string
    {
        if ($password === '' || preg_match('/[\x00-\x1f\x7f]/', $password) === 1) {
            throw new InvalidArgumentException('the Plesk database admin password is empty or has control characters');
        }
        $quoted = '"' . str_replace(['\\', '"'], ['\\\\', '\\"'], $password) . '"';

        return "[client]\nuser=admin\npassword=" . $quoted . "\n";
    }

    // ----- validators, public so the settings screen and Destination use the same rules -----

    public static function host(string $host, string $what = 'Storage Box host'): string
    {
        if (preg_match(self::HOST, $host) !== 1 || str_contains($host, '..')) {
            throw new InvalidArgumentException(sprintf('%s must be a lower-case host name', $what));
        }

        return $host;
    }

    public static function user(string $user): string
    {
        if (preg_match(self::USER, $user) !== 1) {
            throw new InvalidArgumentException('Storage Box user must be lower-case letters, digits, "_" or "-"');
        }

        return $user;
    }

    /** @param mixed $port */
    public static function port($port): int
    {
        if (!is_int($port) || $port < 1 || $port > 65535) {
            throw new InvalidArgumentException('Storage Box port must be between 1 and 65535');
        }

        return $port;
    }

    /** The repository base path on the Storage Box, without a trailing "/". */
    public static function path(string $path): string
    {
        $path = rtrim($path, '/');
        if (preg_match(self::PATH, $path) !== 1) {
            throw new InvalidArgumentException('Storage Box path may hold only letters, digits, "_", ".", "/" and "-"');
        }
        foreach (explode('/', $path) as $part) {
            if ($part === '..') {
                throw new InvalidArgumentException('Storage Box path must not contain ".."');
            }
        }

        return $path;
    }

    /**
     * @param mixed $retention
     * @return array{daily: int, weekly: int, monthly: int}
     */
    public static function retention($retention): array
    {
        $out = [];
        foreach (['daily', 'weekly', 'monthly'] as $key) {
            $value = is_array($retention) ? ($retention[$key] ?? null) : null;
            if (!is_int($value) || $value < 0 || $value > 10000) {
                throw new InvalidArgumentException(sprintf('retention %s must be a whole number from 0', $key));
            }
            $out[$key] = $value;
        }
        if (array_sum($out) === 0) {
            throw new InvalidArgumentException('retention must keep at least one snapshot');
        }

        return $out;
    }

    public static function time(string $time): void
    {
        if (preg_match(self::TIME, $time) !== 1) {
            throw new InvalidArgumentException('backup time must be HH:MM (24-hour)');
        }
    }
}

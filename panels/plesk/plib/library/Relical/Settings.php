<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * Typed getters over the extension's pm_Settings values, with defaults.
 *
 * Only non-secret values live here. The Storage Box key, the pinned host
 * key and the repository keys are files under /etc/relical (see Paths).
 */
final class Settings
{
    public const SCOPE = 'scope';
    public const SFTP_HOST = 'sftp_host';
    public const SFTP_PORT = 'sftp_port';
    public const SFTP_USER = 'sftp_user';
    public const SFTP_PATH = 'sftp_path';
    public const RETENTION_DAILY = 'retention_daily';
    public const RETENTION_WEEKLY = 'retention_weekly';
    public const RETENTION_MONTHLY = 'retention_monthly';
    public const BACKUP_TIME = 'backup_time';
    public const LIMIT_UPLOAD_KIB = 'limit_upload_kib';

    /** @var callable(string, ?string): ?string */
    private $get;

    /**
     * @param callable(string, ?string): ?string $get reads one setting
     */
    public function __construct(callable $get)
    {
        $this->get = $get;
    }

    public static function fromPlesk(): self
    {
        return new self(static fn (string $name, ?string $default): ?string => \pm_Settings::get($name, $default));
    }

    public function scope(): string
    {
        return $this->string(self::SCOPE, Coverage::SCOPE_ALL);
    }

    public function sftpHost(): string
    {
        return $this->string(self::SFTP_HOST, '');
    }

    public function sftpPort(): int
    {
        return $this->int(self::SFTP_PORT, 23);
    }

    public function sftpUser(): string
    {
        return $this->string(self::SFTP_USER, '');
    }

    public function sftpPath(): string
    {
        return $this->string(self::SFTP_PATH, 'relical');
    }

    /** @return array{daily: int, weekly: int, monthly: int} */
    public function retention(): array
    {
        return [
            'daily' => $this->int(self::RETENTION_DAILY, 14),
            'weekly' => $this->int(self::RETENTION_WEEKLY, 8),
            'monthly' => $this->int(self::RETENTION_MONTHLY, 12),
        ];
    }

    /** Local time of the nightly backup, HH:MM (used for the timer). */
    public function backupTime(): string
    {
        return $this->string(self::BACKUP_TIME, '02:30');
    }

    /** Upload limit in KiB/s; 0 means no limit. */
    public function limitUploadKib(): int
    {
        return $this->int(self::LIMIT_UPLOAD_KIB, 0);
    }

    /**
     * The values ConfigWriter renders the core configuration from.
     *
     * @return array<string, mixed>
     */
    public function configInput(): array
    {
        return [
            'sftp_host' => $this->sftpHost(),
            'sftp_port' => $this->sftpPort(),
            'sftp_user' => $this->sftpUser(),
            'sftp_path' => $this->sftpPath(),
            'retention' => $this->retention(),
            'backup_time' => $this->backupTime(),
            'limit_upload_kib' => $this->limitUploadKib(),
        ];
    }

    private function string(string $name, string $default): string
    {
        $value = ($this->get)($name, $default);

        return $value === null ? $default : trim((string) $value);
    }

    /**
     * A stored value that is not a whole number reads as -1, which every
     * validator refuses, instead of silently becoming 0.
     */
    private function int(string $name, int $default): int
    {
        $value = ($this->get)($name, null);
        if ($value === null || trim((string) $value) === '') {
            return $default;
        }
        $value = trim((string) $value);

        return preg_match('/^[0-9]{1,9}$/D', $value) === 1 ? (int) $value : -1;
    }
}

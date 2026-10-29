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

    /** Every setting, in the order the settings form shows them. */
    public const KEYS = [
        self::SCOPE, self::SFTP_HOST, self::SFTP_PORT, self::SFTP_USER, self::SFTP_PATH,
        self::RETENTION_DAILY, self::RETENTION_WEEKLY, self::RETENTION_MONTHLY,
        self::BACKUP_TIME, self::LIMIT_UPLOAD_KIB,
    ];

    /** @var callable(string, ?string): ?string */
    private $get;

    /**
     * @param callable(string, ?string): ?string $get reads one setting
     */
    public function __construct(callable $get)
    {
        $this->get = $get;
    }

    /**
     * Checks the values from the settings form, field by field, with the
     * rules the configuration is rendered with (ConfigWriter).
     *
     * @param array<string, mixed> $values setting name => submitted value
     * @return array<string, string> setting name => what is wrong with it; empty when all is well
     */
    public static function errors(array $values): array
    {
        $settings = self::fromValues($values);
        $input = $settings->configInput();
        $checks = [
            self::SCOPE => static function () use ($settings): void {
                if (!Coverage::isValidScope($settings->scope())) {
                    throw new \InvalidArgumentException('choose which subscriptions are backed up');
                }
            },
            self::SFTP_HOST => static fn () => ConfigWriter::host($input['sftp_host']),
            self::SFTP_PORT => static fn () => ConfigWriter::port($input['sftp_port']),
            self::SFTP_USER => static fn () => ConfigWriter::user($input['sftp_user']),
            self::SFTP_PATH => static fn () => ConfigWriter::path($input['sftp_path']),
            self::RETENTION_DAILY => static fn () => ConfigWriter::retention($input['retention']),
            self::BACKUP_TIME => static fn () => ConfigWriter::time($input['backup_time']),
            self::LIMIT_UPLOAD_KIB => static function () use ($input): void {
                if ($input['limit_upload_kib'] < 0) {
                    throw new \InvalidArgumentException('upload limit must be a whole number of KiB/s, 0 for no limit');
                }
            },
        ];
        $errors = [];
        foreach ($checks as $key => $check) {
            try {
                $check();
            } catch (\InvalidArgumentException $e) {
                $errors[$key] = $e->getMessage();
            }
        }
        if (isset($errors[self::RETENTION_DAILY])) {
            // One message for the three retention fields, on the field it names.
            $message = $errors[self::RETENTION_DAILY];
            unset($errors[self::RETENTION_DAILY]);
            $field = str_contains($message, 'weekly') ? self::RETENTION_WEEKLY : (str_contains($message, 'monthly') ? self::RETENTION_MONTHLY : self::RETENTION_DAILY);
            $errors[$field] = $message;
        }
        if ($errors === []) {
            try {
                ConfigWriter::render($input, 'localhost');
            } catch (\InvalidArgumentException $e) {
                $errors[self::SFTP_HOST] = $e->getMessage();
            }
        }

        return $errors;
    }

    /**
     * The submitted values as they are stored, once errors() found nothing:
     * an empty number takes its default and the host is lower-cased.
     *
     * @param array<string, mixed> $values setting name => submitted value
     * @return array<string, string> setting name => value to store, for every KEYS entry
     * @throws \InvalidArgumentException with the first error when a value is not valid
     */
    public static function validated(array $values): array
    {
        $errors = self::errors($values);
        if ($errors !== []) {
            throw new \InvalidArgumentException((string) reset($errors));
        }
        $settings = self::fromValues($values);
        $input = $settings->configInput();

        return [
            self::SCOPE => $settings->scope(),
            self::SFTP_HOST => $input['sftp_host'],
            self::SFTP_PORT => (string) $input['sftp_port'],
            self::SFTP_USER => $input['sftp_user'],
            self::SFTP_PATH => ConfigWriter::path($input['sftp_path']),
            self::RETENTION_DAILY => (string) $input['retention']['daily'],
            self::RETENTION_WEEKLY => (string) $input['retention']['weekly'],
            self::RETENTION_MONTHLY => (string) $input['retention']['monthly'],
            self::BACKUP_TIME => $input['backup_time'],
            self::LIMIT_UPLOAD_KIB => (string) $input['limit_upload_kib'],
        ];
    }

    /** @param array<string, mixed> $values */
    private static function fromValues(array $values): self
    {
        $raw = [];
        foreach (self::KEYS as $key) {
            $value = $values[$key] ?? '';
            // Anything but a plain value reads as a control character, which every rule refuses.
            $raw[$key] = is_scalar($value) ? trim((string) $value) : "\x01";
        }
        $raw[self::SFTP_HOST] = strtolower($raw[self::SFTP_HOST]);

        return new self(static fn (string $name, ?string $default): ?string => $raw[$name] ?? $default);
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
        return $this->string(self::BACKUP_TIME, '01:00');
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

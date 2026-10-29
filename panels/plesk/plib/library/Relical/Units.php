<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;
use RuntimeException;

/**
 * Renders the extension's systemd units from the templates in
 * plib/resources/systemd/.
 *
 * Pure apart from loadTemplates(). The nightly backup timer runs at the
 * `backup_time` setting (HH:MM, server local time); the weekly check runs on
 * Sunday twelve hours later, so it cannot start while that night's backup
 * still holds the repository lock.
 */
final class Units
{
    public const SYSTEMD_DIR = '/etc/systemd/system';

    public const BACKUP_SERVICE = 'relical-backup.service';
    public const BACKUP_TIMER = 'relical-backup.timer';
    public const CHECK_SERVICE = 'relical-check.service';
    public const CHECK_TIMER = 'relical-check.timer';
    /** Started by both services on failure (OnFailure=); runs notify.php. */
    public const NOTIFY_SERVICE = 'relical-notify@.service';

    public const ALL = [self::BACKUP_SERVICE, self::BACKUP_TIMER, self::CHECK_SERVICE, self::CHECK_TIMER, self::NOTIFY_SERVICE];
    public const SERVICES = [self::BACKUP_SERVICE, self::CHECK_SERVICE];
    public const TIMERS = [self::BACKUP_TIMER, self::CHECK_TIMER];

    public const DEFAULT_ROOT_SCRIPT = '/opt/psa/admin/sbin/modules/relical/relical-root';

    private const TIME = '/^([01][0-9]|2[0-3]):([0-5][0-9])$/D';

    /**
     * @param array<string, string> $templates unit name => template, exactly ALL
     * @return array<string, string> unit name => rendered unit, in ALL's order
     */
    public static function renderAll(array $templates, string $backupTime, string $rootScript = self::DEFAULT_ROOT_SCRIPT): array
    {
        $names = array_keys($templates);
        sort($names);
        $want = self::ALL;
        sort($want);
        if ($names !== $want) {
            throw new InvalidArgumentException('the unit templates must be exactly ' . implode(', ', self::ALL));
        }
        $out = [];
        foreach (self::ALL as $name) {
            $out[$name] = self::render($templates[$name], $backupTime, $rootScript);
        }

        return $out;
    }

    /** @param string $rootScript relical-root as root runs it (Paths::rootScript()) */
    public static function render(string $template, string $backupTime, string $rootScript = self::DEFAULT_ROOT_SCRIPT): string
    {
        if (preg_match('#^/[A-Za-z0-9._/-]+$#D', $rootScript) !== 1 || str_contains($rootScript, '..')) {
            throw new InvalidArgumentException('the root script must be a plain absolute path');
        }
        $unit = strtr($template, [
            '{backup_time}' => self::time($backupTime),
            '{check_time}' => self::checkTime($backupTime),
            '{root_script}' => $rootScript,
        ]);

        if (preg_match('/\{[a-z_]+\}/', $unit, $m) === 1) {
            throw new InvalidArgumentException(sprintf('the unit has an unknown placeholder %s', $m[0]));
        }
        foreach (preg_split('/\R/', $unit) ?: [] as $line) {
            if (preg_match('/^\s*(Exec[A-Za-z]*)\s*=\s*(.*)$/', $line, $m) === 1 && !str_starts_with($m[2], '/')) {
                throw new InvalidArgumentException(sprintf('%s must run an absolute path', $m[1]));
            }
        }

        return $unit;
    }

    /** The weekly check's time: twelve hours after the backup time. */
    public static function checkTime(string $backupTime): string
    {
        [$hour, $minute] = explode(':', self::time($backupTime));

        return sprintf('%02d:%s', ((int) $hour + 12) % 24, $minute);
    }

    /** @return array<string, string> unit name => template */
    public static function loadTemplates(string $dir): array
    {
        $out = [];
        foreach (self::ALL as $name) {
            $content = @file_get_contents($dir . '/' . $name);
            if ($content === false) {
                throw new RuntimeException(sprintf('could not read the unit template %s/%s', $dir, $name));
            }
            $out[$name] = $content;
        }

        return $out;
    }

    private static function time(string $time): string
    {
        if (preg_match(self::TIME, $time) !== 1) {
            throw new InvalidArgumentException('backup time must be HH:MM (24-hour)');
        }

        return $time;
    }
}

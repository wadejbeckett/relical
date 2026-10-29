<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Where customer restores keep their state: one record file per restore,
 * `<dir>/<id>.json` (0600, root), and one lock file per subscription,
 * `<dir>/<subscription>.lock`, which restore-run.php holds (flock) for as
 * long as the restore runs. The directory is the extension's var dir
 * (`pm_Context::getVarDir()`) plus `restores/`, 0700 root, so the web
 * process reads it only through restore-result.php and
 * restore-running.php.
 *
 * The lock is what stops a second restore of the same subscription while
 * one runs, and what tells a record whose process died ("running" with no
 * lock held) from one that is still working.
 */
final class RestoreStore
{
    /** Records older than this are removed when the next restore starts. */
    public const MAX_AGE = 30 * 86400;

    private string $dir;

    public function __construct(string $dir)
    {
        $this->dir = rtrim($dir, '/');
    }

    public static function fromPlesk(): self
    {
        return new self(rtrim((string) \pm_Context::getVarDir(), '/') . '/restores');
    }

    public function ensure(): void
    {
        RootFile::ensureDir($this->dir, 0700);
    }

    public function recordPath(string $id): string
    {
        return $this->dir . '/' . Command::restoreId($id) . '.json';
    }

    public function lockPath(string $subscription): string
    {
        return $this->dir . '/' . Command::domain($subscription) . '.lock';
    }

    /**
     * Takes the subscription's restore lock without waiting.
     *
     * @return resource|null the held lock (keep it open until the restore ends), or null when another restore holds it
     */
    public function lock(string $subscription)
    {
        $path = $this->lockPath($subscription);
        if (is_link($path)) {
            throw new RuntimeException(sprintf('%s is a symbolic link', $path));
        }
        $previousUmask = umask(0077);
        try {
            $handle = @fopen($path, 'c');
        } finally {
            umask($previousUmask);
        }
        if ($handle === false) {
            throw new RuntimeException(sprintf('could not open %s', $path));
        }
        if (!flock($handle, LOCK_EX | LOCK_NB)) {
            fclose($handle);
            return null;
        }

        return $handle;
    }

    /** @param resource $lock from lock() */
    public static function unlock($lock): void
    {
        flock($lock, LOCK_UN);
        fclose($lock);
    }

    /** Whether a restore of the subscription is running now. */
    public function isRunning(string $subscription): bool
    {
        if (!is_dir($this->dir)) {
            return false;
        }
        $lock = $this->lock($subscription);
        if ($lock === null) {
            return true;
        }
        self::unlock($lock);

        return false;
    }

    /** @return ?array<string, mixed> the record, or null when there is none */
    public function read(string $id): ?array
    {
        $path = $this->recordPath($id);
        if (is_link($path) || !is_file($path)) {
            return null;
        }
        $data = json_decode((string) @file_get_contents($path), true);

        return is_array($data) ? $data : null;
    }

    /** @param array<string, mixed> $record with a valid `id` */
    public function write(array $record): void
    {
        RootFile::write(
            $this->recordPath((string) ($record['id'] ?? '')),
            json_encode($record, JSON_UNESCAPED_SLASHES | JSON_PRETTY_PRINT | JSON_INVALID_UTF8_SUBSTITUTE | JSON_THROW_ON_ERROR) . "\n"
        );
    }

    /** Removes records older than MAX_AGE. @return int how many were removed */
    public function prune(int $now): int
    {
        $removed = 0;
        foreach (glob($this->dir . '/*.json') ?: [] as $path) {
            $mtime = @filemtime($path);
            if (!is_link($path) && is_file($path) && $mtime !== false && $now - $mtime > self::MAX_AGE && @unlink($path)) {
                $removed++;
            }
        }

        return $removed;
    }
}

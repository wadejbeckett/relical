<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Where customer restores keep their state, in the extension's var dir
 * (`pm_Context::getVarDir()`) plus `restores/`, 0700 root, so the web
 * process reads it only through root scripts:
 *
 * - `<id>.json` (0600): one record per restore (RestoreRecord).
 * - `<subscription>.lock`: held (flock) by restore-run.php for as long as
 *   the subscription's restore runs; stops a second one.
 * - `slot-<n>.lock`: SLOTS of them, one held by each running restore;
 *   caps the restores running on the whole server.
 * - `<subscription>.read.lock`: held while restore-read.php asks the core
 *   about the subscription's backups; a second request meanwhile is told
 *   to wait instead of opening another Storage Box connection.
 * - `read-slot-<n>.lock`: READ_SLOTS of them, one held by each running
 *   read; caps the customer reads on the whole server.
 * - `cache-<subscription>_<kind>[_<hash>].cache` (0600): the core's last
 *   answer to `snapshots`, `mailboxes` or one `ls` (per restore point and
 *   folder, told apart by a hash of both), reused for CACHE_TTL seconds.
 *   "_" never occurs in a subscription name, so one subscription's files
 *   are never matched for another.
 *
 * Asking whether a lock is held reads /proc/locks (Status::lockHeld) and
 * takes no lock, so asking never makes a restore that is just starting
 * fail as busy.
 */
final class RestoreStore
{
    /** Records older than this are removed when the next restore starts. */
    public const MAX_AGE = 30 * 86400;

    /** Customer restores running at the same time on the whole server. */
    public const SLOTS = 2;

    /** Customer reads (restore-read.php) running at the same time on the whole server. */
    public const READ_SLOTS = 4;

    /** How long an answer of the core about restore points, mailboxes or a folder is reused. */
    public const CACHE_TTL = 300;

    /** The answers that are cached; `ls` needs a key (restore point and folder). */
    public const CACHED = ['snapshots', 'mailboxes', 'ls'];

    private string $dir;

    /** @var callable(): string */
    private $procLocks;

    /** @param ?callable(): string $procLocks reads /proc/locks (replaceable in tests) */
    public function __construct(string $dir, ?callable $procLocks = null)
    {
        $this->dir = rtrim($dir, '/');
        $this->procLocks = $procLocks ?? static fn (): string => (string) @file_get_contents('/proc/locks');
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

    public function readLockPath(string $subscription): string
    {
        return $this->dir . '/' . Command::domain($subscription) . '.read.lock';
    }

    /** @param string $key what else the answer depends on: '' for snapshots and mailboxes, the restore point and folder for ls */
    public function cachePath(string $subscription, string $kind, string $key = ''): string
    {
        if (!in_array($kind, self::CACHED, true)) {
            throw new \InvalidArgumentException(sprintf('"%s" is not cached', $kind));
        }
        if (($kind === 'ls') !== ($key !== '')) {
            throw new \InvalidArgumentException($kind === 'ls' ? 'an ls answer is cached per restore point and folder' : sprintf('"%s" takes no key', $kind));
        }

        return $this->dir . '/cache-' . Command::domain($subscription) . '_' . $kind
            . ($key === '' ? '' : '_' . substr(hash('sha256', $key), 0, 32)) . '.cache';
    }

    // ----- locks -----

    /**
     * Takes the subscription's restore lock without waiting.
     *
     * @return resource|null the held lock (keep it open until the restore ends), or null when another restore holds it
     */
    public function lock(string $subscription)
    {
        return $this->take($this->lockPath($subscription));
    }

    /**
     * Takes one of the server's restore slots without waiting.
     *
     * @return resource|null the held slot, or null when SLOTS restores run already
     */
    public function slot()
    {
        for ($n = 1; $n <= self::SLOTS; $n++) {
            $slot = $this->take($this->dir . '/slot-' . $n . '.lock');
            if ($slot !== null) {
                return $slot;
            }
        }

        return null;
    }

    /**
     * Takes the subscription's read lock without waiting.
     *
     * @return resource|null the held lock, or null while another read of the subscription runs
     */
    public function readLock(string $subscription)
    {
        return $this->take($this->readLockPath($subscription));
    }

    /**
     * Takes one of the server's read slots without waiting.
     *
     * @return resource|null the held slot, or null when READ_SLOTS reads run already
     */
    public function readSlot()
    {
        for ($n = 1; $n <= self::READ_SLOTS; $n++) {
            $slot = $this->take($this->dir . '/read-slot-' . $n . '.lock');
            if ($slot !== null) {
                return $slot;
            }
        }

        return null;
    }

    /** @param resource $lock from lock(), slot(), readLock() or readSlot() */
    public static function unlock($lock): void
    {
        flock($lock, LOCK_UN);
        fclose($lock);
    }

    /** Whether a restore of the subscription is running now. */
    public function isRunning(string $subscription): bool
    {
        return $this->held($this->lockPath($subscription));
    }

    /** Whether every restore slot of the server is taken. */
    public function serverBusy(): bool
    {
        for ($n = 1; $n <= self::SLOTS; $n++) {
            if (!$this->held($this->dir . '/slot-' . $n . '.lock')) {
                return false;
            }
        }

        return true;
    }

    // ----- records -----

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

    /**
     * Every readable record (malformed ones are left out).
     *
     * @return list<array<string, mixed>>
     */
    public function records(): array
    {
        $out = [];
        foreach (glob($this->dir . '/*.json') ?: [] as $path) {
            if (is_link($path) || !is_file($path)) {
                continue;
            }
            $data = json_decode((string) @file_get_contents($path), true);
            if (is_array($data)) {
                $out[] = $data;
            }
        }

        return $out;
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

    // ----- the read cache -----

    /**
     * The core's cached answer, when it is younger than CACHE_TTL and was
     * stored for this subscription.
     */
    public function cached(string $subscription, string $kind, int $now, string $key = ''): ?string
    {
        $path = $this->cachePath($subscription, $kind, $key);
        if (is_link($path) || !is_file($path)) {
            return null;
        }
        $entry = json_decode((string) @file_get_contents($path), true);
        if (!is_array($entry) || ($entry['subscription'] ?? null) !== $subscription || ($entry['kind'] ?? null) !== $kind
            || ($entry['key'] ?? '') !== $key || !is_int($entry['at'] ?? null) || !is_string($entry['stdout'] ?? null)) {
            return null;
        }
        $age = $now - $entry['at'];

        return $age >= 0 && $age < self::CACHE_TTL ? $entry['stdout'] : null;
    }

    public function cache(string $subscription, string $kind, string $stdout, int $now, string $key = ''): void
    {
        RootFile::write(
            $this->cachePath($subscription, $kind, $key),
            json_encode(['subscription' => $subscription, 'kind' => $kind, 'key' => $key, 'at' => $now, 'stdout' => $stdout], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE | JSON_THROW_ON_ERROR) . "\n"
        );
    }

    /** Forgets the cached answers of one subscription, or of all. @return int how many were removed */
    public function clearCache(?string $subscription = null): int
    {
        $paths = $subscription === null
            ? (glob($this->dir . '/*.cache') ?: [])
            : (glob($this->dir . '/cache-' . Command::domain($subscription) . '_*.cache') ?: []);
        $removed = 0;
        foreach ($paths as $path) {
            if ((is_file($path) || is_link($path)) && @unlink($path)) {
                $removed++;
            }
        }

        return $removed;
    }

    // ----- restored copies in the home directory -----

    /**
     * How many `restored-*` entries the subscription's home directory holds
     * (the copies earlier restores left there). Names only; nothing is
     * followed or opened.
     */
    public static function restoredFolders(string $home): int
    {
        if (is_link($home) || !is_dir($home)) {
            return 0;
        }
        $count = 0;
        foreach (@scandir($home) ?: [] as $name) {
            if (str_starts_with($name, 'restored-')) {
                $count++;
            }
        }

        return $count;
    }

    // ----- helpers -----

    /** @return resource|null */
    private function take(string $path)
    {
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

    private function held(string $path): bool
    {
        clearstatcache(true, $path);
        if (is_link($path) || !is_file($path)) {
            return false;
        }
        $stat = @stat($path);
        if ($stat === false) {
            return false;
        }

        return Status::lockHeld(($this->procLocks)(), (int) $stat['dev'], (int) $stat['ino']);
    }
}

<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * One step of an install or uninstall, as planned by Installer and carried
 * out by InstallRunner. Plain data, so a plan can be inspected and tested
 * without touching the system.
 */
final class Action
{
    /** Print a line; nothing changes. */
    public const NOTE = 'note';
    /** Create a directory if missing and set its mode (root-owned). */
    public const ENSURE_DIR = 'ensure_dir';
    /** Copy `from` to `path` atomically, refusing a copy whose sha256 differs. */
    public const INSTALL_BINARY = 'install_binary';
    /** Write `content` to `path` atomically with `mode`. */
    public const WRITE_FILE = 'write_file';
    /** Write config.json (when the destination is set) and mysql.cnf. */
    public const WRITE_CONFIG = 'write_config';
    /** Run `argv` without a shell. */
    public const RUN = 'run';
    /** Delete the file at `path` if it is there. */
    public const REMOVE_FILE = 'remove_file';
    /** Remove the directory at `path`, which must be empty by then. */
    public const REMOVE_DIR = 'remove_dir';
    /** Rename `from` to `path` (same directory, so atomic). */
    public const RENAME = 'rename';

    /**
     * @param array<string, mixed> $params
     * @param bool $optional a failure is reported as a warning and the
     *                       remaining steps still run
     */
    private function __construct(
        public readonly string $type,
        public readonly string $description,
        public readonly array $params = [],
        public readonly bool $optional = false
    ) {
    }

    public static function note(string $text): self
    {
        return new self(self::NOTE, $text);
    }

    public static function ensureDir(string $path, int $mode): self
    {
        return new self(self::ENSURE_DIR, sprintf('directory %s (%04o)', $path, $mode), ['path' => $path, 'mode' => $mode]);
    }

    public static function installBinary(string $from, string $to, string $sha256, string $verb = 'install'): self
    {
        return new self(
            self::INSTALL_BINARY,
            sprintf('%s %s (sha256 %s)', $verb, $to, substr($sha256, 0, 12)),
            ['from' => $from, 'path' => $to, 'sha256' => $sha256, 'mode' => 0755]
        );
    }

    public static function writeFile(string $path, string $content, int $mode): self
    {
        return new self(self::WRITE_FILE, sprintf('write %s (%04o)', $path, $mode), ['path' => $path, 'content' => $content, 'mode' => $mode]);
    }

    /** @param array<string, mixed> $configInput Settings::configInput() */
    public static function writeConfig(array $configInput, string $serverId, bool $withDestination): self
    {
        return new self(
            self::WRITE_CONFIG,
            $withDestination
                ? sprintf('write %s and %s', Paths::CONFIG, Paths::MYSQL_CNF)
                : sprintf('write %s (%s waits until the Storage Box destination is saved in the settings)', Paths::MYSQL_CNF, Paths::CONFIG),
            ['input' => $configInput, 'server_id' => $serverId, 'with_destination' => $withDestination]
        );
    }

    /** @param list<string> $argv */
    public static function run(array $argv, bool $optional = false): self
    {
        $shown = array_map(static fn (string $arg): string => $arg === '' ? "''" : $arg, $argv);

        return new self(self::RUN, 'run ' . implode(' ', $shown), ['argv' => $argv], $optional);
    }

    public static function removeFile(string $path, bool $optional = false): self
    {
        return new self(self::REMOVE_FILE, 'remove ' . $path, ['path' => $path], $optional);
    }

    /**
     * @param string|null $ownFiles a pattern (preg) for the names of files the
     *        installer itself may have left in the directory (staging copies,
     *        temporary files): those are deleted first. With a pattern, a
     *        directory that still holds anything else is kept with a note on
     *        stderr instead of failing.
     */
    public static function removeDir(string $path, bool $optional = false, ?string $ownFiles = null): self
    {
        return new self(self::REMOVE_DIR, 'remove ' . $path, ['path' => $path, 'own_files' => $ownFiles], $optional);
    }

    public static function rename(string $from, string $to): self
    {
        return new self(self::RENAME, sprintf('rename %s to %s', $from, $to), ['from' => $from, 'path' => $to]);
    }

    /** The file or directory this step changes, or null for a note or a command. */
    public function path(): ?string
    {
        if ($this->type === self::WRITE_CONFIG) {
            return Paths::CONFIG;
        }
        $path = $this->params['path'] ?? null;

        return is_string($path) ? $path : null;
    }
}

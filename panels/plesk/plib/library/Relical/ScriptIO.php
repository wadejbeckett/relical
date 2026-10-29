<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * How the administrator screens' root scripts answer: one JSON document on
 * stdout and an exit code. A failure prints `{"error": "..."}` and the same
 * message on stderr; exit 2 means the arguments were refused, exit 1 that
 * the work failed.
 */
final class ScriptIO
{
    /** @param array<string, mixed> $data */
    public static function ok(array $data): never
    {
        echo json_encode($data, JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
        exit(0);
    }

    public static function fail(string $message, int $code = 1): never
    {
        fwrite(STDERR, $message . "\n");
        echo json_encode(['error' => $message], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
        exit($code);
    }

    /**
     * Makes sure the rest of the script runs as root. Plesk runs scripts as
     * its panel user (psaadm): `plesk bin extension --exec`, even when root
     * starts it, and the install hooks. Such a run is handed to
     * sbin/relical-root through Plesk's mod_wrapper, with the same
     * arguments, stdin, stdout and stderr, and this process exits with its
     * exit code. Call it after the script has checked its arguments.
     */
    public static function requireRoot(string $script): void
    {
        if (!function_exists('posix_geteuid') || posix_geteuid() === 0) {
            return;
        }
        $args = array_slice(is_array($_SERVER['argv'] ?? null) ? $_SERVER['argv'] : [], 1);
        exit(self::runAsRoot($script, array_values(array_map('strval', $args))));
    }

    /**
     * Runs a root script through relical-root, sharing this process's
     * stdin, stdout and stderr, and returns its exit code. Fails (exit 1)
     * when relical-root is not installed or cannot start.
     *
     * @param list<string> $args
     */
    public static function runAsRoot(string $script, array $args): int
    {
        $argv = array_merge(Paths::rootCommand(), [$script], $args);
        if (!is_executable($argv[0])) {
            self::fail(sprintf(
                '%s must run as root, and %s is missing: reinstall the extension, or run %s %s as root',
                $script,
                $argv[0],
                Paths::rootScript(),
                $script
            ));
        }
        $process = @proc_open($argv, [0 => STDIN, 1 => STDOUT, 2 => STDERR], $pipes);
        if (!is_resource($process)) {
            self::fail(sprintf('%s must run as root, and %s could not be started', $script, $argv[0]));
        }

        return proc_close($process);
    }
}

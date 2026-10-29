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

    public static function requireRoot(string $script): void
    {
        if (function_exists('posix_geteuid') && posix_geteuid() !== 0) {
            self::fail($script . ' must run as root (plesk bin extension --exec relical ' . $script . ')');
        }
    }
}

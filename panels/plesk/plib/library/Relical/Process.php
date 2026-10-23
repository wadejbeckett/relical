<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Runs a command from an argv array with proc_open's array form, so no
 * shell ever parses the arguments. The child's stdout and stderr go straight
 * to the given streams and its exit code is returned.
 */
final class Process
{
    /**
     * @param list<string> $argv   binary first, as an absolute path
     * @param resource     $stdout
     * @param resource     $stderr
     */
    public static function run(array $argv, $stdout, $stderr): int
    {
        if ($argv === [] || !isset($argv[0]) || !str_starts_with($argv[0], '/')) {
            throw new RuntimeException('the command must start with an absolute path');
        }
        foreach ($argv as $arg) {
            if (!is_string($arg) || str_contains($arg, "\0")) {
                throw new RuntimeException('every argument must be a string without NUL bytes');
            }
        }

        $process = proc_open(
            array_values($argv),
            [0 => ['file', '/dev/null', 'r'], 1 => $stdout, 2 => $stderr],
            $pipes
        );
        if (!is_resource($process)) {
            throw new RuntimeException(sprintf('could not start %s', $argv[0]));
        }

        return proc_close($process);
    }
}

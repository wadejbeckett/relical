<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Runs a command from an argv array with proc_open's array form, so no
 * shell ever parses the arguments. run() passes the child's stdout and
 * stderr straight through; capture() collects them under a time limit.
 */
final class Process
{
    /** capture() keeps at most this much of each stream. */
    public const CAPTURE_LIMIT = 1048576;

    /**
     * @param list<string> $argv   binary first, as an absolute path
     * @param resource     $stdout
     * @param resource     $stderr
     */
    public static function run(array $argv, $stdout, $stderr): int
    {
        self::checkArgv($argv);

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

    /**
     * Runs the command with $stdin as its input and returns what it printed.
     * A command still running after $timeout seconds is killed (SIGTERM,
     * then SIGKILL a second later) and reported with `timed_out` set and
     * code -1. Each stream is cut at CAPTURE_LIMIT bytes.
     *
     * @param list<string> $argv binary first, as an absolute path
     * @return array{code: int, stdout: string, stderr: string, timed_out: bool}
     */
    public static function capture(array $argv, float $timeout, string $stdin = ''): array
    {
        self::checkArgv($argv);

        $process = proc_open(
            array_values($argv),
            [0 => ['pipe', 'r'], 1 => ['pipe', 'w'], 2 => ['pipe', 'w']],
            $pipes
        );
        if (!is_resource($process)) {
            throw new RuntimeException(sprintf('could not start %s', $argv[0]));
        }
        if ($stdin !== '') {
            @fwrite($pipes[0], $stdin);
        }
        fclose($pipes[0]);
        stream_set_blocking($pipes[1], false);
        stream_set_blocking($pipes[2], false);

        $out = ['', ''];
        $open = [1 => $pipes[1], 2 => $pipes[2]];
        $deadline = microtime(true) + $timeout;
        $timedOut = false;
        while ($open !== []) {
            $left = $deadline - microtime(true);
            if ($left <= 0) {
                $timedOut = true;
                break;
            }
            $read = array_values($open);
            $write = $except = null;
            $ready = @stream_select($read, $write, $except, (int) $left, (int) (fmod($left, 1) * 1e6));
            if ($ready === false) {
                break;
            }
            foreach ($open as $n => $pipe) {
                $chunk = fread($pipe, 65536);
                if (is_string($chunk) && $chunk !== '' && strlen($out[$n - 1]) < self::CAPTURE_LIMIT) {
                    $out[$n - 1] .= substr($chunk, 0, self::CAPTURE_LIMIT - strlen($out[$n - 1]));
                }
                if (feof($pipe)) {
                    fclose($pipe);
                    unset($open[$n]);
                }
            }
        }

        if ($timedOut) {
            proc_terminate($process, 15);
            $until = microtime(true) + 1.0;
            while (proc_get_status($process)['running'] && microtime(true) < $until) {
                usleep(20000);
            }
            if (proc_get_status($process)['running']) {
                proc_terminate($process, 9);
            }
        }
        foreach ($open as $pipe) {
            fclose($pipe);
        }
        $code = proc_close($process);

        return ['code' => $timedOut ? -1 : $code, 'stdout' => $out[0], 'stderr' => $out[1], 'timed_out' => $timedOut];
    }

    /** @param list<string> $argv */
    private static function checkArgv(array $argv): void
    {
        if ($argv === [] || !isset($argv[0]) || !is_string($argv[0]) || !str_starts_with($argv[0], '/')) {
            throw new RuntimeException('the command must start with an absolute path');
        }
        foreach ($argv as $arg) {
            if (!is_string($arg) || str_contains($arg, "\0")) {
                throw new RuntimeException('every argument must be a string without NUL bytes');
            }
        }
    }
}

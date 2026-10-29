<?php

declare(strict_types=1);

namespace Noiz\Relical;

use Throwable;

/**
 * How the administrator screens run the root scripts: through
 * `plesk bin extension --exec relical <script> [args]` (Command::rootArgv,
 * argv, no shell), waiting at most $timeout seconds, or detached in the
 * background for a backup run (Command::detached, as Cloudflare DNS Sync
 * starts its syncs). The web process itself never runs relical, restic or
 * ssh.
 */
final class RootScripts
{
    /**
     * Never throws: a script that cannot be started or arguments that are
     * refused come back as a failed result whose stderr says why.
     *
     * @param list<string> $args validated by Command::scriptArgs
     * @return array{code: int, data: ?array<mixed>, stdout: string, stderr: string, timed_out: bool}
     */
    public static function run(string $script, array $args = [], float $timeout = 60.0): array
    {
        try {
            $result = Process::capture(Command::rootArgv($script, $args), $timeout);
        } catch (Throwable $e) {
            return ['code' => -1, 'data' => null, 'stdout' => '', 'stderr' => $e->getMessage(), 'timed_out' => false];
        }
        $data = json_decode(trim($result['stdout']), true);

        return $result + ['data' => is_array($data) ? $data : null];
    }

    /**
     * The message to show for a failed run: the script's `error`, else its
     * last line on stderr, else the exit code.
     *
     * @param array{code: int, data: ?array<mixed>, stdout: string, stderr: string, timed_out: bool} $result
     */
    public static function error(array $result): string
    {
        if ($result['timed_out']) {
            return 'the server did not finish in time';
        }
        if (is_string($result['data']['error'] ?? null) && $result['data']['error'] !== '') {
            return $result['data']['error'];
        }
        $lines = array_values(array_filter(array_map('trim', explode("\n", $result['stderr'])), 'strlen'));
        if ($lines !== []) {
            return (string) end($lines);
        }

        return sprintf('exit %d', $result['code']);
    }

    /** @param list<string> $cliArgs validated by Command::parse */
    public static function startInBackground(array $cliArgs): void
    {
        @exec(Command::detached($cliArgs));
    }
}

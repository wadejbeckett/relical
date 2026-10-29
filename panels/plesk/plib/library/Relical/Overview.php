<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;
use Throwable;

/**
 * The settings page's read-only data in one root call (overview.php): the
 * extension's public key, `relical status` and `relical report`. One
 * `extension --exec` instead of three in a row, each core call cut short
 * after CORE_TIMEOUT so a slow or hung core cannot hold the page.
 */
final class Overview
{
    /** Per core call inside overview.php. */
    public const CORE_TIMEOUT = 6.0;

    /** How long the page waits for overview.php as a whole. */
    public const TIMEOUT = 15.0;

    /** The public key may be at most this long (an ed25519 line is about 100 bytes). */
    private const KEY_READ_LIMIT = 4096;

    /**
     * The public key as one validated `ssh-ed25519` line.
     *
     * @return array{key: string, error: string}
     */
    public static function publicKey(string $path): array
    {
        try {
            if (is_link($path) || !is_file($path)) {
                throw new RuntimeException(sprintf('%s is missing; run the installer again (plesk bin extension --exec relical post-install.php)', $path));
            }
            $content = @file_get_contents($path, false, null, 0, self::KEY_READ_LIMIT);
            if ($content === false) {
                throw new RuntimeException(sprintf('could not read %s', $path));
            }

            return ['key' => HostKey::publicKeyLine($content), 'error' => ''];
        } catch (Throwable $e) {
            return ['key' => '', 'error' => $e->getMessage()];
        }
    }

    /**
     * A core call's outcome as overview.php prints it.
     *
     * @param array{code: int, stdout: string, stderr: string, timed_out: bool} $result
     * @return array{code: int, stdout: string, stderr: string, timed_out: bool}
     */
    public static function call(array $result): array
    {
        return [
            'code' => $result['code'],
            'stdout' => $result['stdout'],
            'stderr' => $result['stderr'],
            'timed_out' => $result['timed_out'],
        ];
    }

    /**
     * Reads overview.php's answer, whatever came back: a part that is
     * missing or of the wrong shape reads as a failed call whose stderr says
     * why ($error: RootScripts::error() of the whole call).
     *
     * @param ?array<mixed> $data
     * @return array{public_key: array{key: string, error: string}, status: array{code: int, stdout: string, stderr: string, timed_out: bool}, report: array{code: int, stdout: string, stderr: string, timed_out: bool}}
     */
    public static function parse(?array $data, string $error): array
    {
        $key = $data['public_key'] ?? null;
        $out = [
            'public_key' => is_array($key) && is_string($key['key'] ?? null) && is_string($key['error'] ?? null)
                ? ['key' => $key['key'], 'error' => $key['error']]
                : ['key' => '', 'error' => $error],
        ];
        foreach (['status', 'report'] as $part) {
            $call = $data[$part] ?? null;
            $out[$part] = is_array($call) && is_int($call['code'] ?? null) && is_string($call['stdout'] ?? null) && is_string($call['stderr'] ?? null)
                ? ['code' => $call['code'], 'stdout' => $call['stdout'], 'stderr' => $call['stderr'], 'timed_out' => ($call['timed_out'] ?? false) === true]
                : ['code' => -1, 'stdout' => '', 'stderr' => $error, 'timed_out' => false];
            if ($out[$part]['timed_out']) {
                $out[$part]['stderr'] = trim($out[$part]['stderr'] . "\nrelical did not answer within " . (int) self::CORE_TIMEOUT . ' seconds');
            }
        }

        return $out;
    }
}

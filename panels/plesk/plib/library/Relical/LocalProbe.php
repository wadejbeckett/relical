<?php

declare(strict_types=1);

namespace Noiz\Relical;

/** Probe over the real filesystem. */
final class LocalProbe implements Probe
{
    public function exists(string $path): bool
    {
        clearstatcache(true, $path);

        return file_exists($path) || is_link($path);
    }

    public function read(string $path): ?string
    {
        if (!is_file($path)) {
            return null;
        }
        $content = @file_get_contents($path);

        return $content === false ? null : $content;
    }

    public function sha256(string $path): ?string
    {
        if (!is_file($path)) {
            return null;
        }
        $hash = @hash_file('sha256', $path);

        return $hash === false ? null : $hash;
    }
}

<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Writes root-only files atomically: a temporary file in the same
 * directory, created 0600 before any content is written, then renamed over
 * the target. A reader never sees a half-written file, and the content is
 * never readable by others, not even for a moment. The data is synced to
 * disk before the rename, so a crash leaves the old file or the new one,
 * never an empty one.
 */
final class RootFile
{
    public static function ensureDir(string $dir, int $mode = 0700): void
    {
        if (is_link($dir)) {
            throw new RuntimeException(sprintf('%s is a symbolic link', $dir));
        }
        if (!is_dir($dir) && !@mkdir($dir, $mode, true) && !is_dir($dir)) {
            throw new RuntimeException(sprintf('could not create %s', $dir));
        }
        if (!@chmod($dir, $mode)) {
            throw new RuntimeException(sprintf('could not set the mode of %s', $dir));
        }
    }

    public static function write(string $path, string $content, int $mode = 0600): void
    {
        $dir = dirname($path);
        if (is_link($path)) {
            throw new RuntimeException(sprintf('%s is a symbolic link', $path));
        }
        $tmp = $dir . '/.' . basename($path) . '.' . bin2hex(random_bytes(6)) . '.tmp';

        $previousUmask = umask(0077);
        try {
            $handle = @fopen($tmp, 'x');
            if ($handle === false) {
                throw new RuntimeException(sprintf('could not create %s', $tmp));
            }
            $ok = @chmod($tmp, $mode)
                && fwrite($handle, $content) === strlen($content)
                && fflush($handle)
                && fsync($handle); // on disk before the rename makes it live
            fclose($handle);
            if (!$ok || !@rename($tmp, $path)) {
                @unlink($tmp);
                throw new RuntimeException(sprintf('could not write %s', $path));
            }
        } finally {
            umask($previousUmask);
        }
    }
}

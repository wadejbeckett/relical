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

    /**
     * Copies a file into place atomically (a temporary copy in the target's
     * directory, renamed over the target), refusing it unless the copy's
     * sha256 is $sha256. A failed or mismatched copy leaves the old target
     * untouched and no temporary file behind, so a binary is either the old
     * one or the new one, never half of either.
     */
    public static function install(string $from, string $to, int $mode, string $sha256): void
    {
        if (is_link($to)) {
            throw new RuntimeException(sprintf('%s is a symbolic link', $to));
        }
        $tmp = dirname($to) . '/.' . basename($to) . '.' . bin2hex(random_bytes(6)) . '.tmp';

        $previousUmask = umask(0077);
        try {
            $in = @fopen($from, 'rb');
            if ($in === false) {
                throw new RuntimeException(sprintf('could not read %s', $from));
            }
            $out = @fopen($tmp, 'x');
            if ($out === false) {
                fclose($in);
                throw new RuntimeException(sprintf('could not create %s', $tmp));
            }
            $ok = stream_copy_to_stream($in, $out) !== false && fflush($out) && fsync($out);
            fclose($in);
            fclose($out);
            if (!$ok) {
                @unlink($tmp);
                throw new RuntimeException(sprintf('could not copy %s to %s', $from, $tmp));
            }
            $actual = hash_file('sha256', $tmp);
            if ($actual === false || !hash_equals(strtolower($sha256), $actual)) {
                @unlink($tmp);
                throw new RuntimeException(sprintf('the copy of %s does not match its checksum', $from));
            }
            if (function_exists('posix_geteuid') && posix_geteuid() === 0 && !(@chown($tmp, 0) && @chgrp($tmp, 0))) {
                @unlink($tmp);
                throw new RuntimeException(sprintf('could not give %s to root', $tmp));
            }
            if (!@chmod($tmp, $mode) || !@rename($tmp, $to)) {
                @unlink($tmp);
                throw new RuntimeException(sprintf('could not install %s', $to));
            }
        } finally {
            umask($previousUmask);
        }
    }
}

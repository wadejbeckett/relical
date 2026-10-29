<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * The last lines of the core's log, for the log screen. Reads only the end
 * of the file, so a large log costs no more than a small one.
 */
final class LogTail
{
    public const LINES = 200;
    private const MAX_BYTES = 262144;

    /**
     * @return list<string>|null the lines, oldest first, or null when there is no log yet
     * @throws RuntimeException when the log exists but cannot be read
     */
    public static function read(string $path, int $lines = self::LINES): ?array
    {
        if (is_link($path)) {
            throw new RuntimeException(sprintf('%s is a symbolic link', $path));
        }
        if (!file_exists($path)) {
            return null;
        }
        if (!is_file($path)) {
            // A FIFO or device would block the read until the script is killed.
            throw new RuntimeException(sprintf('%s is not a regular file', $path));
        }
        $handle = @fopen($path, 'rb');
        if ($handle === false) {
            throw new RuntimeException(sprintf('could not read %s', $path));
        }
        $size = (int) (fstat($handle)['size'] ?? 0);
        $start = max(0, $size - self::MAX_BYTES);
        fseek($handle, $start);
        $content = (string) stream_get_contents($handle);
        fclose($handle);

        return self::lastLines($content, $lines, $start > 0);
    }

    /**
     * @param bool $cut the content starts part-way through the file, so its
     *                  first line may be partial and is dropped
     * @return list<string>
     */
    public static function lastLines(string $content, int $lines = self::LINES, bool $cut = false): array
    {
        $all = preg_split('/\r?\n/', rtrim($content, "\r\n")) ?: [];
        if ($cut && count($all) > 1) {
            array_shift($all);
        }
        if ($all === ['']) {
            return [];
        }
        $out = [];
        foreach (array_slice($all, -$lines) as $line) {
            // Valid UTF-8 without control characters (tabs become spaces).
            $line = str_replace("\t", '    ', $line);
            if (preg_match('//u', $line) !== 1) {
                $line = (string) preg_replace('/[\x80-\xff]/', '?', $line);
            }
            $out[] = (string) preg_replace('/[\x00-\x1f\x7f]/', '?', $line);
        }

        return $out;
    }
}

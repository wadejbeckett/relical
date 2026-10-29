<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\LogTail;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class LogTailTest extends TestCase
{
    public function testNoLogYetIsNull(): void
    {
        self::assertNull(LogTail::read(sys_get_temp_dir() . '/relical-no-log-' . bin2hex(random_bytes(6))));
    }

    public function testASymlinkedLogIsRefused(): void
    {
        $target = (string) tempnam(sys_get_temp_dir(), 'relical-log-target');
        $link = sys_get_temp_dir() . '/relical-log-link-' . bin2hex(random_bytes(6));
        file_put_contents($target, "secret\n");
        symlink($target, $link);
        try {
            $this->expectException(RuntimeException::class);
            $this->expectExceptionMessage('symbolic link');
            LogTail::read($link);
        } finally {
            unlink($link);
            unlink($target);
        }
    }

    public function testAFifoIsRefusedInsteadOfBlocking(): void
    {
        if (!function_exists('posix_mkfifo')) {
            self::markTestSkipped('needs posix_mkfifo');
        }
        $fifo = sys_get_temp_dir() . '/relical-log-fifo-' . bin2hex(random_bytes(6));
        posix_mkfifo($fifo, 0600);
        try {
            $this->expectException(RuntimeException::class);
            $this->expectExceptionMessage('not a regular file');
            LogTail::read($fifo);
        } finally {
            unlink($fifo);
        }
    }

    public function testLastLinesOfALargeLog(): void
    {
        $path = tempnam(sys_get_temp_dir(), 'relical-log');
        $lines = [];
        for ($i = 1; $i <= 20000; $i++) {
            $lines[] = sprintf('2026-10-28T01:00:00Z line %05d', $i);
        }
        file_put_contents($path, implode("\n", $lines) . "\n");

        $tail = LogTail::read($path);
        unlink($path);

        self::assertIsArray($tail);
        self::assertCount(200, $tail);
        self::assertSame('2026-10-28T01:00:00Z line 19801', $tail[0]);
        self::assertSame('2026-10-28T01:00:00Z line 20000', $tail[199]);
    }

    public function testControlCharactersAndBadUtf8AreDefanged(): void
    {
        self::assertSame(['a    b', 'c?[31md', 'caf?'], LogTail::lastLines("a\tb\nc\x1b[31md\ncaf\xe9\n"));
        self::assertSame([], LogTail::lastLines(''));
        self::assertSame(['second'], LogTail::lastLines("partial first\nsecond\n", 200, true));
    }
}

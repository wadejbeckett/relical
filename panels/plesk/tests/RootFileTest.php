<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\RootFile;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class RootFileTest extends TestCase
{
    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/relical-rootfile-' . bin2hex(random_bytes(6));
        mkdir($this->dir, 0700);
    }

    protected function tearDown(): void
    {
        $it = new \RecursiveIteratorIterator(
            new \RecursiveDirectoryIterator($this->dir, \FilesystemIterator::SKIP_DOTS),
            \RecursiveIteratorIterator::CHILD_FIRST
        );
        foreach ($it as $file) {
            $file->isDir() && !$file->isLink() ? rmdir($file->getPathname()) : unlink($file->getPathname());
        }
        rmdir($this->dir);
    }

    private static function mode(string $path): int
    {
        clearstatcache();

        return fileperms($path) & 0777;
    }

    /** @return list<string> leftover temporary files */
    private function leftovers(): array
    {
        return array_values(array_filter(scandir($this->dir), static fn (string $f): bool => str_ends_with($f, '.tmp')));
    }

    public function testWritesWithMode0600EvenUnderAPermissiveUmask(): void
    {
        $old = umask(0);
        try {
            RootFile::write($this->dir . '/config.json', "{}\n");
        } finally {
            umask($old);
        }

        self::assertSame("{}\n", file_get_contents($this->dir . '/config.json'));
        self::assertSame(0600, self::mode($this->dir . '/config.json'));
        self::assertSame([], $this->leftovers());
    }

    public function testReplacingAFileIsAnAtomicRename(): void
    {
        $path = $this->dir . '/config.json';
        file_put_contents($path, 'old');
        chmod($path, 0644);
        $inodeBefore = fileinode($path);

        RootFile::write($path, 'new');

        clearstatcache();
        self::assertSame('new', file_get_contents($path));
        self::assertNotSame($inodeBefore, fileinode($path), 'a new file was renamed into place, not the old one rewritten');
        self::assertSame(0600, self::mode($path), 'the old mode is not inherited');
        self::assertSame([], $this->leftovers());
    }

    public function testRefusesASymlinkTarget(): void
    {
        file_put_contents($this->dir . '/elsewhere', 'keep');
        symlink($this->dir . '/elsewhere', $this->dir . '/config.json');

        try {
            RootFile::write($this->dir . '/config.json', 'new');
            self::fail('wrote through a symlink');
        } catch (RuntimeException $e) {
            self::assertSame('keep', file_get_contents($this->dir . '/elsewhere'));
        }
        self::assertSame([], $this->leftovers());
    }

    public function testLeavesNoPartialFileWhenTheRenameFails(): void
    {
        // A directory in the target's place makes rename() fail after the
        // temporary file has been written.
        mkdir($this->dir . '/config.json');

        try {
            RootFile::write($this->dir . '/config.json', 'new');
            self::fail('rename over a directory succeeded');
        } catch (RuntimeException $e) {
            self::assertStringContainsString('could not write', $e->getMessage());
        }
        self::assertSame([], $this->leftovers());
        self::assertTrue(is_dir($this->dir . '/config.json'));
    }

    public function testFailsCleanlyWhenTheDirectoryIsMissing(): void
    {
        $this->expectException(RuntimeException::class);
        RootFile::write($this->dir . '/missing/config.json', 'x');
    }

    public function testEnsureDirCreatesWithMode0700AndTightensAnExistingOne(): void
    {
        RootFile::ensureDir($this->dir . '/a/b');
        self::assertSame(0700, self::mode($this->dir . '/a/b'));

        mkdir($this->dir . '/open', 0777);
        chmod($this->dir . '/open', 0755);
        RootFile::ensureDir($this->dir . '/open');
        self::assertSame(0700, self::mode($this->dir . '/open'));
    }

    public function testEnsureDirRefusesASymlink(): void
    {
        mkdir($this->dir . '/real', 0755);
        symlink($this->dir . '/real', $this->dir . '/link');

        try {
            RootFile::ensureDir($this->dir . '/link');
            self::fail('accepted a symlink');
        } catch (RuntimeException $e) {
            self::assertSame(0755, self::mode($this->dir . '/real'), 'the link target was not touched');
        }
    }
}

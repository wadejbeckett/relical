<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\Action;
use Noiz\Relical\InstallRunner;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class InstallRunnerTest extends TestCase
{
    private string $dir;
    /** @var resource */
    private $out;
    /** @var resource */
    private $err;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/relical-runner-' . bin2hex(random_bytes(6));
        mkdir($this->dir, 0700);
        $this->out = tmpfile();
        $this->err = tmpfile();
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

    /** @param resource $stream */
    private static function contents($stream): string
    {
        rewind($stream);

        return (string) stream_get_contents($stream);
    }

    public function testCarriesOutEachStepAndPrintsIt(): void
    {
        file_put_contents($this->dir . '/src', 'binary');
        $lib = $this->dir . '/lib';

        (new InstallRunner($this->out, $this->err))->run([
            Action::note('installing'),
            Action::ensureDir($lib, 0755),
            Action::installBinary($this->dir . '/src', $lib . '/relical', hash('sha256', 'binary')),
            Action::writeFile($this->dir . '/unit.timer', "[Timer]\n", 0644),
            Action::run(['/bin/true']),
        ]);

        self::assertSame('binary', file_get_contents($lib . '/relical'));
        self::assertSame(0755, fileperms($lib . '/relical') & 0777);
        self::assertSame(0644, fileperms($this->dir . '/unit.timer') & 0777);
        $printed = self::contents($this->out);
        self::assertStringContainsString("Relical: installing\n", $printed);
        self::assertStringContainsString('Relical: install ' . $lib . '/relical', $printed);
        self::assertStringContainsString("Relical: run /bin/true\n", $printed);
    }

    public function testAFailedStepStopsTheRun(): void
    {
        file_put_contents($this->dir . '/src', 'tampered');

        try {
            (new InstallRunner($this->out, $this->err))->run([
                Action::installBinary($this->dir . '/src', $this->dir . '/relical', hash('sha256', 'binary')),
                Action::writeFile($this->dir . '/after', 'x', 0644),
            ]);
            self::fail('the run went on after a failed step');
        } catch (RuntimeException $e) {
            self::assertStringContainsString('checksum', $e->getMessage());
        }
        self::assertFileDoesNotExist($this->dir . '/relical');
        self::assertFileDoesNotExist($this->dir . '/after');
    }

    public function testACommandThatExitsNonZeroFails(): void
    {
        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('exit 1');
        (new InstallRunner($this->out, $this->err))->run([Action::run(['/bin/false'])]);
    }

    public function testAnOptionalFailureIsAWarningAndTheRunGoesOn(): void
    {
        mkdir($this->dir . '/lib');
        file_put_contents($this->dir . '/lib/leftover', 'x');
        file_put_contents($this->dir . '/unit', 'x');

        $runner = new InstallRunner($this->out, $this->err);
        $runner->run([
            Action::run(['/bin/false'], true),
            Action::removeDir($this->dir . '/lib', true),
            Action::removeFile($this->dir . '/unit', true),
            Action::removeFile($this->dir . '/not-there', true),
        ]);

        self::assertCount(2, $runner->warnings());
        self::assertDirectoryExists($this->dir . '/lib', 'a directory that is not empty is left alone');
        self::assertFileDoesNotExist($this->dir . '/unit');
        self::assertStringContainsString('warning: run /bin/false failed: exit 1', self::contents($this->err));
    }
}

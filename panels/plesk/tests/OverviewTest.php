<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\Overview;
use PHPUnit\Framework\TestCase;

final class OverviewTest extends TestCase
{
    private const KEY = 'AAAAC3NzaC1lZDI1NTE5AAAAIAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8g';

    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/relical-overview-' . bin2hex(random_bytes(4));
        mkdir($this->dir, 0700);
    }

    protected function tearDown(): void
    {
        foreach (glob($this->dir . '/*') ?: [] as $file) {
            unlink($file);
        }
        rmdir($this->dir);
    }

    public function testThePublicKeyIsOneValidatedLine(): void
    {
        file_put_contents($this->dir . '/id_ed25519.pub', 'ssh-ed25519 ' . self::KEY . " relical@host.example.test\n");

        self::assertSame(['key' => 'ssh-ed25519 ' . self::KEY . ' relical@host.example.test', 'error' => ''], Overview::publicKey($this->dir . '/id_ed25519.pub'));
    }

    public function testAMissingSymlinkedOrPrivateKeyIsAnError(): void
    {
        self::assertStringContainsString('is missing', Overview::publicKey($this->dir . '/none.pub')['error']);

        file_put_contents($this->dir . '/real.pub', 'ssh-ed25519 ' . self::KEY);
        symlink($this->dir . '/real.pub', $this->dir . '/link.pub');
        self::assertSame('', Overview::publicKey($this->dir . '/link.pub')['key']);

        file_put_contents($this->dir . '/private.pub', "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----\n");
        $private = Overview::publicKey($this->dir . '/private.pub');
        self::assertSame('', $private['key']);
        self::assertNotSame('', $private['error']);
    }

    public function testParseKeepsEachPart(): void
    {
        $parts = Overview::parse([
            'public_key' => ['key' => 'ssh-ed25519 ' . self::KEY, 'error' => ''],
            'status' => ['code' => 0, 'stdout' => '{}', 'stderr' => '', 'timed_out' => false],
            'report' => ['code' => 2, 'stdout' => '', 'stderr' => 'unknown command report', 'timed_out' => false],
        ], 'unused');

        self::assertSame('ssh-ed25519 ' . self::KEY, $parts['public_key']['key']);
        self::assertSame(['code' => 0, 'stdout' => '{}', 'stderr' => '', 'timed_out' => false], $parts['status']);
        self::assertSame(2, $parts['report']['code']);
    }

    public function testAFailedCallFillsEveryPartWithTheReason(): void
    {
        $parts = Overview::parse(null, 'the server did not finish in time');

        self::assertSame(['key' => '', 'error' => 'the server did not finish in time'], $parts['public_key']);
        self::assertSame(['code' => -1, 'stdout' => '', 'stderr' => 'the server did not finish in time', 'timed_out' => false], $parts['status']);
        self::assertSame(-1, $parts['report']['code']);
    }

    public function testATimedOutCoreCallSaysSo(): void
    {
        $parts = Overview::parse(['status' => ['code' => -1, 'stdout' => '', 'stderr' => '', 'timed_out' => true]], 'x');

        self::assertTrue($parts['status']['timed_out']);
        self::assertStringContainsString('did not answer within 6 seconds', $parts['status']['stderr']);
        self::assertSame('x', $parts['report']['stderr'], 'a missing part');
    }
}

<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\Destination;
use Noiz\Relical\HostKey;
use PHPUnit\Framework\TestCase;

final class HostKeyTest extends TestCase
{
    /** A made-up ed25519 key (bytes 1..32); `ssh-keygen -l` gives FINGERPRINT for it. */
    private const KEY = 'AAAAC3NzaC1lZDI1NTE5AAAAIAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8g';
    private const FINGERPRINT = 'SHA256:mKqU+0K8OhKmA8bBQi9Rz0Q5l7/g160hIP+rJYSTNj4';
    /** Another made-up key (32 bytes of 7). */
    private const OTHER_KEY = 'AAAAC3NzaC1lZDI1NTE5AAAAIAcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcH';
    private const OTHER_FINGERPRINT = 'SHA256:gNSIRW+2Iyiuvsdp/bgjy38bvWHw6wQm3tuoXrl3WjQ';

    private static function destination(int $port = 23): Destination
    {
        return Destination::fromInput([
            'sftp_host' => 'u000000-sub1.your-storagebox.example',
            'sftp_port' => $port,
            'sftp_user' => 'u000000-sub1',
            'sftp_path' => 'relical',
        ]);
    }

    public function testKeyscanLineGivesTheSha256Fingerprint(): void
    {
        $output = "# u000000-sub1.your-storagebox.example:23 SSH-2.0-OpenSSH_9.2\n"
            . '[u000000-sub1.your-storagebox.example]:23 ssh-ed25519 ' . self::KEY . "\n";

        $key = HostKey::fromKeyscan($output, self::destination());

        self::assertSame(self::FINGERPRINT, $key->fingerprint());
        self::assertTrue($key->matches(self::FINGERPRINT));
        self::assertFalse($key->matches(self::OTHER_FINGERPRINT));
        self::assertSame('[u000000-sub1.your-storagebox.example]:23 ssh-ed25519 ' . self::KEY . "\n", $key->knownHostsLine(self::destination()));
    }

    public function testOtherKeyTypesAreIgnored(): void
    {
        $output = "u000000-sub1.your-storagebox.example ssh-rsa AAAAB3NzaC1yc2E=\n"
            . 'u000000-sub1.your-storagebox.example ssh-ed25519 ' . self::OTHER_KEY . "\n";

        self::assertSame(self::OTHER_FINGERPRINT, HostKey::fromKeyscan($output, self::destination(22))->fingerprint());
    }

    /** @return iterable<string, array{string, string}> */
    public static function badScans(): iterable
    {
        yield 'empty' => ['', 'did not offer'];
        yield 'only comments' => ["# host:23 SSH-2.0-OpenSSH\n", 'did not offer'];
        yield 'another host' => ['[other.example.test]:23 ssh-ed25519 ' . self::KEY . "\n", 'answered for [other.example.test]:23'];
        yield 'two keys' => [
            '[u000000-sub1.your-storagebox.example]:23 ssh-ed25519 ' . self::KEY . "\n"
            . '[u000000-sub1.your-storagebox.example]:23 ssh-ed25519 ' . self::OTHER_KEY . "\n",
            'more than one',
        ];
        yield 'not base64' => ['[u000000-sub1.your-storagebox.example]:23 ssh-ed25519 ***' . "\n", 'not an ed25519 public key'];
        yield 'rsa blob labelled ed25519' => ['[u000000-sub1.your-storagebox.example]:23 ssh-ed25519 AAAAB3NzaC1yc2EAAAADAQAB' . "\n", 'not an ed25519 public key'];
    }

    /** @dataProvider badScans */
    public function testRefusesAnythingButOneKeyForTheScannedHost(string $output, string $message): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage($message);
        HostKey::fromKeyscan($output, self::destination());
    }

    public function testFingerprintArgument(): void
    {
        self::assertSame(self::FINGERPRINT, HostKey::fingerprintArg(self::FINGERPRINT));
        foreach (['', 'MD5:aa:bb', 'SHA256:short', self::FINGERPRINT . "\n", '-' . self::FINGERPRINT, 'SHA256:' . str_repeat('A', 42) . ';'] as $bad) {
            try {
                HostKey::fingerprintArg($bad);
                self::fail('accepted ' . json_encode($bad));
            } catch (InvalidArgumentException $e) {
                self::assertStringContainsString('fingerprint', $e->getMessage());
            }
        }
    }

    public function testPublicKeyFileShowsOnlyAnEd25519PublicKey(): void
    {
        self::assertSame('ssh-ed25519 ' . self::KEY . ' relical@host.example.test', HostKey::publicKeyLine('ssh-ed25519 ' . self::KEY . " relical@host.example.test\n"));
        self::assertSame('ssh-ed25519 ' . self::KEY, HostKey::publicKeyLine('ssh-ed25519 ' . self::KEY));

        foreach ([
            '-----BEGIN OPENSSH PRIVATE KEY-----',
            'ssh-ed25519 ' . self::KEY . " a\nssh-ed25519 " . self::KEY,
            'ssh-rsa AAAAB3NzaC1yc2EAAAADAQAB',
            'ssh-ed25519 AAAAB3NzaC1yc2EAAAADAQAB',
            '',
        ] as $bad) {
            try {
                HostKey::publicKeyLine($bad);
                self::fail('accepted ' . json_encode($bad));
            } catch (InvalidArgumentException $e) {
                self::assertStringContainsString('public key', $e->getMessage());
            }
        }
    }
}

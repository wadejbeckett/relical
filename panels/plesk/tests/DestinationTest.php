<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\Destination;
use PHPUnit\Framework\TestCase;

final class DestinationTest extends TestCase
{
    private const INPUT = [
        'sftp_host' => 'u000000-sub1.your-storagebox.example',
        'sftp_port' => 23,
        'sftp_user' => 'u000000-sub1',
        'sftp_path' => 'relical/',
    ];

    public function testFromSettings(): void
    {
        $d = Destination::fromInput(self::INPUT);

        self::assertSame('u000000-sub1.your-storagebox.example', $d->host);
        self::assertSame(23, $d->port);
        self::assertSame('u000000-sub1', $d->user);
        self::assertSame('relical', $d->path);
        self::assertSame('[u000000-sub1.your-storagebox.example]:23', $d->knownHostsName());
        self::assertSame('u000000-sub1@u000000-sub1.your-storagebox.example:relical (port 23)', $d->display());
    }

    public function testPort22UsesTheBareHostInKnownHosts(): void
    {
        self::assertSame('box.example.test', Destination::fromInput(['sftp_host' => 'box.example.test', 'sftp_port' => 22] + self::INPUT)->knownHostsName());
    }

    /** @return iterable<string, array{array<string, mixed>, string}> */
    public static function bad(): iterable
    {
        yield 'no host' => [['sftp_host' => ''] + self::INPUT, 'not set'];
        yield 'option-like host' => [['sftp_host' => '-oProxyCommand=id'] + self::INPUT, 'host'];
        yield 'upper-case host' => [['sftp_host' => 'Box.example.test'] + self::INPUT, 'host'];
        yield 'port 0' => [['sftp_port' => 0] + self::INPUT, 'port'];
        yield 'port as text' => [['sftp_port' => '23'] + self::INPUT, 'port'];
        yield 'user with @' => [['sftp_user' => 'a@b'] + self::INPUT, 'user'];
        yield 'path with ..' => [['sftp_path' => 'relical/../x'] + self::INPUT, '..'];
    }

    /**
     * @dataProvider bad
     * @param array<string, mixed> $input
     */
    public function testRefusesWhatConfigWriterRefuses(array $input, string $message): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage($message);
        Destination::fromInput($input);
    }

    public function testKeyscanAsksForTheEd25519KeyOnTheConfiguredPort(): void
    {
        self::assertSame(
            ['/usr/bin/ssh-keyscan', '-p', '23', '-t', 'ed25519', '-T', '10', 'u000000-sub1.your-storagebox.example'],
            Destination::fromInput(self::INPUT)->keyscanArgv('/usr/bin/ssh-keyscan')
        );
    }

    public function testProbeUsesTheExtensionKeyAndOnlyThePinnedHostKey(): void
    {
        $argv = Destination::fromInput(self::INPUT)->sftpProbeArgv('/usr/bin/sftp');

        self::assertSame([
            '/usr/bin/sftp', '-b', '-', '-P', '23',
            '-i', '/etc/relical/ssh/id_ed25519',
            '-o', 'IdentitiesOnly=yes',
            '-o', 'UserKnownHostsFile=/etc/relical/ssh/known_hosts',
            '-o', 'StrictHostKeyChecking=yes',
            '-o', 'BatchMode=yes',
            '-o', 'ConnectTimeout=10',
            'u000000-sub1@u000000-sub1.your-storagebox.example',
        ], $argv);
        self::assertSame("pwd\n", Destination::probeCommands());
    }
}

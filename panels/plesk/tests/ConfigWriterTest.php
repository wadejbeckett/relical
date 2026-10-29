<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\ConfigWriter;
use PHPUnit\Framework\TestCase;

final class ConfigWriterTest extends TestCase
{
    /** @return array<string, mixed> */
    private static function input(array $override = []): array
    {
        return array_merge([
            'sftp_host' => 'u000000-sub1.your-storagebox.example',
            'sftp_port' => 23,
            'sftp_user' => 'u000000-sub1',
            'sftp_path' => 'relical',
            'retention' => ['daily' => 14, 'weekly' => 8, 'monthly' => 12],
            'backup_time' => '02:30',
            'limit_upload_kib' => 20480,
        ], $override);
    }

    private static function render(array $override = []): array
    {
        return ConfigWriter::render(self::input($override), 'plesk.example.test');
    }

    public function testRendersTheVersion2Configuration(): void
    {
        $config = self::render();

        self::assertSame(2, $config['version']);
        self::assertSame('plesk.example.test', $config['server_id']);
        self::assertSame('/usr/local/lib/relical/restic', $config['restic_bin']);
        self::assertSame('/var/lib/relical', $config['state_dir']);
        self::assertSame('/etc/relical/keys', $config['keys_dir']);
        self::assertSame(['defaults_file' => '/etc/relical/mysql.cnf'], $config['mysql']);
        self::assertSame(
            ['/usr/sbin/plesk', 'bin', 'extension', '--exec', 'relical', 'inventory.php'],
            $config['inventory_command']
        );
        self::assertSame('storagebox', $config['default_destination']);
        self::assertSame(['daily' => 14, 'weekly' => 8, 'monthly' => 12], $config['retention']);
        self::assertSame(20480, $config['limit_upload_kib']);
        self::assertTrue($config['low_priority']);
        self::assertArrayNotHasKey('subscriptions', $config, 'the inventory comes from inventory_command only');

        $destination = $config['destinations']['storagebox'];
        self::assertSame('sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/{subscription}', $destination['repository']);
        self::assertSame('local', $destination['prune']);
    }

    public function testRepositoryHasSubscriptionPlaceholderOnceAndNoDomain(): void
    {
        $json = ConfigWriter::toJson(self::render(['sftp_path' => '/home/relical/']));

        self::assertSame(1, substr_count($json, '{subscription}'));
        self::assertStringNotContainsString('{domain}"', str_replace('/var/qmail/mailnames/{domain}/{mailbox}', '', $json));
        self::assertStringContainsString('"repository": "sftp:u000000-sub1@u000000-sub1.your-storagebox.example:/home/relical/{subscription}"', $json);
        self::assertStringContainsString('"env": {}', $json, 'env encodes as an object, not a list');
    }

    public function testSftpArgsPinTheHostKey(): void
    {
        self::assertSame(
            '-p 2222 -i /etc/relical/ssh/id_ed25519 -o UserKnownHostsFile=/etc/relical/ssh/known_hosts -o StrictHostKeyChecking=yes',
            self::render(['sftp_port' => 2222])['destinations']['storagebox']['sftp_args']
        );
    }

    public function testConfigurationHoldsNoSecrets(): void
    {
        $json = ConfigWriter::toJson(self::render());

        self::assertStringNotContainsStringIgnoringCase('password', $json);
        self::assertStringNotContainsString('.psa.shadow', $json);
    }

    /** @return iterable<string, array{0: array<string, mixed>}> */
    public static function badInputs(): iterable
    {
        yield 'host with upper case' => [['sftp_host' => 'Box.example.test']];
        yield 'host with a space' => [['sftp_host' => 'box.example.test -oProxyCommand=x']];
        yield 'host starting with a dash' => [['sftp_host' => '-oProxyCommand=x']];
        yield 'empty host' => [['sftp_host' => '']];
        yield 'host with @' => [['sftp_host' => 'a@b.example.test']];
        yield 'user with @' => [['sftp_user' => 'u0@x']];
        yield 'user with upper case' => [['sftp_user' => 'U000000']];
        yield 'user starting with a dash' => [['sftp_user' => '-u']];
        yield 'empty user' => [['sftp_user' => '']];
        yield 'path with ..' => [['sftp_path' => 'relical/../etc']];
        yield 'path that is ..' => [['sftp_path' => '..']];
        yield 'path with a space' => [['sftp_path' => 'my backups']];
        yield 'path with a brace' => [['sftp_path' => 'x/{subscription}']];
        yield 'path with a colon' => [['sftp_path' => 'a:b']];
        yield 'empty path' => [['sftp_path' => '']];
        yield 'port 0' => [['sftp_port' => 0]];
        yield 'port 65536' => [['sftp_port' => 65536]];
        yield 'port as a string' => [['sftp_port' => '23']];
        yield 'port -1 (unparseable setting)' => [['sftp_port' => -1]];
        yield 'retention keeps nothing' => [['retention' => ['daily' => 0, 'weekly' => 0, 'monthly' => 0]]];
        yield 'retention negative' => [['retention' => ['daily' => -1, 'weekly' => 8, 'monthly' => 12]]];
        yield 'retention missing' => [['retention' => null]];
        yield 'backup time 24:00' => [['backup_time' => '24:00']];
        yield 'backup time free text' => [['backup_time' => 'tonight']];
        yield 'upload limit negative' => [['limit_upload_kib' => -1]];
    }

    /**
     * @dataProvider badInputs
     * @param array<string, mixed> $override
     */
    public function testRefusesBadValues(array $override): void
    {
        $this->expectException(InvalidArgumentException::class);
        self::render($override);
    }

    public function testPortBoundsAreAccepted(): void
    {
        self::assertStringStartsWith('-p 1 ', self::render(['sftp_port' => 1])['destinations']['storagebox']['sftp_args']);
        self::assertStringStartsWith('-p 65535 ', self::render(['sftp_port' => 65535])['destinations']['storagebox']['sftp_args']);
    }

    public function testRefusesABadServerName(): void
    {
        $this->expectException(InvalidArgumentException::class);
        ConfigWriter::render(self::input(), 'bad host name');
    }

    public function testMysqlOptionFileQuotesThePassword(): void
    {
        self::assertSame(
            "[client]\nuser=admin\npassword=\"a#b;c\\\"d\\\\e f\"\n",
            ConfigWriter::mysqlOptionFile('a#b;c"d\\e f')
        );
    }

    public function testMysqlOptionFileRefusesNewlinesAndEmpty(): void
    {
        foreach (["s3cr3tV4lue\nmore", ''] as $bad) {
            try {
                ConfigWriter::mysqlOptionFile($bad);
                self::fail('accepted ' . json_encode($bad));
            } catch (InvalidArgumentException $e) {
                self::assertStringNotContainsString('s3cr3tV4lue', $e->getMessage(), 'the message never repeats the password');
            }
        }
    }
}

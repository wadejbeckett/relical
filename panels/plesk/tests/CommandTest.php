<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\Command;
use Noiz\Relical\Process;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class CommandTest extends TestCase
{
    private const PREFIX = ['/usr/local/lib/relical/relical', '-config', '/etc/relical/config.json'];

    /** @param list<string> $args */
    private static function argv(array $args): array
    {
        return Command::parse($args)->argv();
    }

    /** @return iterable<string, array{0: list<string>, 1: list<string>}> */
    public static function allowed(): iterable
    {
        yield 'backup one' => [['backup', '--subscription', 'example.test'], ['backup', '--subscription', 'example.test']];
        yield 'backup all' => [['backup', '--all'], ['backup', '--all']];
        yield 'backup unit' => [['backup', '--subscription', 'example.test', '--unit', 'db'], ['backup', '--subscription', 'example.test', '--unit', 'db']];
        yield 'backup all one unit' => [['backup', '--all', '--unit', 'mail'], ['backup', '--all', '--unit', 'mail']];
        yield 'backup panel config' => [['backup', '--subscription', '_server', '--unit', 'panel-config'], ['backup', '--subscription', '_server', '--unit', 'panel-config']];
        yield 'forget all' => [['forget', '--all'], ['forget', '--all']];
        yield 'check subset' => [['check', '--all', '--read-data-subset', '5%'], ['check', '--all', '--read-data-subset', '5%']];
        yield 'snapshots' => [['snapshots', '--subscription', 'example.test', '--unit', 'files'], ['snapshots', '--subscription', 'example.test', '--unit', 'files']];
        yield 'snapshots server' => [['snapshots', '--subscription', '_server'], ['snapshots', '--subscription', '_server']];
        yield 'ls' => [['ls', '--subscription', 'example.test', '--snapshot', 'abcdef12', '--path', 'httpdocs/wp-content'], ['ls', '--subscription', 'example.test', '--snapshot', 'abcdef12', '--path', 'httpdocs/wp-content']];
        yield 'restore mail' => [
            ['restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'shop.example.test', '--mailbox', 'info', '--snapshot', 'latest', '--folder', 'Restored/Inbox'],
            ['restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'shop.example.test', '--mailbox', 'info', '--snapshot', 'latest', '--folder', 'Restored/Inbox'],
        ];
        yield 'restore files' => [['restore', '--subscription', 'example.test', '--unit', 'files', '--path', '/httpdocs/index.php'], ['restore', '--subscription', 'example.test', '--unit', 'files', '--path', '/httpdocs/index.php']];
        yield 'restore db' => [['restore', '--subscription', 'example.test', '--unit', 'db', '--database', 'example_wp'], ['restore', '--subscription', 'example.test', '--unit', 'db', '--database', 'example_wp']];
        yield 'restore panel config' => [['restore', '--subscription', '_server', '--unit', 'panel-config'], ['restore', '--subscription', '_server', '--unit', 'panel-config']];
        yield 'status' => [['status'], ['status']];
        yield 'report' => [['report'], ['report']];
        yield 'inventory' => [['inventory'], ['inventory']];
        yield 'flags in a canonical order' => [
            ['restore', '--database', 'example_wp', '--unit', 'db', '--subscription', 'example.test'],
            ['restore', '--subscription', 'example.test', '--unit', 'db', '--database', 'example_wp'],
        ];
    }

    /**
     * @dataProvider allowed
     * @param list<string> $args
     * @param list<string> $expected
     */
    public function testAllowedCommandsBuildAPlainArgvArray(array $args, array $expected): void
    {
        self::assertSame(array_merge(self::PREFIX, $expected), self::argv($args));
    }

    /** @return iterable<string, array{0: list<string>}> */
    public static function refused(): iterable
    {
        yield 'no command' => [[]];
        yield 'unknown command' => [['init']];
        yield 'version is not exposed' => [['version']];
        yield 'shell in the command' => [['status;id']];
        yield 'neither target' => [['backup']];
        yield 'both targets' => [['backup', '--all', '--subscription', 'example.test']];
        yield 'unknown flag' => [['backup', '--all', '--config', '/tmp/x.json']];
        yield 'single-dash flag' => [['backup', '-all']];
        yield 'flag with =' => [['backup', '--subscription=example.test']];
        yield 'duplicate flag' => [['snapshots', '--subscription', 'example.test', '--subscription', 'other.example.test']];
        yield 'missing value' => [['snapshots', '--subscription']];
        yield 'flag as value' => [['snapshots', '--subscription', '--unit']];
        yield 'stray positional' => [['status', 'extra']];
        yield 'status takes no flags' => [['status', '--all']];
        yield 'unit on forget' => [['forget', '--all', '--unit', 'mail']];
        yield 'subset on backup' => [['backup', '--all', '--read-data-subset', '5%']];
        yield 'snapshots needs subscription' => [['snapshots', '--all']];
        yield 'ls needs snapshot' => [['ls', '--subscription', 'example.test']];
        yield 'restore needs unit' => [['restore', '--subscription', 'example.test']];
        yield 'restore mail needs mailbox' => [['restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'example.test']];
        yield 'restore mail needs domain' => [['restore', '--subscription', 'example.test', '--unit', 'mail', '--mailbox', 'info']];
        yield 'restore db needs database' => [['restore', '--subscription', 'example.test', '--unit', 'db']];
        yield 'restore files with database' => [['restore', '--subscription', 'example.test', '--unit', 'files', '--database', 'x']];
        yield 'restore db with path' => [['restore', '--subscription', 'example.test', '--unit', 'db', '--database', 'x', '--path', 'a']];
        yield 'panel config on a subscription' => [['restore', '--subscription', 'example.test', '--unit', 'panel-config']];
        yield 'mail on _server' => [['restore', '--subscription', '_server', '--unit', 'mail', '--domain', 'example.test', '--mailbox', 'info']];
        yield 'backup files on _server' => [['backup', '--subscription', '_server', '--unit', 'files']];
    }

    /**
     * @dataProvider refused
     * @param list<string> $args
     */
    public function testRefusedCommands(array $args): void
    {
        $this->expectException(InvalidArgumentException::class);
        Command::parse($args);
    }

    /** @return iterable<string, array{0: string, 1: string, 2: bool}> */
    public static function values(): iterable
    {
        // subscription
        yield ['subscription', 'example.test', true];
        yield ['subscription', 'xn--bcher-kva.example.test', true];
        yield ['subscription', '_server', true];
        yield ['subscription', 'Example.test', false];
        yield ['subscription', '-example.test', false];
        yield ['subscription', '../etc', false];
        yield ['subscription', 'a..b', false];
        yield ['subscription', 'example.test/x', false];
        yield ['subscription', 'example.test;id', false];
        yield ['subscription', '', false];
        yield ['subscription', str_repeat('a', 254), false];
        yield ['subscription', '_other', false];
        // domain
        yield ['domain', 'shop.example.test', true];
        yield ['domain', '_server', false];
        // snapshot
        yield ['snapshot', 'latest', true];
        yield ['snapshot', 'abcdef12', true];
        yield ['snapshot', str_repeat('a', 64), true];
        yield ['snapshot', 'abcdef1', false];
        yield ['snapshot', str_repeat('a', 65), false];
        yield ['snapshot', 'ABCDEF12', false];
        yield ['snapshot', 'abcdef12 ', false];
        yield ['snapshot', "abcdef12\n", false];
        // unit
        yield ['unit', 'mail', true];
        yield ['unit', 'panel-config', true];
        yield ['unit', 'all', false];
        // mailbox
        yield ['mailbox', 'info', true];
        yield ['mailbox', 'first.last+tag_1-x', true];
        yield ['mailbox', 'info@example.test', false];
        yield ['mailbox', 'Info', false];
        yield ['mailbox', '../info', false];
        yield ['mailbox', str_repeat('a', 65), false];
        // folder
        yield ['folder', 'Restored 2026-10-23', true];
        yield ['folder', 'Archief/Ontvangen', true];
        yield ['folder', 'Überweisungen', true];
        yield ['folder', "Inbox\r\nX", false];
        yield ['folder', "Inbox\0", false];
        yield ['folder', "tab\there", false];
        yield ['folder', "\xff\xfe", false];
        yield ['folder', '', false];
        // path
        yield ['path', 'httpdocs/wp-content', true];
        yield ['path', '/httpdocs/index.php', true];
        yield ['path', 'httpdocs/my file.txt', true];
        yield ['path', '../etc/shadow', false];
        yield ['path', 'httpdocs/../../etc', false];
        yield ['path', 'httpdocs/..', false];
        yield ['path', "httpdocs\n", false];
        yield ['path', '', false];
        // database
        yield ['database', 'example_wp', true];
        yield ['database', 'wp$db-1', true];
        yield ['database', 'x`; DROP', false];
        yield ['database', 'a.b', false];
        yield ['database', str_repeat('a', 65), false];
        // read-data-subset
        yield ['subset', '5%', true];
        yield ['subset', '100%', true];
        yield ['subset', '0%', false];
        yield ['subset', '101%', false];
        yield ['subset', '5', false];
    }

    /** @dataProvider values */
    public function testValidators(string $validator, string $value, bool $ok): void
    {
        if (!$ok) {
            $this->expectException(InvalidArgumentException::class);
        }
        self::assertSame($value, Command::$validator($value));
    }

    public function testValuesStaySingleArgvElements(): void
    {
        $argv = self::argv(['restore', '--subscription', 'example.test', '--unit', 'files', '--path', 'httpdocs/$(id) `id` ; rm -rf x']);

        self::assertSame('httpdocs/$(id) `id` ; rm -rf x', end($argv));
        self::assertContainsOnly('string', $argv);
        self::assertSame(self::PREFIX, array_slice($argv, 0, 3), 'the binary and the config path are fixed');
    }

    public function testErrorsNeverEchoControlCharacters(): void
    {
        try {
            Command::parse(["bad\ncommand\e[31m"]);
            self::fail('accepted');
        } catch (InvalidArgumentException $e) {
            self::assertDoesNotMatchRegularExpression('/[\x00-\x1f]/', $e->getMessage());
        }
    }

    public function testProcessRunsWithoutAShell(): void
    {
        $out = tmpfile();
        $err = tmpfile();
        $code = Process::run(['/usr/bin/printf', '%s|', '$(echo injected)', 'a;b', '`id`'], $out, $err);

        rewind($out);
        self::assertSame(0, $code);
        self::assertSame('$(echo injected)|a;b|`id`|', stream_get_contents($out));
    }

    public function testProcessReturnsTheChildExitCode(): void
    {
        $out = tmpfile();
        self::assertSame(1, Process::run(['/bin/false'], $out, $out));
    }

    public function testProcessRefusesARelativeBinary(): void
    {
        $this->expectException(RuntimeException::class);
        Process::run(['printf', 'x'], STDOUT, STDERR);
    }
}

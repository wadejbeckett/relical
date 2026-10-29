<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\Command;
use Noiz\Relical\Paths;
use Noiz\Relical\Process;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class CommandTest extends TestCase
{
    private const PREFIX = ['/usr/local/lib/relical/relical', '-config', '/etc/relical/config.json'];

    /** The resolved `extension` utility, quoted as the detached command quotes it. */
    private static function utility(): string
    {
        return implode(' ', array_map('escapeshellarg', Paths::extensionCommand()));
    }

    /**
     * @param list<string> $existing
     * @return callable(string): bool
     */
    private static function executable(array $existing): callable
    {
        return static fn (string $path): bool => in_array($path, $existing, true);
    }

    public function testTheExtensionUtilityPrefersProductRoot(): void
    {
        self::assertSame(['/opt/psa/bin/extension'], Paths::extensionUtility('/opt/psa', self::executable(['/opt/psa/bin/extension', '/usr/sbin/plesk'])));
        self::assertSame(['/usr/local/psa/bin/extension'], Paths::extensionUtility('/usr/local/psa/', self::executable(['/usr/local/psa/bin/extension'])));
    }

    public function testTheExtensionUtilityFallsBackToThePleskWrapper(): void
    {
        self::assertSame(['/usr/sbin/plesk', 'bin', 'extension'], Paths::extensionUtility('/opt/psa', self::executable(['/usr/sbin/plesk', '/opt/psa/bin/plesk'])));
        self::assertSame(['/usr/sbin/plesk', 'bin', 'extension'], Paths::extensionUtility(null, self::executable(['/usr/sbin/plesk', '/opt/psa/bin/extension'])));
        self::assertSame(['/usr/local/psa/bin/extension'], Paths::extensionUtility(null, self::executable(['/usr/local/psa/bin/extension'])));
        self::assertSame(['/opt/psa/bin/extension'], Paths::extensionUtility(null, self::executable([])));
    }

    public function testTheExtensionUtilityIgnoresAnOddProductRoot(): void
    {
        $seen = [];
        $isExecutable = static function (string $path) use (&$seen): bool {
            $seen[] = $path;
            return true;
        };
        foreach (['/opt/psa; id', 'opt/psa', '/opt/$(id)', "/opt/psa
", '/opt/../tmp/x', ''] as $root) {
            $seen = [];
            self::assertSame(['/usr/sbin/plesk', 'bin', 'extension'], Paths::extensionUtility($root, $isExecutable), var_export($root, true));
            self::assertSame(['/usr/sbin/plesk'], $seen, 'the odd root was never even checked');
        }
    }

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
        yield 'mailboxes' => [['mailboxes', '--subscription', 'example.test'], ['mailboxes', '--subscription', 'example.test']];
        yield 'adopt' => [['adopt', '--subscription', 'example.test'], ['adopt', '--subscription', 'example.test']];
        yield 'version' => [['version'], ['version']];
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
        yield 'mailboxes without a subscription' => [['mailboxes']];
        yield 'mailboxes of the server' => [['mailboxes', '--subscription', '_server']];
        yield 'mailboxes with a unit' => [['mailboxes', '--subscription', 'example.test', '--unit', 'mail']];
        yield 'mailboxes of every subscription' => [['mailboxes', '--all']];
        yield 'no command' => [[]];
        yield 'unknown command' => [['init']];
        yield 'version with a flag' => [['version', '--all']];
        yield 'adopt without a subscription' => [['adopt']];
        yield 'adopt everything' => [['adopt', '--all']];
        yield 'adopt the server' => [['adopt', '--subscription', '_server']];
        yield 'adopt one unit' => [['adopt', '--subscription', 'example.test', '--unit', 'mail']];
        yield 'shell in the command' => [['status;id']];
        yield 'neither target' => [['backup']];
        yield 'both targets' => [['backup', '--all', '--subscription', 'example.test']];
        yield 'unknown flag' => [['backup', '--all', '--config', '/tmp/x.json']];
        yield 'single-dash flag' => [['backup', '-all']];
        yield 'flag with =' => [['backup', '--subscription=example.test']];
        yield 'duplicate flag' => [['snapshots', '--subscription', 'example.test', '--subscription', 'other.example.test']];
        yield 'missing value' => [['snapshots', '--subscription']];
        yield 'flag as value' => [['snapshots', '--subscription', '--unit']];
        yield 'dash value for path' => [['ls', '--subscription', 'example.test', '--snapshot', 'latest', '--path', '-osftp.command=id']];
        yield 'dash value for folder' => [['restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'example.test', '--mailbox', 'info', '--folder', '-x']];
        yield 'dash value for subscription' => [['backup', '--subscription', '-x']];
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
        yield ['subscription', 'a.', false];
        yield ['subscription', 'a-', false];
        yield ['subscription', 'localhost', false];
        yield ['subscription', 'example-.test', false];
        yield ['subscription', 'Bücher.example.test', false];
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
        yield ['mailbox', '-x', false];
        yield ['mailbox', '.', false];
        yield ['mailbox', '..', false];
        yield ['mailbox', '.hidden', false];
        // folder
        yield ['folder', 'Restored 2026-10-23', true];
        yield ['folder', 'Archief/Ontvangen', true];
        yield ['folder', 'Überweisungen', true];
        yield ['folder', "Inbox\r\nX", false];
        yield ['folder', "Inbox\0", false];
        yield ['folder', "tab\there", false];
        yield ['folder', "\xff\xfe", false];
        yield ['folder', '', false];
        yield ['folder', '-x', false];
        yield ['folder', '.', false];
        yield ['folder', '..', false];
        yield ['folder', 'a-b', true];
        // path
        yield ['path', 'httpdocs/wp-content', true];
        yield ['path', '/httpdocs/index.php', true];
        yield ['path', 'httpdocs/my file.txt', true];
        yield ['path', '../etc/shadow', false];
        yield ['path', 'httpdocs/../../etc', false];
        yield ['path', 'httpdocs/..', false];
        yield ['path', "httpdocs\n", false];
        yield ['path', '', false];
        yield ['path', '-osftp.command=id', false];
        yield ['path', '.', false];
        yield ['path', '..', false];
        yield ['path', 'httpdocs/./x', false];
        yield ['path', 'httpdocs/-x', true];
        // database
        yield ['database', 'example_wp', true];
        yield ['database', 'wp$db-1', true];
        yield ['database', 'x`; DROP', false];
        yield ['database', 'a.b', false];
        yield ['database', str_repeat('a', 65), false];
        yield ['database', '-x', false];
        yield ['database', '.', false];
        yield ['database', '..', false];
        yield ['database', 'a-b', true];
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

    // ----- root scripts of the administrator screens -----

    private const FINGERPRINT = 'SHA256:mKqU+0K8OhKmA8bBQi9Rz0Q5l7/g160hIP+rJYSTNj4';
    private const RESTORE_ID = '20261029120000-0123456789abcdef01234567';

    /** @return iterable<string, array{string, list<string>}> */
    public static function allowedScripts(): iterable
    {
        yield 'write-config' => ['write-config.php', []];
        yield 'install-timers' => ['install-timers.php', []];
        yield 'show-pubkey' => ['show-pubkey.php', []];
        yield 'probe' => ['probe.php', []];
        yield 'log-tail' => ['log-tail.php', []];
        yield 'running' => ['running.php', []];
        yield 'hostkey scan' => ['hostkey.php', ['scan']];
        yield 'hostkey trust' => ['hostkey.php', ['trust', self::FINGERPRINT]];
        yield 'cli status' => ['cli.php', ['status']];
        yield 'cli backup one' => ['cli.php', ['backup', '--subscription', 'example.test']];
        yield 'cli mailboxes' => ['cli.php', ['mailboxes', '--subscription', 'example.test']];
        yield 'restore-run mail' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'example.test', '--mailbox', 'info', '--snapshot', 'abcdef12']];
        yield 'restore-run files' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', 'abcdef12', '--path', 'httpdocs']];
        yield 'restore-run db' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'db', '--database', 'example_wp']];
        yield 'restore-result' => ['restore-result.php', [self::RESTORE_ID]];
        yield 'restore-running' => ['restore-running.php', ['example.test']];
    }

    /**
     * @dataProvider allowedScripts
     * @param list<string> $args
     */
    public function testRootScriptsRunThroughPleskAsArgv(string $script, array $args): void
    {
        self::assertSame(
            array_merge(Paths::extensionCommand(), ['--exec', 'relical', $script], $args),
            Command::rootArgv($script, $args)
        );
    }

    /** @return iterable<string, array{string, list<string>}> */
    public static function refusedScripts(): iterable
    {
        yield 'unknown script' => ['inventory.php', []];
        yield 'post-install from the screens' => ['post-install.php', []];
        yield 'path in the name' => ['../scripts/cli.php', ['status']];
        yield 'argument to a no-argument script' => ['write-config.php', ['--force']];
        yield 'show-pubkey with a path' => ['show-pubkey.php', ['/etc/relical/ssh/id_ed25519']];
        yield 'log-tail with a count' => ['log-tail.php', ['1000']];
        yield 'hostkey without an action' => ['hostkey.php', []];
        yield 'hostkey unknown action' => ['hostkey.php', ['remove']];
        yield 'hostkey trust without a fingerprint' => ['hostkey.php', ['trust']];
        yield 'hostkey trust a bad fingerprint' => ['hostkey.php', ['trust', 'SHA256:x']];
        yield 'hostkey trust an option' => ['hostkey.php', ['trust', '-oProxyCommand=id']];
        yield 'hostkey scan with a host' => ['hostkey.php', ['scan', 'other.example.test']];
        yield 'hostkey trust twice' => ['hostkey.php', ['trust', self::FINGERPRINT, self::FINGERPRINT]];
        yield 'cli not allowed' => ['cli.php', ['init']];
        yield 'cli probe' => ['cli.php', ['probe']];
        yield 'cli bad value' => ['cli.php', ['backup', '--subscription', '-x']];
        yield 'restore-run without an id' => ['restore-run.php', ['restore', '--subscription', 'example.test', '--unit', 'files']];
        yield 'restore-run with a bad id' => ['restore-run.php', ['../x', 'restore', '--subscription', 'example.test', '--unit', 'files']];
        yield 'restore-run with an upper-case id' => ['restore-run.php', ['20261029120000-ABCDEF0123456789ABCDEF01', 'restore', '--subscription', 'example.test', '--unit', 'files']];
        yield 'restore-run without a command' => ['restore-run.php', [self::RESTORE_ID]];
        yield 'restore-run a backup' => ['restore-run.php', [self::RESTORE_ID, 'backup', '--subscription', 'example.test']];
        yield 'restore-run the panel configuration' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', '_server', '--unit', 'panel-config']];
        yield 'restore-run in _server' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', '_server', '--unit', 'files']];
        yield 'restore-run without a unit' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--domain', 'example.test', '--mailbox', 'info']];
        yield 'restore-run a refused flag' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'files', '--database', 'x']];
        yield 'restore-run an option as a value' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'files', '--path', '-x']];
        yield 'restore-result without an id' => ['restore-result.php', []];
        yield 'restore-result with a path' => ['restore-result.php', ['../../etc/shadow']];
        yield 'restore-result twice' => ['restore-result.php', [self::RESTORE_ID, self::RESTORE_ID]];
        yield 'restore-running of the server' => ['restore-running.php', ['_server']];
        yield 'restore-running without a subscription' => ['restore-running.php', []];
        yield 'restore-running with a path' => ['restore-running.php', ['../x']];
    }

    /**
     * @dataProvider refusedScripts
     * @param list<string> $args
     */
    public function testRootScriptArgumentsAreRefused(string $script, array $args): void
    {
        $this->expectException(InvalidArgumentException::class);
        Command::rootArgv($script, $args);
    }

    public function testBackgroundRunIsFullyEscapedAndDetached(): void
    {
        self::assertSame(
            self::utility() . " '--exec' 'relical' 'cli.php' 'backup' '--subscription' 'example.test' < /dev/null > /dev/null 2>&1 &",
            Command::detached(['backup', '--subscription', 'example.test'])
        );
        self::assertSame(
            self::utility() . " '--exec' 'relical' 'cli.php' 'backup' '--all' < /dev/null > /dev/null 2>&1 &",
            Command::detached(['backup', '--all'])
        );
    }

    public function testBackgroundRestoreIsFullyEscapedAndDetached(): void
    {
        self::assertSame(
            self::utility() . " '--exec' 'relical' 'restore-run.php' '" . self::RESTORE_ID . "' 'restore' '--subscription' 'example.test' '--unit' 'files' '--path' 'my '\\''file'\\''' < /dev/null > /dev/null 2>&1 &",
            Command::detachedScript('restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'files', '--path', "my 'file'"])
        );
    }

    public function testBackgroundRestoreRefusesWhatRestoreRunRefuses(): void
    {
        $this->expectException(InvalidArgumentException::class);
        Command::detachedScript('restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', '_server', '--unit', 'panel-config']);
    }

    public function testBackgroundRunRefusesWhatCliRefuses(): void
    {
        $this->expectException(InvalidArgumentException::class);
        Command::detached(['backup', '--subscription', "example.test'; id; '"]);
    }

    public function testCaptureCollectsOutputWithoutAShell(): void
    {
        $result = Process::capture(['/usr/bin/printf', '%s|', '$(echo injected)', 'a;b'], 5);

        self::assertSame(['code' => 0, 'stdout' => '$(echo injected)|a;b|', 'stderr' => '', 'timed_out' => false], $result);
    }

    public function testCaptureFeedsStdinAndKeepsStderrAndTheExitCode(): void
    {
        self::assertSame("pwd\n", Process::capture(['/bin/cat'], 5, "pwd\n")['stdout']);

        $result = Process::capture(['/bin/sh', '-c', 'echo oops >&2; exit 3'], 5);
        self::assertSame(3, $result['code']);
        self::assertSame("oops\n", $result['stderr']);
    }

    public function testCaptureKillsACommandThatRunsTooLong(): void
    {
        $start = microtime(true);
        $result = Process::capture(['/bin/sleep', '30'], 0.3);

        self::assertTrue($result['timed_out']);
        self::assertSame(-1, $result['code']);
        self::assertLessThan(5, microtime(true) - $start);
    }
}

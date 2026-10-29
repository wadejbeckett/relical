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

    /** The resolved root command, quoted as the detached command quotes it. */
    private static function utility(): string
    {
        return implode(' ', array_map([Command::class, 'shellQuote'], Paths::rootCommand()));
    }

    /**
     * @param list<string> $existing
     * @return callable(string): bool
     */
    private static function existing(array $existing): callable
    {
        return static fn (string $path): bool => in_array($path, $existing, true);
    }

    public function testTheProductRootPrefersProductRootD(): void
    {
        self::assertSame('/opt/psa', Paths::productRootOf('/opt/psa', self::existing(['/opt/psa/admin/sbin/mod_wrapper', '/usr/local/psa/admin/sbin/mod_wrapper'])));
        self::assertSame('/usr/local/psa', Paths::productRootOf('/usr/local/psa/', self::existing(['/usr/local/psa/admin/sbin/mod_wrapper', '/opt/psa/admin/sbin/mod_wrapper'])));
        self::assertSame('/srv/psa', Paths::productRootOf('/srv/psa', self::existing(['/srv/psa/admin/sbin/mod_wrapper'])));
    }

    public function testTheProductRootFallsBackToTheKnownRoots(): void
    {
        self::assertSame('/usr/local/psa', Paths::productRootOf('/srv/psa', self::existing(['/usr/local/psa/admin/sbin/mod_wrapper'])));
        self::assertSame('/usr/local/psa', Paths::productRootOf(null, self::existing(['/usr/local/psa/admin/sbin/mod_wrapper'])));
        self::assertSame('/opt/psa', Paths::productRootOf(null, self::existing([])));
    }

    public function testTheProductRootIgnoresAnOddProductRootD(): void
    {
        $seen = [];
        $exists = static function (string $path) use (&$seen): bool {
            $seen[] = $path;
            return true;
        };
        foreach (['/opt/psa; id', 'opt/psa', '/opt/$(id)', "/opt/psa\n", '/opt/../tmp/x', ''] as $root) {
            $seen = [];
            self::assertSame('/opt/psa', Paths::productRootOf($root, $exists), var_export($root, true));
            self::assertSame(['/opt/psa/admin/sbin/mod_wrapper'], $seen, 'the odd root was never even checked');
        }
    }

    public function testRootScriptsRunThroughThePleskModWrapperLink(): void
    {
        self::assertSame(['/opt/psa/admin/bin/modules/relical/relical-root'], Paths::rootCommand('/opt/psa'));
        self::assertSame('/usr/local/psa/admin/sbin/modules/relical/relical-root', Paths::rootScript('/usr/local/psa'));
        self::assertSame(['/opt/psa/admin/sbin/modules/relical/relical-root', 'inventory.php'], Paths::inventoryCommand('/opt/psa'));
    }

    public function testTheRootEntryPointAllowsExactlyTheRootScripts(): void
    {
        $sbin = (string) file_get_contents(__DIR__ . '/../sbin/relical-root');
        self::assertSame(1, preg_match('/case "\$script" in\n(.*?)\)\n/s', $sbin, $m));
        $allowed = preg_split('/[|\\\\\s]+/', trim($m[1]), -1, PREG_SPLIT_NO_EMPTY);
        $expected = array_merge(Command::SCRIPTS, ['inventory.php', 'post-install.php', 'pre-uninstall.php', 'notify.php']);
        sort($allowed);
        sort($expected);
        self::assertSame($expected, $allowed);
        foreach ($expected as $script) {
            self::assertFileExists(__DIR__ . '/../plib/scripts/' . $script);
        }
        self::assertTrue(is_executable(__DIR__ . '/../sbin/relical-root'), 'sbin/relical-root must be executable');
    }

    public function testTheRootEntryPointStartsPhpWithAnEmptyEnvironment(): void
    {
        $sbin = (string) file_get_contents(__DIR__ . '/../sbin/relical-root');
        $code = array_values(array_filter(
            array_map('trim', explode("\n", $sbin)),
            static fn (string $line): bool => $line !== '' && $line[0] !== '#'
        ));

        self::assertSame(
            'exec env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin LANG=C.UTF-8 "$php" -dauto_prepend_file=sdk.php "$file" "$@"',
            end($code),
            'PHPRC, PHP_INI_SCAN_DIR and the rest of the caller\'s environment must not reach PHP as root'
        );
        self::assertSame(1, preg_match_all('/^\s*exec\b/m', $sbin), 'one exec only');
    }

    /** @return iterable<string, array{string, list<string>, bool}> */
    public static function hookArguments(): iterable
    {
        yield 'inventory' => ['inventory.php', [], true];
        yield 'inventory with an argument' => ['inventory.php', ['--subscription'], false];
        yield 'pre-uninstall' => ['pre-uninstall.php', [], true];
        yield 'pre-uninstall with an argument' => ['pre-uninstall.php', ['x'], false];
        yield 'post-install' => ['post-install.php', [], true];
        yield 'post-install upgrade' => ['post-install.php', ['upgrade'], true];
        yield 'post-install upgrade version' => ['post-install.php', ['upgrade', '0.2.0'], true];
        yield 'post-install upgrade version release' => ['post-install.php', ['upgrade', '0.2.0', '1'], true];
        yield 'post-install upgrade dashed version' => ['post-install.php', ['upgrade', '0.2.0-1'], true];
        yield 'post-install something else' => ['post-install.php', ['install'], false];
        yield 'post-install upgrade an option' => ['post-install.php', ['upgrade', '--x'], false];
        yield 'post-install upgrade too many' => ['post-install.php', ['upgrade', '1', '2', '3'], false];
        yield 'post-install upgrade with a space' => ['post-install.php', ['upgrade', '1 2'], false];
        yield 'not a hook' => ['cli.php', [], false];
    }

    /**
     * @dataProvider hookArguments
     * @param list<string> $args
     */
    public function testHooksRefuseExtraArguments(string $script, array $args, bool $ok): void
    {
        if (!$ok) {
            $this->expectException(InvalidArgumentException::class);
        }
        self::assertSame($args, Command::hookArgs($script, $args));
    }

    public function testRunEverythingNowStartsTheNightlyUnit(): void
    {
        self::assertSame(
            ['mode' => 'systemd', 'argv' => ['/usr/bin/systemctl', 'start', '--no-block', 'relical-backup.service']],
            Command::runAll('/usr/bin/systemctl')
        );
        $detached = Command::runAll(null);
        self::assertSame('detached', $detached['mode']);
        self::assertSame(['/usr/local/lib/relical/relical', '-config', '/etc/relical/config.json', 'backup', '--all'], $detached['argv']);
        self::assertSame(
            "'/usr/local/lib/relical/relical' '-config' '/etc/relical/config.json' 'backup' '--all' < /dev/null > /dev/null 2>&1 &",
            Command::background($detached['argv'])
        );
    }

    public function testTheRootEntryPointRefusesOtherScripts(): void
    {
        foreach (['', 'zz.php', '../scripts/cli.php', 'cli.php/../x', '-x'] as $script) {
            $result = Process::capture(['/bin/sh', __DIR__ . '/../sbin/relical-root', $script], 5);
            self::assertSame(2, $result['code'], $script);
            self::assertStringContainsString('is not allowed', $result['stderr'], $script);
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
        yield 'backup all without forget' => [['backup', '--all', '--no-forget'], ['backup', '--all', '--no-forget']];
        yield 'backup all one unit without forget' => [['backup', '--no-forget', '--unit', 'files', '--all'], ['backup', '--all', '--unit', 'files', '--no-forget']];
        yield 'backup all one unit' => [['backup', '--all', '--unit', 'mail'], ['backup', '--all', '--unit', 'mail']];
        yield 'backup panel config' => [['backup', '--subscription', '_server', '--unit', 'panel-config'], ['backup', '--subscription', '_server', '--unit', 'panel-config']];
        yield 'forget all' => [['forget', '--all'], ['forget', '--all']];
        yield 'check subset' => [['check', '--all', '--read-data-subset', '5%'], ['check', '--all', '--read-data-subset', '5%']];
        yield 'snapshots' => [['snapshots', '--subscription', 'example.test', '--unit', 'files'], ['snapshots', '--subscription', 'example.test', '--unit', 'files']];
        yield 'snapshots server' => [['snapshots', '--subscription', '_server'], ['snapshots', '--subscription', '_server']];
        yield 'ls' => [['ls', '--subscription', 'example.test', '--snapshot', 'abcdef12', '--path', 'httpdocs/wp-content'], ['ls', '--subscription', 'example.test', '--snapshot', 'abcdef12', '--path', 'httpdocs/wp-content']];
        yield 'ls of _server' => [['ls', '--subscription', '_server', '--snapshot', 'abcdef12'], ['ls', '--subscription', '_server', '--snapshot', 'abcdef12']];
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
        yield 'dash value for path' => [['ls', '--subscription', 'example.test', '--snapshot', 'abcdef12', '--path', '-osftp.command=id']];
        yield 'ls of latest' => [['ls', '--subscription', 'example.test', '--snapshot', 'latest']];
        yield 'ls of _server with a path' => [['ls', '--subscription', '_server', '--snapshot', 'abcdef12', '--path', 'httpdocs']];
        yield 'ls of a short id' => [['ls', '--subscription', 'example.test', '--snapshot', 'abcdef1']];
        yield 'ls of an upper-case id' => [['ls', '--subscription', 'example.test', '--snapshot', 'ABCDEF12']];
        yield 'dash value for folder' => [['restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'example.test', '--mailbox', 'info', '--folder', '-x']];
        yield 'dash value for subscription' => [['backup', '--subscription', '-x']];
        yield 'stray positional' => [['status', 'extra']];
        yield 'status takes no flags' => [['status', '--all']];
        yield 'unit on forget' => [['forget', '--all', '--unit', 'mail']];
        yield 'no-forget on one subscription' => [['backup', '--subscription', 'example.test', '--no-forget']];
        yield 'no-forget with a value' => [['backup', '--all', '--no-forget', 'true']];
        yield 'no-forget twice' => [['backup', '--all', '--no-forget', '--no-forget']];
        yield 'no-forget on forget' => [['forget', '--all', '--no-forget']];
        yield 'no-forget on check' => [['check', '--all', '--no-forget']];
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
        yield ['snapshotId', 'abcdef12', true];
        yield ['snapshotId', str_repeat('a', 64), true];
        yield ['snapshotId', 'latest', false];
        yield ['snapshotId', 'abcdef1', false];
        yield ['snapshotId', 'ABCDEF12', false];
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

    public function testProcessRunFeedsMemoryStreamsThroughPipes(): void
    {
        foreach (['php://memory', 'php://temp'] as $kind) {
            $out = fopen($kind, 'w+');
            $err = fopen($kind, 'w+');
            $code = Process::run(['/bin/sh', '-c', 'printf out; printf err >&2; exit 3'], $out, $err);

            rewind($out);
            rewind($err);
            self::assertSame(3, $code, $kind);
            self::assertSame('out', stream_get_contents($out), $kind);
            self::assertSame('err', stream_get_contents($err), $kind);
        }
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
        yield 'overview' => ['overview.php', []];
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
        yield 'restore-run by an administrator' => ['restore-run.php', [self::RESTORE_ID, '--admin', 'restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', 'abcdef12']];
        yield 'restore-result' => ['restore-result.php', [self::RESTORE_ID]];
        yield 'restore-running' => ['restore-running.php', ['example.test']];
        yield 'run-all' => ['run-all.php', []];
        yield 'restore-read snapshots' => ['restore-read.php', ['snapshots', 'example.test']];
        yield 'restore-read mailboxes' => ['restore-read.php', ['mailboxes', 'example.test']];
        yield 'restore-read ls top' => ['restore-read.php', ['ls', 'example.test', 'abcdef12']];
        yield 'restore-read ls path' => ['restore-read.php', ['ls', 'example.test', 'abcdef12', 'httpdocs/fotos/año']];
        yield 'cache-clear all' => ['cache-clear.php', []];
        yield 'cache-clear one' => ['cache-clear.php', ['example.test']];
    }

    /**
     * @dataProvider allowedScripts
     * @param list<string> $args
     */
    public function testRootScriptsRunThroughRelicalRootAsArgv(string $script, array $args): void
    {
        self::assertSame(
            array_merge(Paths::rootCommand(), [$script], $args),
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
        yield 'overview with a path' => ['overview.php', ['/etc/relical/ssh/id_ed25519']];
        yield 'show-pubkey is gone' => ['show-pubkey.php', []];
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
        yield 'restore-run admin without a command' => ['restore-run.php', [self::RESTORE_ID, '--admin']];
        yield 'restore-run admin in the wrong place' => ['restore-run.php', [self::RESTORE_ID, 'restore', '--admin', '--subscription', 'example.test', '--unit', 'files']];
        yield 'restore-run admin twice' => ['restore-run.php', [self::RESTORE_ID, '--admin', '--admin', 'restore', '--subscription', 'example.test', '--unit', 'files']];
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
        yield 'run-all with an argument' => ['run-all.php', ['--all']];
        yield 'restore-running without a subscription' => ['restore-running.php', []];
        yield 'restore-running with a path' => ['restore-running.php', ['../x']];
        yield 'restore-read nothing' => ['restore-read.php', []];
        yield 'restore-read status' => ['restore-read.php', ['status']];
        yield 'restore-read the server' => ['restore-read.php', ['snapshots', '_server']];
        yield 'restore-read snapshots with a unit' => ['restore-read.php', ['snapshots', 'example.test', 'mail']];
        yield 'restore-read ls latest' => ['restore-read.php', ['ls', 'example.test', 'latest']];
        yield 'restore-read ls without a snapshot' => ['restore-read.php', ['ls', 'example.test']];
        yield 'restore-read ls climbing' => ['restore-read.php', ['ls', 'example.test', 'abcdef12', 'httpdocs/../..']];
        yield 'restore-read ls absolute' => ['restore-read.php', ['ls', 'example.test', 'abcdef12', '/etc']];
        yield 'restore-read ls an option' => ['restore-read.php', ['ls', 'example.test', 'abcdef12', '-x']];
        yield 'restore-read ls too much' => ['restore-read.php', ['ls', 'example.test', 'abcdef12', 'a', 'b']];
        yield 'cache-clear the server' => ['cache-clear.php', ['_server']];
        yield 'cache-clear two' => ['cache-clear.php', ['example.test', 'other.example.test']];
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
            self::utility() . " 'cli.php' 'backup' '--subscription' 'example.test' < /dev/null > /dev/null 2>&1 &",
            Command::detached(['backup', '--subscription', 'example.test'])
        );
        self::assertSame(
            self::utility() . " 'cli.php' 'backup' '--all' < /dev/null > /dev/null 2>&1 &",
            Command::detached(['backup', '--all'])
        );
    }

    public function testBackgroundRestoreIsFullyEscapedAndDetached(): void
    {
        self::assertSame(
            self::utility() . " 'restore-run.php' '" . self::RESTORE_ID . "' 'restore' '--subscription' 'example.test' '--unit' 'files' '--path' 'my '\\''file'\\''' < /dev/null > /dev/null 2>&1 &",
            Command::detachedScript('restore-run.php', [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'files', '--path', "my 'file'"])
        );
    }

    public function testBackgroundQuotingKeepsEveryByteUnderTheCLocale(): void
    {
        $previous = setlocale(LC_CTYPE, '0');
        setlocale(LC_CTYPE, 'C');
        try {
            $files = [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', 'abcdef12', '--path', 'fotos/año'];
            $mail = [self::RESTORE_ID, 'restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'example.test', '--mailbox', 'info', '--snapshot', 'abcdef12', '--folder', "Envoyés d'été"];
            foreach ([$files, $mail] as $args) {
                $command = Command::detachedScript('restore-run.php', $args);
                $words = substr($command, 0, -strlen(' < /dev/null > /dev/null 2>&1 &'));
                // What sh hands the program, one NUL-terminated word each.
                $result = Process::capture(['/bin/sh', '-c', 'printf "%s\0" ' . $words], 5);
                self::assertSame(
                    implode("\0", array_merge(Paths::rootCommand(), ['restore-run.php'], $args)) . "\0",
                    $result['stdout']
                );
            }
        } finally {
            setlocale(LC_CTYPE, (string) $previous);
        }
    }

    public function testReadArgumentsForCli(): void
    {
        self::assertSame(['snapshots', '--subscription', 'example.test'], Command::readCliArgs(['snapshots', 'example.test']));
        self::assertSame(['mailboxes', '--subscription', 'example.test'], Command::readCliArgs(['mailboxes', 'example.test']));
        self::assertSame(['ls', '--subscription', 'example.test', '--snapshot', 'abcdef12', '--path', 'httpdocs'], Command::readCliArgs(['ls', 'example.test', 'abcdef12', 'httpdocs']));
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

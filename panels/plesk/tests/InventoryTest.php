<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\Inventory;
use PHPUnit\Framework\TestCase;

final class InventoryTest extends TestCase
{
    /** @param array<string, mixed> $override */
    private static function webspace(array $override = []): array
    {
        if (isset($override['name']) && !array_key_exists('home', $override)) {
            $override['home'] = '/var/www/vhosts/' . $override['name']; // the core wants <root>/<name>
        }
        if (isset($override['name']) && !array_key_exists('databases', $override)) {
            // a database belongs to one subscription only
            $override['databases'] = [['type' => 'mysql', 'name' => 'db_' . substr(md5((string) $override['name']), 0, 8), 'local' => true]];
        }

        return array_merge([
            'id' => 17,
            'name' => 'example.test',
            'sys_user' => 'exampleuser',
            'uid' => 10001,
            'gid' => 1004,
            'home' => '/var/www/vhosts/example.test',
            'mail_domains' => ['shop.example.test', 'example.test'],
            'databases' => [['type' => 'mysql', 'name' => 'example_wp', 'local' => true]],
            'plan_items' => [],
        ], $override);
    }

    public function testBuildsTheContractShape(): void
    {
        $inventory = Inventory::build([self::webspace()], 'all');

        self::assertSame(2, $inventory['version']);
        self::assertSame([[
            'id' => '17',
            'name' => 'example.test',
            'renamed_from' => '',
            'sys_user' => 'exampleuser',
            'uid' => 10001,
            'gid' => 1004,
            'home' => '/var/www/vhosts/example.test',
            'features' => ['mail', 'files', 'db'],
            'mail_domains' => ['example.test', 'shop.example.test'],
            'files' => ['/var/www/vhosts/example.test'],
            'file_excludes' => ['logs/**', 'statistics/logs/**', 'tmp/**', '.cagefs/**', 'restored-*'],
            'databases' => [['type' => 'mysql', 'name' => 'example_wp']],
            'destination' => '',
            'retention' => null,
        ]], $inventory['subscriptions']);
        self::assertSame([], $inventory['skipped']);
    }

    public function testDefaultExcludesMatchTheCoresAndLeaveOutRestoredCopies(): void
    {
        self::assertContains('restored-*', Inventory::DEFAULT_FILE_EXCLUDES);
        $core = dirname(__DIR__, 3) . '/core/internal/inventory/inventory.go';
        if (!is_file($core)) {
            self::markTestSkipped('the core source is not beside the extension');
        }
        self::assertSame(1, preg_match('/^var DefaultFileExcludes = \[\]string\{(.*)\}$/m', (string) file_get_contents($core), $m));
        preg_match_all('/"([^"]*)"/', $m[1], $words);
        self::assertSame($words[1], Inventory::DEFAULT_FILE_EXCLUDES);
    }

    public function testServerBlockRunsAConfigurationOnlyPleskBackup(): void
    {
        $server = Inventory::build([], 'all')['server'];

        self::assertSame(['panel-config'], $server['features']);
        self::assertSame('tar', $server['config_ext']);
        self::assertSame([
            '/usr/sbin/plesk', 'bin', 'pleskbackup', '--server',
            '-exclude-mail', '-exclude-files', '-exclude-databases', '-exclude-logs',
            '-include-server-settings', '-output-file', '{output}',
        ], $server['config_command']);
    }

    public function testPlanItemScopeGatesFeatures(): void
    {
        $inventory = Inventory::build([
            self::webspace(['id' => 1, 'name' => 'mail-only.example.test', 'mail_domains' => [], 'plan_items' => ['relical-mail']]),
            self::webspace(['id' => 2, 'name' => 'web-only.example.test', 'mail_domains' => [], 'plan_items' => ['relical-web']]),
            self::webspace(['id' => 3, 'name' => 'both.example.test', 'mail_domains' => [], 'plan_items' => ['relical-mail', 'relical-web']]),
            self::webspace(['id' => 4, 'name' => 'none.example.test', 'mail_domains' => [], 'plan_items' => ['unrelated']]),
        ], 'plan-items');

        $features = array_column($inventory['subscriptions'], 'features', 'name');
        self::assertSame([
            'mail-only.example.test' => ['mail'],
            'web-only.example.test' => ['files', 'db'],
            'both.example.test' => ['mail', 'files', 'db'],
        ], $features);
        self::assertSame([], $inventory['skipped'], 'an uncovered subscription is not a skip');
    }

    public function testScopeAllIgnoresPlanItems(): void
    {
        $inventory = Inventory::build([self::webspace(['plan_items' => []])], 'all');

        self::assertSame(['mail', 'files', 'db'], $inventory['subscriptions'][0]['features']);
    }

    public function testNonMysqlDatabasesAreDroppedWithASkippedNote(): void
    {
        $inventory = Inventory::build([self::webspace(['databases' => [
            ['type' => 'mysql', 'name' => 'keep_me', 'local' => true],
            ['type' => 'postgresql', 'name' => 'pg_db', 'local' => true],
            ['type' => 'MySQL', 'name' => 'upper_type', 'local' => true],
        ]])], 'all');

        self::assertSame(
            [['type' => 'mysql', 'name' => 'keep_me'], ['type' => 'mysql', 'name' => 'upper_type']],
            $inventory['subscriptions'][0]['databases']
        );
        self::assertSame([[
            'name' => 'example.test',
            'reason' => 'database pg_db: postgresql databases are not supported',
        ]], $inventory['skipped']);
    }

    public function testSkippedEntriesHaveTheCoreShape(): void
    {
        $inventory = Inventory::build([
            self::webspace(['databases' => [['type' => 'postgresql', 'name' => 'pg', 'local' => true]]]),
            self::webspace(['id' => 2, 'name' => 'Bad_Name']),
        ], 'all');

        self::assertNotEmpty($inventory['skipped']);
        foreach ($inventory['skipped'] as $entry) {
            self::assertSame(['name', 'reason'], array_keys($entry));
            self::assertIsString($entry['name']);
            self::assertIsString($entry['reason']);
        }
    }

    public function testDatabaseWithAnUnsafeNameIsSkipped(): void
    {
        $inventory = Inventory::build([self::webspace(['databases' => [['type' => 'mysql', 'name' => 'x`; DROP', 'local' => true]]])], 'all');

        self::assertSame([], $inventory['subscriptions'][0]['databases']);
        self::assertStringStartsWith('database x`; DROP:', $inventory['skipped'][0]['reason']);
    }

    /** @return iterable<string, array{0: string}> */
    public static function refusedDatabaseNames(): iterable
    {
        foreach (['-x', '$x', 'SYS', 'phpMyAdmin2', 'mysql', 'phpmyadmin_old', 'roundcubemail'] as $name) {
            yield $name => [$name];
        }
    }

    /** @dataProvider refusedDatabaseNames */
    public function testDatabaseTheCoreRefusesIsSkippedAndTheRestStays(string $dbName): void
    {
        $inventory = Inventory::build([self::webspace(['databases' => [
            ['type' => 'mysql', 'name' => $dbName, 'local' => true],
            ['type' => 'mysql', 'name' => 'example_wp', 'local' => true],
        ]])], 'all');

        self::assertSame([['type' => 'mysql', 'name' => 'example_wp']], $inventory['subscriptions'][0]['databases']);
        self::assertSame(['mail', 'files', 'db'], $inventory['subscriptions'][0]['features']);
        self::assertCount(1, $inventory['skipped']);
        self::assertStringStartsWith('database ' . $dbName . ':', $inventory['skipped'][0]['reason']);
    }

    public function testADatabaseListedByTwoSubscriptionsStaysWithTheFirst(): void
    {
        $inventory = Inventory::build([
            self::webspace(['databases' => [['type' => 'mysql', 'name' => 'shared', 'local' => true]]]),
            self::webspace(['id' => 2, 'name' => 'b.example.test', 'home' => '/var/www/vhosts/b.example.test', 'mail_domains' => [],
                'databases' => [['type' => 'mysql', 'name' => 'shared', 'local' => true], ['type' => 'mysql', 'name' => 'own', 'local' => true]]]),
        ], 'all');

        $byName = array_column($inventory['subscriptions'], 'databases', 'name');
        self::assertSame([['type' => 'mysql', 'name' => 'shared']], $byName['example.test']);
        self::assertSame([['type' => 'mysql', 'name' => 'own']], $byName['b.example.test']);
        self::assertSame([[
            'name' => 'b.example.test',
            'reason' => 'database shared: the database is already listed by example.test',
        ]], $inventory['skipped']);
    }

    public function testDatabasesOnARemoteServerAreSkipped(): void
    {
        $inventory = Inventory::build([self::webspace(['databases' => [
            ['type' => 'mysql', 'name' => 'here', 'local' => true],
            ['type' => 'mysql', 'name' => 'elsewhere', 'local' => false],
            ['type' => 'mysql', 'name' => 'unknown'],
        ]])], 'all');

        self::assertSame([['type' => 'mysql', 'name' => 'here']], $inventory['subscriptions'][0]['databases']);
        self::assertSame(
            ['database elsewhere: it is on a remote database server', 'database unknown: it is on a remote database server'],
            array_column($inventory['skipped'], 'reason')
        );
    }

    public function testNamesArePassedThroughUnchanged(): void
    {
        $inventory = Inventory::build([
            self::webspace(['name' => 'xn--bcher-kva.example.test', 'mail_domains' => ['xn--bcher-kva.example.test']]),
        ], 'all');

        self::assertSame('xn--bcher-kva.example.test', $inventory['subscriptions'][0]['name']);
        self::assertSame(['xn--bcher-kva.example.test'], $inventory['subscriptions'][0]['mail_domains']);
    }

    public function testServerRepositoryNameIsNeverASubscription(): void
    {
        $inventory = Inventory::build([self::webspace(['name' => '_server']), self::webspace(['id' => 2])], 'all');

        self::assertSame(['example.test'], array_column($inventory['subscriptions'], 'name'));
        self::assertSame('_server', $inventory['skipped'][0]['name']);
    }

    public function testMailOnlySubscriptionIsEmittedWithoutAWebspace(): void
    {
        $inventory = Inventory::build([
            self::webspace(['sys_user' => '', 'uid' => null, 'gid' => null, 'home' => '/var/www/vhosts/example.test', 'databases' => []]),
        ], 'all');

        self::assertSame([], $inventory['skipped'], 'no hosting is normal for mail-only, not a problem');
        $subscription = $inventory['subscriptions'][0];
        self::assertSame(['mail'], $subscription['features']);
        self::assertSame('', $subscription['sys_user']);
        self::assertSame(0, $subscription['uid']);
        self::assertSame(0, $subscription['gid']);
        self::assertSame('', $subscription['home']);
        self::assertSame([], $subscription['files']);
        self::assertSame([], $subscription['databases']);
        self::assertSame(['example.test', 'shop.example.test'], $subscription['mail_domains']);
    }

    public function testWebOnlyCoverageWithoutHostingHasNothingToBackUp(): void
    {
        $inventory = Inventory::build([
            self::webspace(['sys_user' => '', 'uid' => null, 'gid' => null, 'databases' => [], 'plan_items' => ['relical-web']]),
        ], 'plan-items');

        self::assertSame([], $inventory['subscriptions']);
        self::assertStringStartsWith('nothing to back up:', $inventory['skipped'][0]['reason']);
    }

    /** @return iterable<string, array{0: array<string, mixed>}> */
    public static function brokenWebspaces(): iterable
    {
        yield 'invalid system user' => [['sys_user' => 'Bad User']];
        yield 'system user with a leading dot' => [['sys_user' => '.john']];
        yield 'uid 0' => [['uid' => 0, 'gid' => 0]];
        yield 'unknown uid' => [['uid' => null]];
        yield 'home outside the files root' => [['home' => '/home/example.test']];
        yield 'home is the files root' => [['home' => '/var/www/vhosts']];
        yield 'home with ..' => [['home' => '/var/www/vhosts/../../etc']];
        yield 'home with a trailing slash' => [['home' => '/var/www/vhosts/example.test/']];
        yield 'home of another webspace' => [['home' => '/var/www/vhosts/other.test']];
        yield 'home below the webspace' => [['home' => '/var/www/vhosts/example.test/httpdocs']];
    }

    /**
     * @dataProvider brokenWebspaces
     * @param array<string, mixed> $override
     */
    public function testBrokenWebspaceKeepsMailOnlyAndTheOthersBackUp(array $override): void
    {
        $inventory = Inventory::build([
            self::webspace($override),
            self::webspace(['id' => 2, 'name' => 'fine.example.test', 'home' => '/var/www/vhosts/fine.example.test', 'mail_domains' => ['fine.example.test']]),
        ], 'all');

        $byName = array_column($inventory['subscriptions'], null, 'name');
        self::assertSame(['mail'], $byName['example.test']['features']);
        self::assertSame('', $byName['example.test']['sys_user']);
        self::assertSame(0, $byName['example.test']['uid']);
        self::assertSame([], $byName['example.test']['files']);
        self::assertSame(['mail', 'files', 'db'], $byName['fine.example.test']['features']);
        self::assertSame('example.test', $inventory['skipped'][0]['name']);
        self::assertStringStartsWith('website files and databases:', $inventory['skipped'][0]['reason']);
    }

    public function testASystemUserWithADotKeepsFilesAndDatabases(): void
    {
        $inventory = Inventory::build([self::webspace(['sys_user' => 'john.smith'])], 'all');

        self::assertSame([], $inventory['skipped']);
        self::assertSame('john.smith', $inventory['subscriptions'][0]['sys_user']);
        self::assertSame(['mail', 'files', 'db'], $inventory['subscriptions'][0]['features']);
        self::assertSame(['/var/www/vhosts/example.test'], $inventory['subscriptions'][0]['files']);
        self::assertSame([['type' => 'mysql', 'name' => 'example_wp']], $inventory['subscriptions'][0]['databases']);
    }

    public function testFilesRootsCanBeGiven(): void
    {
        $inventory = Inventory::build([self::webspace(['home' => '/srv/vhosts/example.test'])], 'all', ['/srv/vhosts']);

        self::assertSame(['/srv/vhosts/example.test'], $inventory['subscriptions'][0]['files']);
    }

    /** @return iterable<string, array{0: string}> */
    public static function invalidNames(): iterable
    {
        yield 'unicode' => ['bücher.example.test'];
        yield 'upper case' => ['Example.test'];
        yield 'no dot' => ['localhost'];
        yield 'leading dash' => ['-example.test'];
        yield 'trailing dash' => ['example-.test'];
        yield 'trailing dot' => ['example.test.'];
        yield 'empty' => [''];
    }

    /** @dataProvider invalidNames */
    public function testInvalidSubscriptionNameIsSkippedAndTheOthersStay(string $name): void
    {
        $inventory = Inventory::build([
            self::webspace(['name' => $name]),
            self::webspace(['id' => 2, 'name' => 'fine.example.test', 'mail_domains' => []]),
        ], 'all');

        self::assertSame(['fine.example.test'], array_column($inventory['subscriptions'], 'name'));
        self::assertSame($name, $inventory['skipped'][0]['name']);
    }

    public function testInvalidMailDomainIsDroppedAndTheSubscriptionStays(): void
    {
        $inventory = Inventory::build([self::webspace(['mail_domains' => ['example.test', 'bücher.example.test', 'Shop.example.test']])], 'all');

        self::assertSame(['example.test'], $inventory['subscriptions'][0]['mail_domains']);
        self::assertSame(['bücher.example.test', 'Shop.example.test'], array_column($inventory['skipped'], 'name'));
    }

    public function testDuplicateNamesIdsAndMailDomainsAreSkipped(): void
    {
        $inventory = Inventory::build([
            self::webspace(),
            self::webspace(['id' => 2]),                                                  // same name
            self::webspace(['name' => 'other.example.test', 'mail_domains' => []]),      // same id
            self::webspace(['id' => 3, 'name' => 'third.example.test', 'mail_domains' => ['third.example.test', 'shop.example.test']]),
            self::webspace(['id' => 'bad id!', 'name' => 'fourth.example.test']),
        ], 'all');

        self::assertSame(['example.test', 'third.example.test'], array_column($inventory['subscriptions'], 'name'));
        self::assertSame(['third.example.test'], $inventory['subscriptions'][1]['mail_domains']);
        self::assertSame(
            ['example.test', 'other.example.test', 'fourth.example.test', 'shop.example.test'],
            array_column($inventory['skipped'], 'name')
        );
    }

    /** @return iterable<string, array{0: list<string>}> */
    public static function bothOrders(): iterable
    {
        yield 'lister first' => [['c.example.test', 'd.example.test']];
        yield 'lister last' => [['d.example.test', 'c.example.test']];
    }

    /**
     * The core refuses both subscriptions when one lists the other's name
     * as a mail domain; the mail domain is dropped and both stay.
     *
     * @dataProvider bothOrders
     * @param list<string> $order
     */
    public function testAMailDomainThatIsAnotherSubscriptionsNameIsDropped(array $order): void
    {
        $webspaces = [
            'c.example.test' => self::webspace(['id' => 3, 'name' => 'c.example.test', 'mail_domains' => ['c.example.test', 'd.example.test']]),
            'd.example.test' => self::webspace(['id' => 4, 'name' => 'd.example.test', 'mail_domains' => ['d.example.test']]),
        ];
        $inventory = Inventory::build([$webspaces[$order[0]], $webspaces[$order[1]]], 'all');

        $mail = array_column($inventory['subscriptions'], 'mail_domains', 'name');
        ksort($mail);
        self::assertSame(['c.example.test' => ['c.example.test'], 'd.example.test' => ['d.example.test']], $mail, 'both stay, each with its own mail only');
        self::assertCount(2, $inventory['subscriptions']);
        self::assertSame(
            [['name' => 'd.example.test', 'reason' => 'mail of c.example.test: the domain is the name of another subscription']],
            $inventory['skipped']
        );
    }

    public function testAMailDomainNamingASubscriptionThatIsNotEmittedStays(): void
    {
        $inventory = Inventory::build([
            self::webspace(['id' => 3, 'name' => 'c.example.test', 'mail_domains' => ['c.example.test', 'd.example.test']]),
            self::webspace(['id' => 'bad id!', 'name' => 'd.example.test', 'mail_domains' => []]),
        ], 'all');

        self::assertSame(['c.example.test', 'd.example.test'], $inventory['subscriptions'][0]['mail_domains']);
    }

    public function testMailDomainsAreDeduplicatedWithTheWebspaceFirst(): void
    {
        $inventory = Inventory::build([self::webspace(['mail_domains' => ['b.example.test', 'example.test', 'b.example.test']])], 'all');

        self::assertSame(['example.test', 'b.example.test'], $inventory['subscriptions'][0]['mail_domains']);
    }

    public function testRenameIsCarriedAcrossRuns(): void
    {
        [$first, $known] = Inventory::trackRenames([], [self::webspace(['name' => 'old.example.test'])]);
        self::assertSame('', $first[0]['renamed_from']);

        [$second, $known] = Inventory::trackRenames($known, [self::webspace(['name' => 'new.example.test'])]);
        self::assertSame('old.example.test', $second[0]['renamed_from']);

        [$third, $known] = Inventory::trackRenames($known, [self::webspace(['name' => 'new.example.test'])]);
        self::assertSame('old.example.test', $third[0]['renamed_from'], 'the hint stays after the rename');

        [$fourth] = Inventory::trackRenames($known, [self::webspace(['name' => 'old.example.test'])]);
        self::assertSame('new.example.test', $fourth[0]['renamed_from']);

        self::assertSame('old.example.test', Inventory::build($third, 'all')['subscriptions'][0]['renamed_from']);
    }

    public function testRenameHintIsClearedWhenANewWebspaceTakesTheOldName(): void
    {
        [, $known] = Inventory::trackRenames([], [self::webspace(['id' => 1, 'name' => 'a.example.test'])]);
        [, $known] = Inventory::trackRenames($known, [self::webspace(['id' => 1, 'name' => 'b.example.test'])]);

        [$now, $known] = Inventory::trackRenames($known, [
            self::webspace(['id' => 1, 'name' => 'b.example.test']),
            self::webspace(['id' => 2, 'name' => 'a.example.test']),
        ]);
        self::assertSame(['', ''], array_column($now, 'renamed_from'));
        self::assertSame('', $known['1']['renamed_from'], 'the cleared hint is not stored again');

        $inventory = Inventory::build($now, 'all');
        self::assertSame(['', ''], array_column($inventory['subscriptions'], 'renamed_from'));
    }

    public function testSwappedNamesGiveNoRenameHints(): void
    {
        [, $known] = Inventory::trackRenames([], [
            self::webspace(['id' => 1, 'name' => 'a.example.test']),
            self::webspace(['id' => 2, 'name' => 'b.example.test']),
        ]);
        [$swapped] = Inventory::trackRenames($known, [
            self::webspace(['id' => 1, 'name' => 'b.example.test']),
            self::webspace(['id' => 2, 'name' => 'a.example.test']),
        ]);

        self::assertSame(['', ''], array_column($swapped, 'renamed_from'));
    }

    public function testBuildDropsAHintThatTheCoreWouldRefuse(): void
    {
        $inventory = Inventory::build([
            self::webspace(['renamed_from' => 'b.example.test', 'mail_domains' => []]),
            self::webspace(['id' => 2, 'name' => 'b.example.test', 'mail_domains' => []]),
            self::webspace(['id' => 3, 'name' => 'c.example.test', 'renamed_from' => 'Not A Domain', 'mail_domains' => []]),
            self::webspace(['id' => 4, 'name' => 'd.example.test', 'renamed_from' => 'd.example.test', 'mail_domains' => []]),
            self::webspace(['id' => 5, 'name' => 'e.example.test', 'renamed_from' => 'gone.example.test', 'mail_domains' => []]),
        ], 'all');

        self::assertSame(['', '', '', '', 'gone.example.test'], array_column($inventory['subscriptions'], 'renamed_from'));
    }

    /** @return iterable<string, array{0: mixed}> */
    public static function corruptHints(): iterable
    {
        yield 'null' => [null];
        yield 'a string' => ['garbage'];
        yield 'a list' => [['x', 'y']];
        yield 'entry is a string' => [['17' => 'old.example.test']];
        yield 'entry fields of the wrong type' => [['17' => ['name' => ['x'], 'renamed_from' => 5]]];
    }

    /**
     * @dataProvider corruptHints
     * @param mixed $known
     */
    public function testCorruptHintsNeverBreakTheRun($known): void
    {
        [$webspaces, $next] = Inventory::trackRenames($known, [self::webspace()]);

        self::assertSame('', $webspaces[0]['renamed_from']);
        self::assertSame(['17' => ['name' => 'example.test', 'renamed_from' => '']], $next);
    }

    public function testInventoryIsValidJson(): void
    {
        $json = json_encode(Inventory::build([self::webspace()], 'all'), JSON_THROW_ON_ERROR);

        self::assertStringContainsString('"retention":null', $json);
    }
}

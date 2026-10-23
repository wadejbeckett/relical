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
        return array_merge([
            'id' => 17,
            'name' => 'example.test',
            'sys_user' => 'exampleuser',
            'uid' => 10001,
            'gid' => 1004,
            'home' => '/var/www/vhosts/example.test',
            'mail_domains' => ['shop.example.test', 'example.test'],
            'databases' => [['type' => 'mysql', 'name' => 'example_wp']],
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
            'file_excludes' => ['logs/**', 'statistics/logs/**', 'tmp/**', '.cagefs/**'],
            'databases' => [['type' => 'mysql', 'name' => 'example_wp']],
            'destination' => '',
            'retention' => null,
        ]], $inventory['subscriptions']);
        self::assertSame([], $inventory['skipped']);
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
            self::webspace(['id' => 1, 'name' => 'mail-only.example.test', 'plan_items' => ['relical-mail']]),
            self::webspace(['id' => 2, 'name' => 'web-only.example.test', 'plan_items' => ['relical-web']]),
            self::webspace(['id' => 3, 'name' => 'both.example.test', 'plan_items' => ['relical-mail', 'relical-web']]),
            self::webspace(['id' => 4, 'name' => 'none.example.test', 'plan_items' => ['unrelated']]),
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
            ['type' => 'mysql', 'name' => 'keep_me'],
            ['type' => 'postgresql', 'name' => 'pg_db'],
            ['type' => 'MySQL', 'name' => 'upper_type'],
        ]])], 'all');

        self::assertSame(
            [['type' => 'mysql', 'name' => 'keep_me'], ['type' => 'mysql', 'name' => 'upper_type']],
            $inventory['subscriptions'][0]['databases']
        );
        self::assertSame([[
            'subscription' => 'example.test',
            'item' => 'database pg_db',
            'reason' => 'postgresql databases are not supported',
        ]], $inventory['skipped']);
    }

    public function testDatabaseWithAnUnsafeNameIsSkipped(): void
    {
        $inventory = Inventory::build([self::webspace(['databases' => [['type' => 'mysql', 'name' => 'x`; DROP']]])], 'all');

        self::assertSame([], $inventory['subscriptions'][0]['databases']);
        self::assertSame('database x`; DROP', $inventory['skipped'][0]['item']);
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
        self::assertSame('_server', $inventory['skipped'][0]['subscription']);
    }

    public function testWebspaceWithoutASystemUserIsSkipped(): void
    {
        $inventory = Inventory::build([
            self::webspace(['sys_user' => '', 'uid' => null, 'gid' => null]),
            self::webspace(['id' => 2, 'name' => 'rootish.example.test', 'uid' => 0, 'gid' => 0]),
        ], 'all');

        self::assertSame([], $inventory['subscriptions']);
        self::assertCount(2, $inventory['skipped']);
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

    public function testInventoryIsValidJson(): void
    {
        $json = json_encode(Inventory::build([self::webspace()], 'all'), JSON_THROW_ON_ERROR);

        self::assertStringContainsString('"retention":null', $json);
    }
}

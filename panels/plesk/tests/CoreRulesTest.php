<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\CoreRules;
use PHPUnit\Framework\TestCase;

/**
 * Runs the shared accept/refuse cases in fixtures/core-rules.json, which the
 * core's inventory tests are meant to read as well.
 */
final class CoreRulesTest extends TestCase
{
    /** @return array<string, mixed> */
    private static function fixture(): array
    {
        return json_decode((string) file_get_contents(__DIR__ . '/fixtures/core-rules.json'), true, 512, JSON_THROW_ON_ERROR);
    }

    /** @return iterable<string, array{0: string, 1: string, 2: bool}> */
    public static function cases(): iterable
    {
        $fixture = self::fixture();
        foreach (['subscription_names', 'sys_users', 'databases'] as $kind) {
            foreach (['accept' => true, 'refuse' => false] as $key => $want) {
                foreach ($fixture[$kind][$key] as $value) {
                    yield sprintf('%s %s %s', $kind, $key, json_encode($value)) => [$kind, $value, $want];
                }
            }
        }
    }

    /** @dataProvider cases */
    public function testSharedCases(string $kind, string $value, bool $want): void
    {
        $got = match ($kind) {
            'subscription_names' => CoreRules::validDomain($value),
            'sys_users' => CoreRules::validSysUser($value),
            'databases' => CoreRules::validDatabase($value),
        };

        self::assertSame($want, $got);
    }

    public function testSharedHomes(): void
    {
        $homes = self::fixture()['homes'];
        foreach (['accept' => true, 'refuse' => false] as $key => $want) {
            foreach ($homes[$key] as $case) {
                $got = CoreRules::cleanAbs($case['home']) && CoreRules::webspaceOf($case['home'], $case['name'], $homes['files_roots']);
                self::assertSame($want, $got, $case['home']);
            }
        }
    }

    public function testRulesMatchTheCoreSource(): void
    {
        $core = dirname(__DIR__, 3) . '/core/internal/inventory/inventory.go';
        if (!is_file($core)) {
            self::markTestSkipped('the core source is not beside the extension');
        }
        $source = (string) file_get_contents($core);

        self::assertSame(1, preg_match('/dbNameRE\s*=\s*regexp\.MustCompile\(`([^`]*)`\)/', $source, $m));
        self::assertSame('/' . $m[1] . '/D', CoreRules::DB_NAME);

        self::assertSame(1, preg_match('/var reservedDatabases = map\[string\]bool\{(.*?)\n\}/s', $source, $m));
        preg_match_all('/"([^"]*)"/', $m[1], $words);
        self::assertEqualsCanonicalizing($words[1], CoreRules::RESERVED_DB);
        self::assertStringContainsString('strings.HasPrefix(n, "' . CoreRules::RESERVED_DB_PREFIX . '")', $source);
    }

    public function testSysUserIsAtMostAsLenientAsTheCore(): void
    {
        $core = dirname(__DIR__, 3) . '/core/internal/inventory/inventory.go';
        if (!is_file($core)) {
            self::markTestSkipped('the core source is not beside the extension');
        }
        self::assertSame(1, preg_match('/sysUserRE\s*=\s*regexp\.MustCompile\(`([^`]*)`\)/', (string) file_get_contents($core), $m));
        $coreRe = '/' . $m[1] . '/D';
        // Everything the extension accepts, the core must accept too. Until
        // the core's "." change is merged, "john.smith" is the one expected
        // difference.
        foreach (self::fixture()['sys_users']['accept'] as $user) {
            if (str_contains($user, '.') && preg_match($coreRe, $user) !== 1) {
                continue;
            }
            self::assertSame(1, preg_match($coreRe, $user), $user);
        }
    }
}

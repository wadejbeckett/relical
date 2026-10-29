<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\PleskSource;
use PHPUnit\Framework\TestCase;
use RuntimeException;
use SimpleXMLElement;

final class PleskSourceTest extends TestCase
{
    public function testASubdomainWithoutMailServiceIsNotAProblem(): void
    {
        // What Plesk 18.0.81 answers for a subdomain (Docker Plesk test).
        self::assertTrue(PleskSource::hasNoMailService('There is no DSMail service for domain with id = 2'));
        self::assertFalse(PleskSource::hasNoMailService('Mail service is temporarily unavailable'));
        self::assertFalse(PleskSource::hasNoMailService(''));
    }

    /** A pm_Domain stand-in whose getPlanItems() records that it was called. */
    private static function domain(int $id, string $name, int &$planCalls): object
    {
        return new class ($id, $name, $planCalls) {
            public function __construct(private int $id, private string $name, private int &$planCalls)
            {
            }

            public function getId(): int { return $this->id; }
            public function getName(): string { return $this->name; }
            public function getHomePath(): string { return '/var/www/vhosts/' . $this->name; }
            public function hasHosting(): bool { return false; }
            public function getSysUserLogin(): string { return ''; }

            /** @return list<string> */
            public function getPlanItems(): array
            {
                $this->planCalls++;

                return ['relical-mail'];
            }
        };
    }

    /** @return array{0: PleskSource, 1: callable(): int} */
    private static function source(): array
    {
        $planCalls = 0;
        $domains = [self::domain(1, 'a.example.test', $planCalls), self::domain(2, 'b.example.test', $planCalls)];
        $source = new PleskSource(
            static fn (): array => $domains,
            static fn (string $request): SimpleXMLElement => new SimpleXMLElement('<packet/>')
        );

        return [$source, static function () use (&$planCalls): int { return $planCalls; }];
    }

    public function testScopeAllNeverReadsPlanItems(): void
    {
        [$source, $calls] = self::source();

        $webspaces = $source->webspaces('all');

        self::assertSame(0, $calls());
        self::assertSame(['a.example.test', 'b.example.test'], array_column($webspaces, 'name'));
        self::assertSame([[], []], array_column($webspaces, 'plan_items'));
    }

    public function testScopePlanItemsReadsThem(): void
    {
        [$source, $calls] = self::source();

        $webspaces = $source->webspaces('plan-items');

        self::assertSame(2, $calls());
        self::assertSame([['relical-mail'], ['relical-mail']], array_column($webspaces, 'plan_items'));
    }

    public function testARequestLevelErrorFailsTheInventory(): void
    {
        $planCalls = 0;
        $source = new PleskSource(
            static fn (): array => [self::domain(1, 'a.example.test', $planCalls)],
            static fn (string $request): SimpleXMLElement => new SimpleXMLElement('<packet><system><status>error</status><errtext>boom</errtext></system></packet>')
        );

        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('Plesk API-RPC error: boom');
        $source->webspaces('all');
    }
}

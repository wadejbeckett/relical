<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\RestoreAccess;
use PHPUnit\Framework\TestCase;

final class RestoreAccessTest extends TestCase
{
    /** Domain 10 is the subscription example.test; 11 is its subdomain; 20 belongs to another customer. */
    private const DOMAINS = [
        10 => ['id' => 10, 'name' => 'example.test', 'plan_items' => ['relical-web']],
        11 => ['id' => 10, 'name' => 'example.test', 'plan_items' => ['relical-web']],
        20 => ['id' => 20, 'name' => 'other.example.test', 'plan_items' => ['relical-mail', 'relical-web']],
        30 => ['id' => 30, 'name' => 'bare.example.test', 'plan_items' => []],
        40 => ['id' => 40, 'name' => '_server', 'plan_items' => ['relical-web']],
        41 => ['id' => 41, 'name' => 'Not A Domain', 'plan_items' => ['relical-web']],
    ];

    /** @var list<int> */
    private array $lookedUp = [];

    /** @param list<int> $access */
    private function decide(bool $admin, array $access, int $domainId, string $scope = 'plan-items'): RestoreAccess
    {
        $this->lookedUp = [];

        return RestoreAccess::decide(
            $admin,
            static fn (int $id): bool => in_array($id, $access, true),
            $domainId,
            function (int $id): ?array {
                $this->lookedUp[] = $id;
                return self::DOMAINS[$id] ?? null;
            },
            $scope
        );
    }

    public function testCustomerOwnSubscriptionIsAllowedWithItsFeatures(): void
    {
        $access = $this->decide(false, [10, 11], 10);

        self::assertTrue($access->allowed());
        self::assertNull($access->refusal());
        self::assertSame('example.test', $access->subscription());
        self::assertSame(['files', 'db'], $access->features());
        self::assertTrue($access->may('files'));
        self::assertFalse($access->may('mail'));
        self::assertFalse($access->isAdmin());
        self::assertTrue($access->covered());
    }

    public function testASubdomainActsOnItsSubscription(): void
    {
        $access = $this->decide(false, [10, 11], 11);

        self::assertTrue($access->allowed());
        self::assertSame('example.test', $access->subscription());
    }

    public function testAForeignDomainIsRefusedBeforeItIsLookedUp(): void
    {
        $access = $this->decide(false, [10, 11], 20);

        self::assertFalse($access->allowed());
        self::assertSame(RestoreAccess::NO_ACCESS, $access->refusal());
        self::assertNull($access->subscription());
        self::assertSame([], $access->features());
        self::assertSame([], $this->lookedUp);
    }

    public function testAccessToTheDomainButNotItsSubscriptionIsRefused(): void
    {
        $access = $this->decide(false, [11], 11);

        self::assertSame(RestoreAccess::NO_ACCESS, $access->refusal());
        self::assertNull($access->subscription());
    }

    public function testAMissingOrZeroIdIsRefused(): void
    {
        self::assertSame(RestoreAccess::NO_DOMAIN, $this->decide(false, [0], 0)->refusal());
        self::assertSame(RestoreAccess::NO_DOMAIN, $this->decide(true, [], -5)->refusal());
        self::assertSame([], $this->lookedUp);
        self::assertSame(RestoreAccess::NO_DOMAIN, $this->decide(false, [99], 99)->refusal());
        self::assertSame(RestoreAccess::NO_DOMAIN, $this->decide(true, [], 99)->refusal());
    }

    public function testASubscriptionWithoutCoverageIsRefusedAsNotCovered(): void
    {
        $access = $this->decide(false, [30], 30);

        self::assertFalse($access->allowed());
        self::assertSame(RestoreAccess::NOT_COVERED, $access->refusal());
        self::assertSame('bare.example.test', $access->subscription());
        self::assertSame([], $access->features());
        self::assertFalse($access->may('files'));
        self::assertFalse($access->covered());
    }

    public function testAnUnknownScopeCoversNothing(): void
    {
        self::assertSame(RestoreAccess::NOT_COVERED, $this->decide(false, [10], 10, 'everything')->refusal());
    }

    public function testScopeAllCoversEveryFeature(): void
    {
        $access = $this->decide(false, [30], 30, 'all');

        self::assertTrue($access->allowed());
        self::assertSame(['mail', 'files', 'db'], $access->features());
    }

    public function testTheServerRepositoryAndBadNamesAreNeverReachable(): void
    {
        self::assertSame(RestoreAccess::BAD_NAME, $this->decide(false, [40], 40, 'all')->refusal());
        self::assertSame(RestoreAccess::BAD_NAME, $this->decide(true, [], 40, 'all')->refusal());
        self::assertNull($this->decide(true, [], 40)->subscription());
        self::assertSame(RestoreAccess::BAD_NAME, $this->decide(true, [], 41)->refusal());
    }

    public function testAnAdministratorMayActOnAnySubscription(): void
    {
        $foreign = $this->decide(true, [], 20);
        self::assertTrue($foreign->allowed());
        self::assertSame('other.example.test', $foreign->subscription());
        self::assertTrue($foreign->isAdmin());

        $uncovered = $this->decide(true, [], 30);
        self::assertTrue($uncovered->allowed());
        self::assertSame(['mail', 'files', 'db'], $uncovered->features());
        self::assertFalse($uncovered->covered(), 'the button stays hidden on an uncovered subscription');
        self::assertTrue($foreign->covered());
    }
}

<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\Coverage;
use PHPUnit\Framework\TestCase;

final class CoverageTest extends TestCase
{
    public function testScopeAllCoversEverySubscriptionWithEveryFeature(): void
    {
        self::assertSame(['mail', 'files', 'db'], Coverage::features('all', []));
        self::assertTrue(Coverage::isCovered('all', []));
    }

    public function testPlanItemsGiveTheirFeatures(): void
    {
        self::assertSame(['mail'], Coverage::features('plan-items', ['relical-mail']));
        self::assertSame(['files', 'db'], Coverage::features('plan-items', ['relical-web']));
        self::assertSame(['mail', 'files', 'db'], Coverage::features('plan-items', ['relical-web', 'other', 'relical-mail']));
    }

    public function testNoRelicalItemMeansNotCovered(): void
    {
        self::assertSame([], Coverage::features('plan-items', ['some-other-extension-item']));
        self::assertFalse(Coverage::isCovered('plan-items', []));
    }

    public function testUnknownScopeIsRefused(): void
    {
        self::assertFalse(Coverage::isValidScope('everything'));
        $this->expectException(InvalidArgumentException::class);
        Coverage::isCovered('everything', ['relical-mail']);
    }
}

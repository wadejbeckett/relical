<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\PleskDomains;
use PHPUnit\Framework\TestCase;

final class PleskDomainsTest extends TestCase
{
    public function testTheDomainIdComesFromDomIdThenSiteId(): void
    {
        self::assertSame(10, PleskDomains::domainId(['dom_id' => '10', 'site_id' => '11']));
        self::assertSame(11, PleskDomains::domainId(['site_id' => '11']));
        self::assertSame(12, PleskDomains::domainId(['dom_id' => 12]));
    }

    public function testAnythingButAPlainPositiveNumberIsZero(): void
    {
        foreach (['', '0', '-1', '10abc', ' 10', '1e3', '010', '99999999999'] as $value) {
            self::assertSame(0, PleskDomains::domainId(['dom_id' => $value]), var_export($value, true));
        }
        self::assertSame(0, PleskDomains::domainId(['dom_id' => ['10']]));
        self::assertSame(0, PleskDomains::domainId([]));
    }

    public function testAnAliasHasNoRestorePage(): void
    {
        self::assertSame(0, PleskDomains::domainId(['dom_id' => '10', 'alias_id' => '3']));
    }

    public function testWithoutHasPermissionInTheSdkThereIsNoPermission(): void
    {
        self::assertFalse(PleskDomains::hasPermission(new \stdClass(), 10));
    }

    public function testAFailingLookupIsNoPermission(): void
    {
        $client = new class () {
            public function hasPermission(string $name, ?object $domain = null): bool
            {
                return true;
            }
        };
        // No Plesk here: pm_Domain::getByDomainId cannot be called, which must read as "no".
        self::assertFalse(PleskDomains::hasPermission($client, 10));
    }
}

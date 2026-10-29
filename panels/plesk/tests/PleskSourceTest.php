<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\PleskSource;
use PHPUnit\Framework\TestCase;

final class PleskSourceTest extends TestCase
{
    public function testASubdomainWithoutMailServiceIsNotAProblem(): void
    {
        // What Plesk 18.0.81 answers for a subdomain (Docker Plesk test).
        self::assertTrue(PleskSource::hasNoMailService('There is no DSMail service for domain with id = 2'));
        self::assertFalse(PleskSource::hasNoMailService('Mail service is temporarily unavailable'));
        self::assertFalse(PleskSource::hasNoMailService(''));
    }
}

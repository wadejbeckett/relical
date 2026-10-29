<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use PHPUnit\Framework\TestCase;

/**
 * The controllers redirect to absolute paths (pm_Context::getBaseUrl(),
 * getActionUrl()). Zend's _redirect() prepends the request's base URL
 * (/modules/relical/index.php) unless told not to, which on Plesk 18.0.81
 * sent every such redirect to /modules/relical/index.php/modules/relical/...
 * (seen in the Docker Plesk test).
 */
final class ControllerRedirectTest extends TestCase
{
    public function testEveryRedirectKeepsItsAbsolutePath(): void
    {
        $calls = 0;
        foreach (glob(__DIR__ . '/../plib/controllers/*.php') ?: [] as $file) {
            preg_match_all('/->_redirect\((.*)\);/', (string) file_get_contents($file), $m);
            foreach ($m[1] as $args) {
                $calls++;
                self::assertStringContainsString("['prependBase' => false]", $args, basename($file));
            }
        }
        self::assertGreaterThan(0, $calls);
    }
}

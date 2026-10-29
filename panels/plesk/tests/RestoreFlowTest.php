<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\RestoreAccess;
use Noiz\Relical\RestoreFlow;
use PHPUnit\Framework\TestCase;

final class RestoreFlowTest extends TestCase
{
    private const CONTROLLER = __DIR__ . '/../plib/controllers/RestoreController.php';

    /** @param list<string> $planItems */
    private static function access(bool $admin, bool $hasAccess, array $planItems = ['relical-web']): RestoreAccess
    {
        return RestoreAccess::decide(
            $admin,
            static fn (int $id): bool => $hasAccess,
            static fn (int $id): bool => true,
            10,
            static fn (int $id): array => ['id' => 10, 'name' => 'example.test', 'plan_items' => $planItems],
            'plan-items'
        );
    }

    public function testNoActionProceedsForAForeignDomain(): void
    {
        foreach (array_keys(RestoreFlow::ACTIONS) as $action) {
            self::assertSame(RestoreFlow::DENY, RestoreFlow::gate(self::access(false, false), $action), $action);
        }
    }

    public function testEveryActionShowsTheNotCoveredPageWithoutCoverage(): void
    {
        foreach (array_keys(RestoreFlow::ACTIONS) as $action) {
            self::assertSame(RestoreFlow::NOT_COVERED, RestoreFlow::gate(self::access(false, true, []), $action), $action);
        }
    }

    public function testAUnitOutsideThePlanIsDenied(): void
    {
        $web = self::access(false, true, ['relical-web']);

        self::assertSame(RestoreFlow::DENY, RestoreFlow::gate($web, 'mail'));
        self::assertSame(RestoreFlow::PROCEED, RestoreFlow::gate($web, 'files'));
        self::assertSame(RestoreFlow::PROCEED, RestoreFlow::gate($web, 'database'));
        self::assertSame(RestoreFlow::PROCEED, RestoreFlow::gate($web, 'index'));
        self::assertSame(RestoreFlow::PROCEED, RestoreFlow::gate($web, 'status'));
        self::assertSame(RestoreFlow::DENY, RestoreFlow::gate($web, 'adopt'), 'an unknown action');
    }

    public function testAnAdministratorProceedsEverywhere(): void
    {
        foreach (array_keys(RestoreFlow::ACTIONS) as $action) {
            self::assertSame(RestoreFlow::PROCEED, RestoreFlow::gate(self::access(true, false, []), $action), $action);
        }
    }

    /**
     * The controller cannot be built without Plesk, so its source is read:
     * every action's first statement is the gate for that action, init()
     * runs no root script, and the actions are exactly RestoreFlow's.
     */
    public function testTheControllerGatesEveryActionBeforeAnythingElse(): void
    {
        $source = (string) file_get_contents(self::CONTROLLER);

        preg_match_all('/public function (\w+)Action\(\)\s*\{\s*(.*?);/s', $source, $m, PREG_SET_ORDER);
        $actions = [];
        foreach ($m as [, $action, $first]) {
            $actions[] = $action;
            self::assertMatchesRegularExpression(
                '/^if \(!\$this->gate\(\'' . preg_quote($action, '/') . '\'\)\) \{\s*return$/',
                trim($first),
                $action . 'Action must start with its gate'
            );
        }
        self::assertSame(array_keys(RestoreFlow::ACTIONS), $actions);

        self::assertSame(1, preg_match('/public function init\(\)\s*\{(.*?)\n    \}/s', $source, $init));
        self::assertStringNotContainsString('RootScripts', $init[1]);
        self::assertStringContainsString('PleskDomains::access', $init[1]);
    }
}

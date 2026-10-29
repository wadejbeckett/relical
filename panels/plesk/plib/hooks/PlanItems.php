<?php

declare(strict_types=1);

/**
 * Relical's plan items. Plesk creates them when the extension is installed
 * and removes them when it is uninstalled; an administrator (or WHMCS, via
 * add-on plans) switches them on per service plan or add-on plan. They only
 * matter when the extension's scope is "plan-items" (see Relical\Coverage).
 */

use Noiz\Relical\Coverage;

require_once __DIR__ . '/../library/autoload.php';

class Modules_Relical_PlanItems extends pm_Hook_PlanItems
{
    public function getPlanItems()
    {
        return [
            Coverage::ITEM_MAIL => 'Mail backup (Relical)',
            Coverage::ITEM_WEB => 'Website and database backup (Relical)',
        ];
    }

    /**
     * Non-exclusive: Plesk shows checkboxes, so a plan can carry either item
     * or both (an exclusive set is a single drop-down choice).
     */
    public function isExclusive()
    {
        return false;
    }
}

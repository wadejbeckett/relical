<?php

declare(strict_types=1);

/**
 * The permission to use the restore screens. It appears on the
 * Permissions tab of service plans and subscriptions, on by default, so a
 * covered subscription can restore unless an administrator switches it
 * off; Plesk's roles for additional users may then grant or withhold it
 * (RestoreAccess checks it for every viewer who is not an administrator).
 */

use Noiz\Relical\RestoreAccess;

require_once __DIR__ . '/../library/autoload.php';

class Modules_Relical_Permissions extends pm_Hook_Permissions
{
    public function getPermissions()
    {
        return [
            RestoreAccess::PERMISSION => [
                'default' => true,
                'place' => self::PLACE_MAIN,
                'name' => 'Restore from Relical backups',
                'description' => 'Restore mail, website files and databases from Relical backups, into new folders beside the live data.',
            ],
        ];
    }
}

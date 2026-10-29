<?php

declare(strict_types=1);

/**
 * The "Backups" button on a subscription's domain card. It links to the
 * customer restore screens (RestoreController) and is shown only when the
 * subscription is covered by Relical and the viewer may manage it
 * (RestoreAccess). The controller repeats the access check; hiding the
 * button is not the security boundary.
 */

use Noiz\Relical\PleskDomains;

require_once __DIR__ . '/../library/autoload.php';

class Modules_Relical_CustomButtons extends pm_Hook_CustomButtons
{
    public function getButtons()
    {
        return [
            [
                'place' => self::PLACE_DOMAIN,
                'title' => 'Backups',
                'description' => 'Restore mail, website files or databases from Relical backups.',
                'icon' => pm_Context::getBaseUrl() . 'images/backups.png',
                'link' => pm_Context::getActionUrl('restore', 'index'),
                'contextParams' => true,
                'visibility' => [$this, 'isVisible'],
            ],
        ];
    }

    /**
     * @param array<string, mixed> $params the context Plesk passes: dom_id
     *        (the subscription), site_id (the site) and alias_id (an alias)
     */
    public function isVisible($params): bool
    {
        try {
            if (!is_array($params)) {
                return false;
            }
            $access = PleskDomains::access($params);

            return $access->allowed() && $access->covered();
        } catch (Throwable $e) {
            // An unknown scope or a missing domain hides the button.
            return false;
        }
    }
}

<?php

declare(strict_types=1);

/**
 * The "Backups" button on a subscription's domain card. It links to the
 * customer restore screens (RestoreController, Task 9) and is shown only
 * when the subscription is covered by Relical and the viewer may manage it.
 * The controller repeats the access check; hiding the button is not the
 * security boundary.
 */

use Noiz\Relical\Coverage;
use Noiz\Relical\Settings;

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
            if (!is_array($params) || isset($params['alias_id'])) {
                return false;
            }
            $domain = self::webspace((int) ($params['dom_id'] ?? $params['site_id'] ?? 0));
            if ($domain === null) {
                return false;
            }
            if (!pm_Session::getClient()->hasAccessToDomain($domain->getId())) {
                return false;
            }

            return Coverage::isCovered(Settings::fromPlesk()->scope(), array_values((array) $domain->getPlanItems()));
        } catch (Throwable $e) {
            // An unknown scope or a missing domain hides the button.
            return false;
        }
    }

    /** The subscription's main domain for a domain id (itself, or its webspace). */
    private static function webspace(int $domainId): ?pm_Domain
    {
        if ($domainId <= 0) {
            return null;
        }
        $domain = pm_Domain::getByDomainId($domainId);
        $webspaceId = (int) $domain->getProperty('webspace_id');

        return $webspaceId > 0 && $webspaceId !== $domainId ? pm_Domain::getByDomainId($webspaceId) : $domain;
    }
}

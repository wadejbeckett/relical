<?php

declare(strict_types=1);

namespace Noiz\Relical;

use Throwable;

/**
 * The Plesk lookups RestoreAccess needs, shared by the "Backups" button
 * (CustomButtons) and the restore screens (RestoreController).
 */
final class PleskDomains
{
    /**
     * The subscription (webspace) a domain id belongs to: the domain itself
     * for a main domain, else the domain named by its `webspace_id`.
     * pm_Domain::getName() is the punycode name the inventory also uses.
     *
     * @return ?array{id: int, name: string, plan_items: list<string>} null when there is no such domain
     */
    public static function webspaceOf(int $domainId): ?array
    {
        if ($domainId <= 0) {
            return null;
        }
        try {
            $domain = \pm_Domain::getByDomainId($domainId);
            $webspaceId = (int) $domain->getProperty('webspace_id');
            if ($webspaceId > 0 && $webspaceId !== $domainId) {
                $domain = \pm_Domain::getByDomainId($webspaceId);
            }

            return [
                'id' => (int) $domain->getId(),
                'name' => (string) $domain->getName(),
                'plan_items' => array_values(array_map('strval', (array) $domain->getPlanItems())),
            ];
        } catch (Throwable $e) {
            return null;
        }
    }

    /**
     * Decides access for the current Plesk session and request.
     *
     * @param array<string, mixed> $params the request's (or button's) parameters
     */
    public static function access(array $params): RestoreAccess
    {
        $client = \pm_Session::getClient();

        return RestoreAccess::decide(
            (bool) $client->isAdmin(),
            static fn (int $id): bool => (bool) $client->hasAccessToDomain($id),
            static fn (int $id): bool => self::hasPermission($client, $id),
            self::domainId($params),
            [self::class, 'webspaceOf'],
            Settings::fromPlesk()->scope()
        );
    }

    /**
     * The domain id a request or button names: `dom_id`, else `site_id`;
     * 0 (refused) for an alias, which has no restore page of its own, and
     * for anything that is not a plain number.
     *
     * @param array<string, mixed> $params
     */
    public static function domainId(array $params): int
    {
        if (isset($params['alias_id'])) {
            return 0;
        }
        $value = $params['dom_id'] ?? $params['site_id'] ?? '';
        if (is_int($value)) {
            return $value;
        }

        return is_string($value) && preg_match('/^[1-9][0-9]{0,9}$/D', $value) === 1 ? (int) $value : 0;
    }

    /**
     * Whether the viewer holds RestoreAccess::PERMISSION for the domain.
     * False (refused) when the SDK cannot answer, so an unknown case never
     * opens the screens.
     *
     * @param object $client pm_Client
     */
    public static function hasPermission(object $client, int $domainId): bool
    {
        if (!method_exists($client, 'hasPermission')) {
            return false;
        }
        try {
            return (bool) $client->hasPermission(RestoreAccess::PERMISSION, \pm_Domain::getByDomainId($domainId));
        } catch (Throwable $e) {
            return false;
        }
    }
}

<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;

/**
 * Who may use the customer restore screens for which subscription.
 *
 * The restore button carries a `dom_id` (or `site_id`) from the request,
 * which the viewer controls, so nothing is trusted until these checks pass,
 * in this order:
 *
 * 1. the id is a positive number;
 * 2. the viewer may access that domain (before it is even looked up);
 * 3. the domain resolves to its subscription (webspace), and the restore
 *    acts on that subscription, never on the domain the request named;
 * 4. the viewer may access the subscription as well;
 * 5. its name is a valid subscription name (never `_server`, which holds
 *    the Plesk configuration and is the administrator's alone);
 * 6. the subscription is covered (Coverage). A customer whose plan does
 *    not include Relical is refused with NOT_COVERED, so the screen can say
 *    so plainly; an administrator may act on any subscription with every
 *    unit, since older backups can outlive the plan item.
 *
 * Pure: the Plesk session and domain lookups come in as callables.
 */
final class RestoreAccess
{
    public const NO_DOMAIN = 'no-domain';
    public const NO_ACCESS = 'no-access';
    public const BAD_NAME = 'bad-name';
    public const NOT_COVERED = 'not-covered';

    private ?string $subscription;
    /** @var list<string> */
    private array $features;
    private ?string $refusal;
    private bool $admin;
    private bool $covered;

    /** @param list<string> $features */
    private function __construct(?string $subscription, array $features, ?string $refusal, bool $admin, bool $covered = false)
    {
        $this->subscription = $subscription;
        $this->features = $features;
        $this->refusal = $refusal;
        $this->admin = $admin;
        $this->covered = $covered;
    }

    /**
     * @param callable(int): bool $hasAccess pm_Client::hasAccessToDomain
     * @param callable(int): ?array{id: int, name: string, plan_items: list<string>} $webspaceOf
     *        the subscription a domain id belongs to (itself for a main
     *        domain), or null when there is no such domain
     * @param string $scope the Settings scope (Coverage::SCOPE_*)
     */
    public static function decide(bool $isAdmin, callable $hasAccess, int $domainId, callable $webspaceOf, string $scope): self
    {
        if ($domainId <= 0) {
            return self::refuse(self::NO_DOMAIN, $isAdmin);
        }
        if (!$isAdmin && $hasAccess($domainId) !== true) {
            return self::refuse(self::NO_ACCESS, $isAdmin);
        }
        $webspace = $webspaceOf($domainId);
        if (!is_array($webspace) || (int) ($webspace['id'] ?? 0) <= 0) {
            return self::refuse(self::NO_DOMAIN, $isAdmin);
        }
        if (!$isAdmin && $hasAccess((int) $webspace['id']) !== true) {
            return self::refuse(self::NO_ACCESS, $isAdmin);
        }
        $name = (string) ($webspace['name'] ?? '');
        try {
            Command::domain($name);
        } catch (InvalidArgumentException $e) {
            return self::refuse(self::BAD_NAME, $isAdmin);
        }

        try {
            $features = Coverage::features($scope, array_values(array_map('strval', (array) ($webspace['plan_items'] ?? []))));
        } catch (InvalidArgumentException $e) {
            $features = []; // an unknown scope covers nothing
        }
        if ($isAdmin) {
            return new self($name, Coverage::ALL_FEATURES, null, true, $features !== []);
        }
        if ($features === []) {
            return new self($name, [], self::NOT_COVERED, false);
        }

        return new self($name, $features, null, false, true);
    }

    private static function refuse(string $reason, bool $isAdmin): self
    {
        return new self(null, [], $reason, $isAdmin);
    }

    public function allowed(): bool
    {
        return $this->refusal === null;
    }

    /** Why the viewer was refused (one of the constants), or null. */
    public function refusal(): ?string
    {
        return $this->refusal;
    }

    /**
     * The subscription the restore acts on. Set when allowed, and also for
     * NOT_COVERED (so the page can name it); null for every other refusal.
     */
    public function subscription(): ?string
    {
        return $this->subscription;
    }

    /** @return list<string> the units the viewer may restore (mail, files, db) */
    public function features(): array
    {
        return $this->features;
    }

    public function may(string $unit): bool
    {
        return $this->allowed() && in_array($unit, $this->features, true);
    }

    /**
     * Whether the subscription's plan includes Relical (the "Backups"
     * button is shown only then, also to an administrator).
     */
    public function covered(): bool
    {
        return $this->covered;
    }

    public function isAdmin(): bool
    {
        return $this->admin;
    }
}

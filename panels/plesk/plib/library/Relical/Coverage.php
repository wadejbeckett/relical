<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;

/**
 * Which subscriptions Relical covers, and with which features.
 *
 * Scope `all` covers every subscription with every feature. Scope
 * `plan-items` covers a subscription only through the Relical plan items its
 * plan (or an add-on plan) carries: `relical-mail` gives mail,
 * `relical-web` gives website files and databases.
 */
final class Coverage
{
    public const SCOPE_ALL = 'all';
    public const SCOPE_PLAN_ITEMS = 'plan-items';

    public const ITEM_MAIL = 'relical-mail';
    public const ITEM_WEB = 'relical-web';

    public const FEATURE_MAIL = 'mail';
    public const FEATURE_FILES = 'files';
    public const FEATURE_DB = 'db';

    /** @var list<string> every subscription feature, in backup order */
    public const ALL_FEATURES = [self::FEATURE_MAIL, self::FEATURE_FILES, self::FEATURE_DB];

    /**
     * @param list<string> $planItems the subscription's plan item keys
     * @return list<string> features, in backup order (mail, files, db)
     */
    public static function features(string $scope, array $planItems): array
    {
        if ($scope === self::SCOPE_ALL) {
            return self::ALL_FEATURES;
        }
        if ($scope !== self::SCOPE_PLAN_ITEMS) {
            // An unknown scope covers nothing rather than guessing.
            throw new InvalidArgumentException(sprintf('unknown scope "%s"', $scope));
        }

        $items = array_map('strval', $planItems);
        $features = [];
        if (in_array(self::ITEM_MAIL, $items, true)) {
            $features[] = self::FEATURE_MAIL;
        }
        if (in_array(self::ITEM_WEB, $items, true)) {
            $features[] = self::FEATURE_FILES;
            $features[] = self::FEATURE_DB;
        }

        return $features;
    }

    /**
     * @param list<string> $planItems
     */
    public static function isCovered(string $scope, array $planItems): bool
    {
        return self::features($scope, $planItems) !== [];
    }

    public static function isValidScope(string $scope): bool
    {
        return $scope === self::SCOPE_ALL || $scope === self::SCOPE_PLAN_ITEMS;
    }
}

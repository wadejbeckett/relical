<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * What each restore screen may do for a viewer, decided before the screen
 * runs any root script. Pure; RestoreController calls gate() as the first
 * statement of every action (RestoreFlowTest pins that too).
 */
final class RestoreFlow
{
    public const PROCEED = 'proceed';
    /** "Permission denied": no root script runs. */
    public const DENY = 'deny';
    /** The plain "not included in your plan" page: no root script runs. */
    public const NOT_COVERED = 'not-covered';

    /** Each action of RestoreController and the unit it needs ('' for none). */
    public const ACTIONS = [
        'index' => '',
        'mail' => 'mail',
        'files' => 'files',
        'database' => 'db',
        'status' => '',
    ];

    public static function gate(RestoreAccess $access, string $action): string
    {
        if (!array_key_exists($action, self::ACTIONS)) {
            return self::DENY;
        }
        if ($access->refusal() === RestoreAccess::NOT_COVERED) {
            return self::NOT_COVERED;
        }
        if (!$access->allowed()) {
            return self::DENY;
        }
        $unit = self::ACTIONS[$action];

        return $unit === '' || $access->may($unit) ? self::PROCEED : self::DENY;
    }
}

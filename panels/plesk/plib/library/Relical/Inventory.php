<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * Builds the inventory the core reads from `inventory_command` (the
 * "Inventory contract" in the phase 2 plan).
 *
 * Pure: it takes plain arrays describing the webspaces, as PleskSource
 * gathers them, and returns the inventory array. The core validates paths
 * and names again; this class only shapes the data and decides coverage.
 *
 * A webspace is described as:
 *
 *     [
 *         'id' => 17,                        // Plesk domain id of the webspace
 *         'name' => 'example.test',          // lower-case / punycode, as Plesk stores it
 *         'renamed_from' => '',              // optional, see trackRenames()
 *         'sys_user' => 'exampleuser',       // '' when the webspace has no hosting
 *         'uid' => 10001, 'gid' => 1004,     // null when the system user is unknown
 *         'home' => '/var/www/vhosts/example.test',
 *         'mail_domains' => ['example.test', 'shop.example.test'],
 *         'databases' => [['type' => 'mysql', 'name' => 'example_wp']],
 *         'plan_items' => ['relical-mail'],
 *     ]
 */
final class Inventory
{
    public const VERSION = 2;

    /** @var list<string> applied when a subscription lists no excludes of its own */
    public const DEFAULT_FILE_EXCLUDES = ['logs/**', 'statistics/logs/**', 'tmp/**', '.cagefs/**'];

    private const DATABASE_NAME = '/^[A-Za-z0-9_$-]{1,64}$/D';

    /**
     * @param list<array<string, mixed>> $webspaces
     * @return array{version: int, subscriptions: list<array<string, mixed>>, server: array<string, mixed>, skipped: list<array{subscription: string, item: string, reason: string}>}
     */
    public static function build(array $webspaces, string $scope): array
    {
        $subscriptions = [];
        $skipped = [];

        foreach ($webspaces as $webspace) {
            $name = (string) ($webspace['name'] ?? '');

            if ($name === Paths::SERVER_REPO || $name === '') {
                $skipped[] = self::skip($name, 'subscription', 'the name is empty or reserved for the Plesk configuration');
                continue;
            }

            $features = Coverage::features($scope, self::stringList($webspace['plan_items'] ?? []));
            if ($features === []) {
                continue; // not covered: nothing to back up, nothing to report
            }

            $sysUser = (string) ($webspace['sys_user'] ?? '');
            $uid = $webspace['uid'] ?? null;
            $gid = $webspace['gid'] ?? null;
            if ($sysUser === '' || !is_int($uid) || !is_int($gid) || $uid <= 0 || $gid <= 0) {
                // Restores are written as the subscription's system user;
                // without one (no web hosting) nothing may be restored, so
                // the subscription is left out rather than given root's ids.
                $skipped[] = self::skip($name, 'subscription', 'it has no system user (no web hosting)');
                continue;
            }

            $home = (string) ($webspace['home'] ?? '');

            $databases = [];
            foreach (($webspace['databases'] ?? []) as $database) {
                $type = strtolower((string) ($database['type'] ?? ''));
                $dbName = (string) ($database['name'] ?? '');
                if ($type !== 'mysql') {
                    $skipped[] = self::skip($name, 'database ' . $dbName, sprintf('%s databases are not supported', $type === '' ? 'untyped' : $type));
                    continue;
                }
                if (preg_match(self::DATABASE_NAME, $dbName) !== 1) {
                    $skipped[] = self::skip($name, 'database ' . $dbName, 'the name has characters Relical does not accept');
                    continue;
                }
                $databases[] = ['type' => 'mysql', 'name' => $dbName];
            }

            $subscriptions[] = [
                'id' => (string) ($webspace['id'] ?? ''),
                'name' => $name,
                'renamed_from' => (string) ($webspace['renamed_from'] ?? ''),
                'sys_user' => $sysUser,
                'uid' => $uid,
                'gid' => $gid,
                'home' => $home,
                'features' => $features,
                'mail_domains' => self::mailDomains($name, self::stringList($webspace['mail_domains'] ?? [])),
                'files' => [$home],
                'file_excludes' => self::DEFAULT_FILE_EXCLUDES,
                'databases' => $databases,
                'destination' => '',
                'retention' => null,
            ];
        }

        return [
            'version' => self::VERSION,
            'subscriptions' => $subscriptions,
            'server' => [
                'features' => ['panel-config'],
                'config_command' => Paths::panelConfigCommand(),
                'config_ext' => 'tar',
            ],
            'skipped' => $skipped,
        ];
    }

    /**
     * Carries `renamed_from` across runs. Plesk keeps a webspace's id when it
     * is renamed; the core names repositories by name, so a rename starts a
     * new repository and the hint lets `status` point at the old one.
     *
     * @param array<string, array{name?: string, renamed_from?: string}> $known  id => last seen name and hint
     * @param list<array<string, mixed>> $webspaces
     * @return array{0: list<array<string, mixed>>, 1: array<string, array{name: string, renamed_from: string}>}
     *         the webspaces with `renamed_from` set, and the map to store
     */
    public static function trackRenames(array $known, array $webspaces): array
    {
        $next = [];
        foreach ($webspaces as $i => $webspace) {
            $id = (string) ($webspace['id'] ?? '');
            $name = (string) ($webspace['name'] ?? '');
            $previous = $known[$id] ?? null;

            $renamedFrom = '';
            if (is_array($previous)) {
                $lastName = (string) ($previous['name'] ?? '');
                $renamedFrom = $lastName !== '' && $lastName !== $name
                    ? $lastName
                    : (string) ($previous['renamed_from'] ?? '');
                if ($renamedFrom === $name) {
                    $renamedFrom = ''; // renamed back
                }
            }

            $webspaces[$i]['renamed_from'] = $renamedFrom;
            if ($id !== '') {
                $next[$id] = ['name' => $name, 'renamed_from' => $renamedFrom];
            }
        }

        return [array_values($webspaces), $next];
    }

    /**
     * The webspace's own domain first, then the others, without duplicates.
     *
     * @param list<string> $domains
     * @return list<string>
     */
    private static function mailDomains(string $name, array $domains): array
    {
        $out = [];
        foreach ($domains as $domain) {
            if ($domain !== '' && !in_array($domain, $out, true)) {
                $out[] = $domain;
            }
        }
        if (in_array($name, $out, true)) {
            $out = array_values(array_diff($out, [$name]));
            array_unshift($out, $name);
        }

        return $out;
    }

    /**
     * @param mixed $value
     * @return list<string>
     */
    private static function stringList($value): array
    {
        return is_array($value) ? array_values(array_map('strval', $value)) : [];
    }

    /** @return array{subscription: string, item: string, reason: string} */
    private static function skip(string $subscription, string $item, string $reason): array
    {
        return ['subscription' => $subscription, 'item' => $item, 'reason' => $reason];
    }
}

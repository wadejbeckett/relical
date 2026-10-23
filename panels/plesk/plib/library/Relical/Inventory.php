<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * Builds the inventory the core reads from `inventory_command` (the
 * "Inventory contract" in the phase 2 plan).
 *
 * Pure: it takes plain arrays describing the webspaces, as PleskSource
 * gathers them, and returns the inventory array. The core refuses the whole
 * inventory if one entry breaks its rules, so this class applies the same
 * rules (CoreRules) first: whatever would be refused is left out and listed
 * in `skipped` as {name, reason}, and everything else still backs up.
 *
 * A webspace is described as:
 *
 *     [
 *         'id' => 17,                        // Plesk domain id of the webspace
 *         'name' => 'example.test',          // the punycode (ASCII) name
 *         'renamed_from' => '',              // optional, see trackRenames()
 *         'sys_user' => 'exampleuser',       // '' when the webspace has no hosting
 *         'uid' => 10001, 'gid' => 1004,     // null when the system user is unknown
 *         'home' => '/var/www/vhosts/example.test',
 *         'mail_domains' => ['example.test', 'shop.example.test'],  // punycode
 *         'databases' => [['type' => 'mysql', 'name' => 'example_wp', 'local' => true]],
 *         'plan_items' => ['relical-mail'],
 *     ]
 *
 * `local` says whether the database lives on this server's own database
 * server (the only one the MySQL option file can reach); a missing `local`
 * counts as remote.
 */
final class Inventory
{
    public const VERSION = 2;

    /** @var list<string> applied when a subscription lists no excludes of its own */
    public const DEFAULT_FILE_EXCLUDES = ['logs/**', 'statistics/logs/**', 'tmp/**', '.cagefs/**'];

    /**
     * @param list<array<string, mixed>> $webspaces
     * @param list<string>               $filesRoots the core's roots.files
     * @return array{version: int, subscriptions: list<array<string, mixed>>, server: array<string, mixed>, skipped: list<array{name: string, reason: string}>}
     */
    public static function build(array $webspaces, string $scope, array $filesRoots = [Paths::FILES_ROOT]): array
    {
        $subscriptions = [];
        $skipped = [];
        $names = [];
        $ids = [];
        $mailOwner = [];

        foreach ($webspaces as $webspace) {
            $name = (string) ($webspace['name'] ?? '');
            $id = (string) ($webspace['id'] ?? '');

            if ($name === Paths::SERVER_REPO) {
                $skipped[] = self::skip($name, 'the name is reserved for the Plesk configuration');
                continue;
            }
            if (!CoreRules::validDomain($name)) {
                $skipped[] = self::skip($name, 'the subscription name is not a lower-case (punycode) domain name');
                continue;
            }
            if (isset($names[$name])) {
                $skipped[] = self::skip($name, 'the subscription is listed twice');
                continue;
            }
            if (!CoreRules::validId($id)) {
                $skipped[] = self::skip($name, sprintf('the Plesk id "%s" is not valid', $id));
                continue;
            }
            if (isset($ids[$id])) {
                $skipped[] = self::skip($name, sprintf('the Plesk id %s is also used by %s', $id, $ids[$id]));
                continue;
            }

            $features = Coverage::features($scope, self::stringList($webspace['plan_items'] ?? []));
            if ($features === []) {
                continue; // not covered: nothing to back up, nothing to report
            }

            // Website files and databases are restored into home as the
            // system user, so they need a valid user, ids and home. Without
            // them the subscription is backed up for mail only.
            $sysUser = (string) ($webspace['sys_user'] ?? '');
            $uid = $webspace['uid'] ?? null;
            $gid = $webspace['gid'] ?? null;
            $home = (string) ($webspace['home'] ?? '');
            $rawDatabases = is_array($webspace['databases'] ?? null) ? $webspace['databases'] : [];
            $webProblem = self::webProblem($sysUser, $uid, $gid, $home, $filesRoots);
            $hasWeb = in_array(Coverage::FEATURE_FILES, $features, true) || in_array(Coverage::FEATURE_DB, $features, true);

            if ($webProblem !== null) {
                $noted = false;
                if ($hasWeb && ($sysUser !== '' || $rawDatabases !== [])) {
                    // A hosted webspace with a broken user or home is worth
                    // reporting; no hosting at all is normal for mail-only.
                    $skipped[] = self::skip($name, 'website files and databases: ' . $webProblem);
                    $noted = true;
                }
                $features = array_values(array_diff($features, [Coverage::FEATURE_FILES, Coverage::FEATURE_DB]));
                if ($features === []) {
                    if (!$noted) {
                        $skipped[] = self::skip($name, 'nothing to back up: ' . $webProblem);
                    }
                    continue;
                }
                $sysUser = '';
                $uid = 0;
                $gid = 0;
                $home = '';
                $rawDatabases = [];
            }

            $databases = [];
            $seen = [];
            foreach ($rawDatabases as $database) {
                $type = strtolower((string) ($database['type'] ?? ''));
                $dbName = (string) ($database['name'] ?? '');
                $label = 'database ' . $dbName;
                if ($type !== 'mysql') {
                    $skipped[] = self::skip($name, sprintf('%s: %s databases are not supported', $label, $type === '' ? 'untyped' : $type));
                    continue;
                }
                if (!CoreRules::validDbName($dbName)) {
                    $skipped[] = self::skip($name, $label . ': the name has characters Relical does not accept');
                    continue;
                }
                if (($database['local'] ?? false) !== true) {
                    $skipped[] = self::skip($name, $label . ': it is on a remote database server');
                    continue;
                }
                if (isset($seen[$dbName])) {
                    continue;
                }
                $seen[$dbName] = true;
                $databases[] = ['type' => 'mysql', 'name' => $dbName];
            }

            $mailDomains = [];
            foreach (self::mailDomains($name, self::stringList($webspace['mail_domains'] ?? [])) as $domain) {
                if (!CoreRules::validDomain($domain)) {
                    $skipped[] = self::skip($domain, sprintf('mail of %s: the domain name is not lower-case punycode', $name));
                    continue;
                }
                if (isset($mailOwner[$domain])) {
                    $skipped[] = self::skip($domain, sprintf('mail of %s: the domain is already listed by %s', $name, $mailOwner[$domain]));
                    continue;
                }
                $mailOwner[$domain] = $name;
                $mailDomains[] = $domain;
            }

            $names[$name] = true;
            $ids[$id] = $name;
            $subscriptions[] = [
                'id' => $id,
                'name' => $name,
                'renamed_from' => (string) ($webspace['renamed_from'] ?? ''),
                'sys_user' => $sysUser,
                'uid' => $uid,
                'gid' => $gid,
                'home' => $home,
                'features' => $features,
                'mail_domains' => $mailDomains,
                'files' => $home === '' ? [] : [$home],
                'file_excludes' => self::DEFAULT_FILE_EXCLUDES,
                'databases' => $databases,
                'destination' => '',
                'retention' => null,
            ];
        }

        // The core refuses a renamed_from that is not a domain, is the
        // subscription's own name or still names a subscription: such a
        // hint is dropped, never allowed to stop the run.
        foreach ($subscriptions as $i => $subscription) {
            $from = $subscription['renamed_from'];
            if ($from !== '' && (!CoreRules::validDomain($from) || $from === $subscription['name'] || isset($names[$from]))) {
                $subscriptions[$i]['renamed_from'] = '';
            }
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
     * new repository and the hint lets `status` point at the old one. A hint
     * is cleared as soon as its old name belongs to a current webspace again
     * (a new subscription under the old name, or two names swapped).
     *
     * $known comes from a file on disk; anything malformed in it is ignored.
     *
     * @param mixed                      $known  id => {name, renamed_from}, as last stored
     * @param list<array<string, mixed>> $webspaces
     * @return array{0: list<array<string, mixed>>, 1: array<string, array{name: string, renamed_from: string}>}
     *         the webspaces with `renamed_from` set, and the map to store
     */
    public static function trackRenames($known, array $webspaces): array
    {
        $known = is_array($known) ? $known : [];
        $current = [];
        foreach ($webspaces as $webspace) {
            $current[(string) ($webspace['name'] ?? '')] = true;
        }

        $next = [];
        foreach ($webspaces as $i => $webspace) {
            $id = (string) ($webspace['id'] ?? '');
            $name = (string) ($webspace['name'] ?? '');
            $previous = $known[$id] ?? null;

            $renamedFrom = '';
            if (is_array($previous)) {
                $lastName = is_string($previous['name'] ?? null) ? $previous['name'] : '';
                $lastHint = is_string($previous['renamed_from'] ?? null) ? $previous['renamed_from'] : '';
                $renamedFrom = $lastName !== '' && $lastName !== $name ? $lastName : $lastHint;
            }
            if ($renamedFrom === $name || isset($current[$renamedFrom])) {
                $renamedFrom = '';
            }

            $webspaces[$i]['renamed_from'] = $renamedFrom;
            if ($id !== '') {
                $next[$id] = ['name' => $name, 'renamed_from' => $renamedFrom];
            }
        }

        return [array_values($webspaces), $next];
    }

    /**
     * Why files and databases cannot be backed up, or null when they can.
     *
     * @param mixed        $uid
     * @param mixed        $gid
     * @param list<string> $filesRoots
     */
    private static function webProblem(string $sysUser, $uid, $gid, string $home, array $filesRoots): ?string
    {
        if ($sysUser === '') {
            return 'the subscription has no system user (no web hosting)';
        }
        if (!CoreRules::validSysUser($sysUser)) {
            return sprintf('the system user "%s" is not a name Relical accepts', $sysUser);
        }
        if (!is_int($uid) || !is_int($gid) || $uid <= 0 || $gid <= 0) {
            return sprintf('the system user "%s" has no usable uid and gid', $sysUser);
        }
        if (!CoreRules::cleanAbs($home) || !CoreRules::under($home, $filesRoots)) {
            return sprintf('the home directory "%s" is not inside %s', $home, implode(', ', $filesRoots));
        }

        return null;
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

    /** @return array{name: string, reason: string} the core's Skip shape */
    public static function skip(string $name, string $reason): array
    {
        return ['name' => $name, 'reason' => $reason];
    }
}

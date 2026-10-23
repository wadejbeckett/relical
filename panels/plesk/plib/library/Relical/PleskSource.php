<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;
use SimpleXMLElement;

/**
 * Gathers the webspaces from Plesk as the plain arrays Inventory::build()
 * takes. Every Plesk call the inventory depends on is in this class; it is
 * exercised in the Docker Plesk test (Task 10), not in PHPUnit.
 *
 * Calls used:
 *  - pm_Domain::getAllDomains(true): the webspaces (subscriptions' main
 *    domains), with getId(), getName(), getHomePath(), getSysUserLogin(),
 *    hasHosting() and getPlanItems();
 *  - posix_getpwnam(): the system user's uid and gid;
 *  - API-RPC <site><get><filter><parent-id>…</parent-id></filter>
 *    <dataset><gen_info/></dataset></get></site>: the addon domains and
 *    subdomains of each webspace (all webspaces in one request);
 *  - API-RPC <mail><get_prefs><filter><site-id>…</site-id></filter></get_prefs></mail>:
 *    `prefs/mailservice` says whether mail is on for each domain;
 *  - API-RPC <database><get-db><filter><webspace-id>…</webspace-id></filter></get-db></database>:
 *    each webspace's databases with their type.
 * Requests list up to CHUNK ids at a time so a large server needs a handful
 * of calls rather than one per webspace (the core allows the inventory
 * command 60 seconds).
 */
final class PleskSource
{
    private const CHUNK = 200;
    private const NOT_FOUND = '1013';

    /** @var list<array{subscription: string, item: string, reason: string}> */
    private array $notes = [];

    /**
     * @return list<array<string, mixed>>
     */
    public function webspaces(): array
    {
        $webspaces = [];
        foreach (\pm_Domain::getAllDomains(true) as $domain) {
            $id = (int) $domain->getId();
            $sysUser = $domain->hasHosting() ? (string) $domain->getSysUserLogin() : '';
            $uid = null;
            $gid = null;
            if ($sysUser !== '') {
                $pw = posix_getpwnam($sysUser);
                if (is_array($pw)) {
                    $uid = (int) $pw['uid'];
                    $gid = (int) $pw['gid'];
                }
            }
            $webspaces[$id] = [
                'id' => $id,
                'name' => (string) $domain->getName(),
                'sys_user' => $sysUser,
                'uid' => $uid,
                'gid' => $gid,
                'home' => rtrim((string) $domain->getHomePath(), '/'),
                'mail_domains' => [],
                'databases' => [],
                'plan_items' => array_values(array_map('strval', (array) $domain->getPlanItems())),
            ];
        }
        if ($webspaces === []) {
            return [];
        }

        // Every domain of every webspace, keyed by domain id.
        $domainNames = [];
        $owner = [];
        foreach ($webspaces as $id => $webspace) {
            $domainNames[$id] = $webspace['name'];
            $owner[$id] = $id;
        }
        foreach ($this->sites(array_keys($webspaces)) as $siteId => [$name, $webspaceId]) {
            if (isset($webspaces[$webspaceId]) && !isset($domainNames[$siteId])) {
                $domainNames[$siteId] = $name;
                $owner[$siteId] = $webspaceId;
            }
        }

        foreach ($this->mailOn(array_keys($domainNames)) as $siteId) {
            if (isset($owner[$siteId])) {
                $webspaces[$owner[$siteId]]['mail_domains'][] = $domainNames[$siteId];
            }
        }

        foreach ($this->databases(array_keys($webspaces)) as [$webspaceId, $type, $name]) {
            if (isset($webspaces[$webspaceId])) {
                $webspaces[$webspaceId]['databases'][] = ['type' => $type, 'name' => $name];
            }
        }

        return array_values($webspaces);
    }

    /**
     * Problems that did not stop the inventory (a domain whose mail status
     * could not be read), in Inventory's `skipped` shape.
     *
     * @return list<array{subscription: string, item: string, reason: string}>
     */
    public function notes(): array
    {
        return $this->notes;
    }

    /**
     * @param list<int> $webspaceIds
     * @return array<int, array{0: string, 1: int}> site id => [name, webspace id]
     */
    private function sites(array $webspaceIds): array
    {
        $out = [];
        foreach (array_chunk($webspaceIds, self::CHUNK) as $chunk) {
            $filter = '';
            foreach ($chunk as $id) {
                $filter .= '<parent-id>' . (int) $id . '</parent-id>';
            }
            $response = $this->call('<site><get><filter>' . $filter . '</filter><dataset><gen_info/></dataset></get></site>');
            foreach ($response->xpath('//site/get/result') ?: [] as $result) {
                if ((string) $result->status !== 'ok') {
                    $this->noteError($result, 'additional domains of webspace id ' . (int) $result->{'filter-id'});
                    continue;
                }
                $siteId = (int) $result->id;
                $name = (string) $result->data->gen_info->name;
                $webspaceId = (int) $result->data->gen_info->{'webspace-id'};
                if ($siteId > 0 && $name !== '' && $webspaceId > 0) {
                    $out[$siteId] = [$name, $webspaceId];
                }
            }
        }

        return $out;
    }

    /**
     * @param list<int> $siteIds
     * @return list<int> the ids of the domains whose mail service is on
     */
    private function mailOn(array $siteIds): array
    {
        $on = [];
        foreach (array_chunk($siteIds, self::CHUNK) as $chunk) {
            $filter = '';
            foreach ($chunk as $id) {
                $filter .= '<site-id>' . (int) $id . '</site-id>';
            }
            $response = $this->call('<mail><get_prefs><filter>' . $filter . '</filter></get_prefs></mail>');
            foreach ($response->xpath('//mail/get_prefs/result') ?: [] as $result) {
                $siteId = (int) $result->{'site-id'};
                if ((string) $result->status !== 'ok') {
                    $this->noteError($result, 'mail status of domain id ' . $siteId);
                    continue;
                }
                if (strtolower(trim((string) $result->prefs->mailservice)) === 'true') {
                    $on[] = $siteId;
                }
            }
        }

        return $on;
    }

    /**
     * @param list<int> $webspaceIds
     * @return list<array{0: int, 1: string, 2: string}> [webspace id, type, name]
     */
    private function databases(array $webspaceIds): array
    {
        $out = [];
        foreach (array_chunk($webspaceIds, self::CHUNK) as $chunk) {
            $filter = '';
            foreach ($chunk as $id) {
                $filter .= '<webspace-id>' . (int) $id . '</webspace-id>';
            }
            $response = $this->call('<database><get-db><filter>' . $filter . '</filter></get-db></database>');
            foreach ($response->xpath('//database/get-db/result') ?: [] as $result) {
                if ((string) $result->status !== 'ok') {
                    $this->noteError($result, 'databases of webspace id ' . (int) $result->{'filter-id'});
                    continue;
                }
                $name = (string) $result->name;
                if ($name !== '') {
                    $out[] = [(int) $result->{'webspace-id'}, (string) $result->type, $name];
                }
            }
        }

        return $out;
    }

    /**
     * A per-result error is reported, not fatal. "Object not found" (1013)
     * is how API-RPC answers a filter id that has nothing to list, so it is
     * not a problem.
     */
    private function noteError(SimpleXMLElement $result, string $item): void
    {
        if ((string) $result->errcode === self::NOT_FOUND) {
            return;
        }
        $this->notes[] = [
            'subscription' => '',
            'item' => $item,
            'reason' => 'Plesk did not report it: ' . trim((string) $result->errtext),
        ];
    }

    private function call(string $request): SimpleXMLElement
    {
        $response = \pm_ApiRpc::getService()->call($request);
        if (!$response instanceof SimpleXMLElement) {
            throw new RuntimeException('Plesk API-RPC returned no response');
        }
        // A request-level failure (not per-result) fails the inventory.
        $system = $response->xpath('//system');
        if (is_array($system) && $system !== [] && (string) $system[0]->status === 'error') {
            throw new RuntimeException('Plesk API-RPC error: ' . trim((string) $system[0]->errtext));
        }

        return $response;
    }
}

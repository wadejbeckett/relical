<?php

declare(strict_types=1);

/**
 * Prints the record of one customer restore (root-only, so the status page
 * reads it through this script):
 *
 *     plesk bin extension --exec relical restore-result.php <id>
 *
 * Prints {"found": false} when there is no record yet, else
 * {"found": true, "record": {...}}. A record still marked "running" whose
 * subscription lock nobody holds belonged to a process that died; it is
 * reported (not rewritten) as failed. The caller checks that the record
 * belongs to the viewer's subscription.
 */

use Noiz\Relical\Command;
use Noiz\Relical\RestoreRecord;
use Noiz\Relical\RestoreStore;
use Noiz\Relical\ScriptIO;

// Plesk SDK and module context: see the note in cli.php.
if (class_exists('pm_Loader')) {
    pm_Loader::registerAutoload();
}
if (class_exists('pm_Context')) {
    pm_Context::init('relical');
}

require_once __DIR__ . '/../library/autoload.php';

try {
    $args = Command::scriptArgs('restore-result.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('restore-result.php');

try {
    $store = RestoreStore::fromPlesk();
    $record = $store->read($args[0]);
    if ($record === null) {
        ScriptIO::ok(['found' => false]);
    }
    if (($record['state'] ?? '') === 'running' && is_string($record['subscription'] ?? null)
        && !$store->isRunning($record['subscription'])) {
        $record = RestoreRecord::failed($record, RestoreRecord::STOPPED, time());
    }
    ScriptIO::ok(['found' => true, 'record' => $record]);
} catch (Throwable $e) {
    ScriptIO::fail('restore-result: ' . $e->getMessage());
}

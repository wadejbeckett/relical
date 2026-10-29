<?php

declare(strict_types=1);

/**
 * What the restore screens check before starting a restore:
 *
 *     plesk bin extension --exec relical restore-running.php <subscription>
 *
 * Prints {"running": bool, "server_busy": bool, "restored_folders": int} or
 * {"error": ...}: whether a restore of the subscription runs, whether every
 * restore slot of the server is taken (both read from /proc/locks, so
 * asking takes no lock; restore-run.php's own locks are what refuse), and
 * how many `restored-*` copies the home directory holds (the core keeps
 * homes at /var/www/vhosts/<subscription>).
 */

use Noiz\Relical\Command;
use Noiz\Relical\Paths;
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
    $args = Command::scriptArgs('restore-running.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('restore-running.php');

try {
    $store = RestoreStore::fromPlesk();
    ScriptIO::ok([
        'running' => $store->isRunning($args[0]),
        'server_busy' => $store->serverBusy(),
        'restored_folders' => RestoreStore::restoredFolders(Paths::FILES_ROOT . '/' . $args[0]),
    ]);
} catch (Throwable $e) {
    ScriptIO::fail('restore-running: ' . $e->getMessage());
}

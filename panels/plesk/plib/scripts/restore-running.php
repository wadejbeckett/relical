<?php

declare(strict_types=1);

/**
 * Says whether a customer restore of the subscription is running, so the
 * restore screens can say so before starting another:
 *
 *     plesk bin extension --exec relical restore-running.php <subscription>
 *
 * It tries the subscription's restore lock and lets it go at once.
 * restore-run.php takes the same lock, so the answer here is a courtesy;
 * the lock itself is what refuses a second restore.
 *
 * Prints {"running": bool} or {"error": ...}.
 */

use Noiz\Relical\Command;
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
    ScriptIO::ok(['running' => RestoreStore::fromPlesk()->isRunning($args[0])]);
} catch (Throwable $e) {
    ScriptIO::fail('restore-running: ' . $e->getMessage());
}

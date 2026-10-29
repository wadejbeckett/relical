<?php

declare(strict_types=1);

/**
 * Says whether a relical run (timer or "Back up now") holds the run lock
 * /var/lib/relical/relical.lock, so the screens can say so instead of
 * starting a run that would stop at once:
 *
 *     plesk bin extension --exec relical running.php
 *
 * It reads /proc/locks and takes no lock itself, so asking never stops a
 * run that is just starting.
 *
 * Takes no arguments. Prints {"running": bool} or {"error": ...}.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Paths;
use Noiz\Relical\ScriptIO;
use Noiz\Relical\Status;

// Plesk SDK and module context: see the note in cli.php.
if (class_exists('pm_Loader')) {
    pm_Loader::registerAutoload();
}
if (class_exists('pm_Context')) {
    pm_Context::init('relical');
}

require_once __DIR__ . '/../library/autoload.php';

try {
    Command::scriptArgs('running.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('running.php');

try {
    clearstatcache(true, Paths::LOCK);
    $stat = is_file(Paths::LOCK) ? @stat(Paths::LOCK) : false;
    if ($stat === false) {
        ScriptIO::ok(['running' => false]);
    }
    $locks = @file_get_contents('/proc/locks');
    if ($locks === false) {
        throw new RuntimeException('could not read /proc/locks');
    }
    ScriptIO::ok(['running' => Status::lockHeld($locks, (int) $stat['ino'])]);
} catch (Throwable $e) {
    ScriptIO::fail('running: ' . $e->getMessage());
}

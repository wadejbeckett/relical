<?php

declare(strict_types=1);

/**
 * Prints the last 200 lines of the core's log, /var/lib/relical/relical.log
 * (root-only, so the log screen reads it through this script):
 *
 *     plesk bin extension --exec relical log-tail.php
 *
 * Takes no arguments. Prints {"exists": bool, "lines": [...]} (no log yet
 * is not an error) or {"error": ...}.
 */

use Noiz\Relical\Command;
use Noiz\Relical\LogTail;
use Noiz\Relical\Paths;
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
    Command::scriptArgs('log-tail.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('log-tail.php');

try {
    $lines = LogTail::read(Paths::LOG);
    ScriptIO::ok(['exists' => $lines !== null, 'path' => Paths::LOG, 'lines' => $lines ?? []]);
} catch (Throwable $e) {
    ScriptIO::fail('log-tail: ' . $e->getMessage());
}

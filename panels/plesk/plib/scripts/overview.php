<?php

declare(strict_types=1);

/**
 * The settings page's read-only data in one call:
 *
 *     plesk bin extension --exec relical overview.php
 *
 * Prints {"public_key": {"key", "error"}, "status": {...}, "report": {...}}
 * where status and report are the core's answer to `relical status` and
 * `relical report` ({code, stdout, stderr, timed_out}), each stopped after
 * Overview::CORE_TIMEOUT seconds. Takes no arguments.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Overview;
use Noiz\Relical\Paths;
use Noiz\Relical\Process;
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
    Command::scriptArgs('overview.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('overview.php');

$out = ['public_key' => Overview::publicKey(Paths::SSH_KEY . '.pub')];
foreach (['status', 'report'] as $command) {
    try {
        $out[$command] = Overview::call(Process::capture(Command::parse([$command])->argv(), Overview::CORE_TIMEOUT));
    } catch (Throwable $e) {
        $out[$command] = ['code' => -1, 'stdout' => '', 'stderr' => $e->getMessage(), 'timed_out' => false];
    }
}
ScriptIO::ok($out);

<?php

declare(strict_types=1);

/**
 * "Run everything now" on the settings page:
 *
 *     plesk bin extension --exec relical run-all.php
 *
 * Starts relical-backup.service (`systemctl start --no-block`), so the run
 * is the nightly one in every respect: idle priority, retention, its own
 * cgroup, OnFailure= and the 20-hour bound. Without systemd (no
 * /run/systemd/system) it starts the core's `backup --all` detached
 * instead. Takes no arguments. Prints {"started": "systemd"|"detached"}.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Installer;
use Noiz\Relical\LocalProbe;
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
    Command::scriptArgs('run-all.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('run-all.php');

try {
    $systemctl = null;
    if (is_dir('/run/systemd/system')) {
        try {
            $systemctl = Installer::findTool(new LocalProbe(), Paths::SYSTEMCTL);
        } catch (Throwable $e) {
            $systemctl = null;
        }
    }
    $plan = Command::runAll($systemctl);
    if ($plan['mode'] === 'systemd') {
        $result = Process::capture($plan['argv'], 30.0);
        if ($result['code'] !== 0 || $result['timed_out']) {
            $lines = array_values(array_filter(array_map('trim', explode("\n", $result['stderr'])), 'strlen'));
            throw new RuntimeException('systemctl start relical-backup.service: ' . ($lines !== [] ? end($lines) : 'exit ' . $result['code']));
        }
    } else {
        $output = [];
        $code = -1;
        if (@exec(Command::background($plan['argv']), $output, $code) === false || $code !== 0) {
            throw new RuntimeException('could not start the backup in the background');
        }
    }
    ScriptIO::ok(['started' => $plan['mode']]);
} catch (Throwable $e) {
    ScriptIO::fail('run-all: ' . $e->getMessage());
}

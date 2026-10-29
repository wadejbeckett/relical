<?php

declare(strict_types=1);

/**
 * Re-renders the systemd units from the saved backup time and installs the
 * ones that changed (the same steps as post-install's last part): writes
 * them, reloads systemd, enables both timers and restarts a timer whose
 * schedule changed. The settings screen runs it on every save (a run with
 * nothing changed only reloads systemd and enables the timers):
 *
 *     plesk bin extension --exec relical install-timers.php
 *
 * Takes no arguments. Prints {"ok": true, "steps": [...]} or {"error": ...}.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Installer;
use Noiz\Relical\InstallRunner;
use Noiz\Relical\LocalProbe;
use Noiz\Relical\Paths;
use Noiz\Relical\ScriptIO;
use Noiz\Relical\Settings;
use Noiz\Relical\Units;

// Plesk SDK and module context: see the note in cli.php.
if (class_exists('pm_Loader')) {
    pm_Loader::registerAutoload();
}
if (class_exists('pm_Context')) {
    pm_Context::init('relical');
}

require_once __DIR__ . '/../library/autoload.php';

try {
    Command::scriptArgs('install-timers.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('install-timers.php');

try {
    $probe = new LocalProbe();
    $installer = new Installer(
        __DIR__ . '/../bin',
        $probe,
        Installer::findTool($probe, Paths::SYSTEMCTL),
        Installer::findTool($probe, Paths::SSH_KEYGEN)
    );
    $units = Units::renderAll(Units::loadTemplates(__DIR__ . '/../resources/systemd'), Settings::fromPlesk()->backupTime(), Paths::rootScript());

    $log = fopen('php://memory', 'w+');
    (new InstallRunner($log, STDERR, ''))->run($installer->unitActions($units));
    rewind($log);
    $steps = array_values(array_filter(explode("\n", (string) stream_get_contents($log)), 'strlen'));

    ScriptIO::ok(['ok' => true, 'steps' => $steps]);
} catch (Throwable $e) {
    ScriptIO::fail('install-timers: ' . $e->getMessage());
}

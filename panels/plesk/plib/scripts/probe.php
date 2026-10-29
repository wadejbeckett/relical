<?php

declare(strict_types=1);

/**
 * Tests the connection to the Storage Box the way restic will use it: an
 * sftp session with the extension's key and only the pinned host key, which
 * asks for the current directory and changes nothing.
 *
 *     plesk bin extension --exec relical probe.php
 *
 * The destination comes from the saved settings. sftp runs from an argv
 * array (no shell) and is killed after 20 seconds.
 *
 * Takes no arguments. Prints {"ok": true|false, "message": "..."}, or
 * {"error": ...} when the probe could not be run at all.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Destination;
use Noiz\Relical\Installer;
use Noiz\Relical\LocalProbe;
use Noiz\Relical\Paths;
use Noiz\Relical\Process;
use Noiz\Relical\ScriptIO;
use Noiz\Relical\Settings;

// Plesk SDK and module context: see the note in cli.php.
if (class_exists('pm_Loader')) {
    pm_Loader::registerAutoload();
}
if (class_exists('pm_Context')) {
    pm_Context::init('relical');
}

require_once __DIR__ . '/../library/autoload.php';

try {
    Command::scriptArgs('probe.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('probe.php');

const RELICAL_PROBE_TIMEOUT = 20;

try {
    $destination = Destination::fromInput(Settings::fromPlesk()->configInput());
    if (!is_file(Paths::KNOWN_HOSTS)) {
        ScriptIO::ok(['ok' => false, 'message' => 'No host key is pinned yet: check the connection and trust the host key first.']);
    }
    if (!is_file(Paths::SSH_KEY)) {
        ScriptIO::ok(['ok' => false, 'message' => sprintf('%s is missing; run the installer again.', Paths::SSH_KEY)]);
    }
    $sftp = Installer::findTool(new LocalProbe(), Paths::SFTP);
    $result = Process::capture($destination->sftpProbeArgv($sftp, 10), RELICAL_PROBE_TIMEOUT, Destination::probeCommands());

    if ($result['timed_out']) {
        ScriptIO::ok(['ok' => false, 'message' => sprintf('No answer from %s within %d seconds.', $destination->knownHostsName(), RELICAL_PROBE_TIMEOUT)]);
    }
    if ($result['code'] === 0) {
        ScriptIO::ok(['ok' => true, 'message' => sprintf('Connected to %s with the extension key and the pinned host key.', $destination->display())]);
    }
    // sftp's own words (authentication refused, host key changed, ...), last lines only.
    $lines = array_slice(array_values(array_filter(array_map('trim', explode("\n", $result['stderr'])), 'strlen')), -3);
    $detail = substr((string) preg_replace('/[^\x20-\x7e]/', '?', implode(' ', $lines)), 0, 500);
    ScriptIO::ok(['ok' => false, 'message' => sprintf('The connection to %s failed (exit %d)%s', $destination->display(), $result['code'], $detail !== '' ? ': ' . $detail : '.')]);
} catch (Throwable $e) {
    ScriptIO::fail('probe: ' . $e->getMessage());
}

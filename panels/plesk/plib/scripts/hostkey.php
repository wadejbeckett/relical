<?php

declare(strict_types=1);

/**
 * Reads and pins the Storage Box's SSH host key.
 *
 *     plesk bin extension --exec relical hostkey.php scan
 *     plesk bin extension --exec relical hostkey.php trust SHA256:<fingerprint>
 *
 * The host and port come from the saved settings, never from the arguments.
 * `scan` runs `ssh-keyscan -p <port> -t ed25519 <host>` (argv, no shell,
 * killed after 15 seconds) and prints the key's fingerprint. `trust` scans
 * again and writes /etc/relical/ssh/known_hosts (0600, atomically) only if
 * the key the host offers now has the fingerprint the administrator
 * confirmed; otherwise nothing is written.
 *
 * Prints {"host", "port", "fingerprint", "pinned"} for scan,
 * {"ok": true, "fingerprint"} for trust, or {"error": ...}.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Destination;
use Noiz\Relical\HostKey;
use Noiz\Relical\Installer;
use Noiz\Relical\LocalProbe;
use Noiz\Relical\Paths;
use Noiz\Relical\Process;
use Noiz\Relical\RootFile;
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
    $args = Command::scriptArgs('hostkey.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('hostkey.php');

const RELICAL_KEYSCAN_TIMEOUT = 15;

/** ssh-keyscan's output for the destination. */
function relical_scan(Destination $destination): string
{
    $keyscan = Installer::findTool(new LocalProbe(), Paths::SSH_KEYSCAN);
    $result = Process::capture($destination->keyscanArgv($keyscan, 10), RELICAL_KEYSCAN_TIMEOUT);
    if ($result['timed_out']) {
        throw new RuntimeException(sprintf('%s did not answer within %d seconds', $destination->knownHostsName(), RELICAL_KEYSCAN_TIMEOUT));
    }

    return $result['stdout'];
}

try {
    $destination = Destination::fromInput(Settings::fromPlesk()->configInput());
    $scan = relical_scan($destination);

    if ($args[0] === 'scan') {
        $key = HostKey::fromKeyscan($scan, $destination);
        $line = $key->knownHostsLine($destination);
        $pinned = !is_link(Paths::KNOWN_HOSTS) && is_file(Paths::KNOWN_HOSTS) && @file_get_contents(Paths::KNOWN_HOSTS) === $line;
        ScriptIO::ok([
            'host' => $destination->host,
            'port' => $destination->port,
            'fingerprint' => $key->fingerprint(),
            'pinned' => $pinned,
        ]);
    }

    // Throws, and nothing is written, unless the fresh scan has the confirmed key.
    $line = HostKey::trustedLine($scan, $destination, $args[1]);
    RootFile::ensureDir(Paths::SSH_DIR, 0700);
    RootFile::write(Paths::KNOWN_HOSTS, $line, 0600);
    ScriptIO::ok(['ok' => true, 'fingerprint' => $args[1]]);
} catch (Throwable $e) {
    ScriptIO::fail('hostkey: ' . $e->getMessage());
}

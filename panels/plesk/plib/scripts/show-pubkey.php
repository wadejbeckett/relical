<?php

declare(strict_types=1);

/**
 * Prints the extension's Storage Box public key, for the administrator to
 * add to the Storage Box's authorized_keys:
 *
 *     plesk bin extension --exec relical show-pubkey.php
 *
 * It reads only /etc/relical/ssh/id_ed25519.pub and prints it only if the
 * file is exactly one ed25519 public key, so nothing else under /etc/relical
 * (the private key above all) can reach the screen through it.
 *
 * Takes no arguments. Prints {"public_key": "ssh-ed25519 ..."} or {"error": ...}.
 */

use Noiz\Relical\Command;
use Noiz\Relical\HostKey;
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
    Command::scriptArgs('show-pubkey.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('show-pubkey.php');

$path = Paths::SSH_KEY . '.pub';
try {
    if (is_link($path) || !is_file($path)) {
        throw new RuntimeException(sprintf('%s is missing; run the installer again (plesk bin extension --exec relical post-install.php)', $path));
    }
    $content = @file_get_contents($path, false, null, 0, 4096);
    if ($content === false) {
        throw new RuntimeException(sprintf('could not read %s', $path));
    }
    ScriptIO::ok(['public_key' => HostKey::publicKeyLine($content)]);
} catch (Throwable $e) {
    ScriptIO::fail('show-pubkey: ' . $e->getMessage());
}

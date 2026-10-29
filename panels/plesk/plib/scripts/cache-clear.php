<?php

declare(strict_types=1);

/**
 * Forgets the restore screens' cached answers about restore points and
 * mailboxes (restore-read.php), for one subscription or for all:
 *
 *     plesk bin extension --exec relical cache-clear.php [<subscription>]
 *
 * Not needed in normal use: the cache expires after
 * RestoreStore::CACHE_TTL seconds, and restore-run.php clears a
 * subscription's entries after each restore. For an administrator who
 * wants a fresh list at once (for example right after "Back up now").
 *
 * Prints {"removed": n} or {"error": ...}.
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
    $args = Command::scriptArgs('cache-clear.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('cache-clear.php');

try {
    ScriptIO::ok(['removed' => RestoreStore::fromPlesk()->clearCache($args[0] ?? null)]);
} catch (Throwable $e) {
    ScriptIO::fail('cache-clear: ' . $e->getMessage());
}

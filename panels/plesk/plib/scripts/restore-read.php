<?php

declare(strict_types=1);

/**
 * Asks the core about a subscription's backups for the customer screens:
 *
 *     plesk bin extension --exec relical restore-read.php snapshots <subscription>
 *     plesk bin extension --exec relical restore-read.php mailboxes <subscription>
 *     plesk bin extension --exec relical restore-read.php ls <subscription> <snapshot> [<path>]
 *
 * Each read of the Storage Box opens an SFTP session as root, and a page
 * reloaded in a loop must not use up the Storage Box's connections. So the
 * answers to `snapshots`, `mailboxes` and each `ls` (per restore point and
 * folder) are kept for RestoreStore::CACHE_TTL seconds (root-only, in the
 * restores directory; restore-run.php and cache-clear.php forget them
 * early), only one read per subscription runs at a time (another request
 * meanwhile gets {"error": RestoreRecord::READ_BUSY} at once instead of a
 * second session), and at most RestoreStore::READ_SLOTS reads run on the
 * whole server (beyond that: RestoreRecord::READ_SERVER_BUSY).
 *
 * Prints the core's JSON as `cli.php` would, with its exit code.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Process;
use Noiz\Relical\RestoreRecord;
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

/** Below the screens' wait for a root script (60 s). */
const READ_TIMEOUT = 50.0;

try {
    $args = Command::scriptArgs('restore-read.php', array_slice($argv, 1));
    $cli = Command::readCliArgs($args);
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('restore-read.php');

[$kind, $subscription] = [$args[0], $args[1]];
$cacheable = in_array($kind, RestoreStore::CACHED, true);
// ls: the restore point and the folder ("" for the top), NUL-separated.
$cacheKey = $kind === 'ls' ? implode("\0", array_slice($args, 2)) : '';

try {
    $store = RestoreStore::fromPlesk();
    $store->ensure();
    if ($cacheable) {
        $cached = $store->cached($subscription, $kind, time(), $cacheKey);
        if ($cached !== null) {
            echo $cached;
            exit(0);
        }
    }
    $lock = $store->readLock($subscription);
    if ($lock === null) {
        ScriptIO::fail(RestoreRecord::READ_BUSY);
    }
    $slot = null;
    try {
        $slot = $store->readSlot();
        if ($slot === null) {
            ScriptIO::fail(RestoreRecord::READ_SERVER_BUSY);
        }
        $result = Process::capture(Command::parse($cli)->argv(), READ_TIMEOUT);
        if ($result['code'] === 0 && !$result['timed_out'] && $cacheable) {
            $store->cache($subscription, $kind, $result['stdout'], time(), $cacheKey);
        }
    } finally {
        if ($slot !== null) {
            RestoreStore::unlock($slot);
        }
        RestoreStore::unlock($lock);
    }
} catch (Throwable $e) {
    ScriptIO::fail('restore-read: ' . $e->getMessage());
}

if ($result['timed_out']) {
    ScriptIO::fail('restore-read: the core did not answer within ' . (int) READ_TIMEOUT . ' seconds');
}
fwrite(STDERR, $result['stderr']);
echo $result['stdout'];
exit($result['code']);

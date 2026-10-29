<?php

declare(strict_types=1);

/**
 * Runs one customer restore and records its outcome:
 *
 *     plesk bin extension --exec relical restore-run.php <id> [--admin] restore --subscription S --unit mail|files|db ...
 *
 * The restore screens start it detached (Command::detachedScript). It takes
 * the subscription's restore lock (so a second restore of the same
 * subscription is refused while this one runs) and one of the server's
 * RestoreStore::SLOTS restore slots (so only that many run at once), writes a "running" record
 * to <var dir>/restores/<id>.json (0600), runs the core's restore with the
 * same argv cli.php would build (Command::parse, no shell), and replaces
 * the record with the core's JSON or its error. The record is the only
 * place the outcome lands; restore-result.php reads it.
 *
 * A subscription gets at most RestoreRecord::MAX_RESTORES_PER_DAY files or
 * database restores in 24 hours, counted from its records here, so a
 * customer cannot fill the disk by restoring and renaming copies over and
 * over. `--admin` (the screens add it for an administrator) is exempt.
 *
 * The arguments are a customer restore only (Command::customerRestore):
 * never `_server`, never the panel configuration.
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

/** A restore of a large webspace can take hours; past this it is stopped and recorded as failed. */
const RESTORE_TIMEOUT = 12 * 3600.0;

try {
    $args = Command::scriptArgs('restore-run.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}
ScriptIO::requireRoot('restore-run.php');

$id = $args[0];
$admin = $args[1] === Command::ADMIN_FLAG;
$cliArgs = array_slice($args, $admin ? 2 : 1);

try {
    $store = RestoreStore::fromPlesk();
    $store->ensure();
    $store->prune(time());
    $record = RestoreRecord::running($id, $cliArgs, time(), $admin);
    if ($store->read($id) !== null) {
        ScriptIO::fail('restore-run: a restore with this id exists already');
    }

    $lock = $store->lock($record['subscription']);
    if ($lock === null) {
        $store->write(RestoreRecord::failed($record, RestoreRecord::BUSY, time()));
        ScriptIO::fail(RestoreRecord::BUSY);
    }
    if (!$admin && $record['unit'] !== 'mail'
        && RestoreRecord::recentCustomerRestores($store->records(), $record['subscription'], time()) >= RestoreRecord::MAX_RESTORES_PER_DAY) {
        RestoreStore::unlock($lock);
        $store->write(RestoreRecord::failed($record, RestoreRecord::TOO_MANY_RESTORES, time()));
        ScriptIO::fail(RestoreRecord::TOO_MANY_RESTORES);
    }
    // At most RestoreStore::SLOTS customer restores run on the server at once.
    $slot = $store->slot();
    if ($slot === null) {
        RestoreStore::unlock($lock);
        $store->write(RestoreRecord::failed($record, RestoreRecord::SERVER_BUSY, time()));
        ScriptIO::fail(RestoreRecord::SERVER_BUSY);
    }
    try {
        $store->write($record);
        try {
            $result = Process::capture(Command::parse($cliArgs)->argv(), RESTORE_TIMEOUT);
            $record = RestoreRecord::finished($record, $result['code'], $result['stdout'], $result['stderr'], $result['timed_out'], time());
        } catch (Throwable $e) {
            $record = RestoreRecord::failed($record, $e->getMessage(), time());
        }
        $store->write($record);
        // Mail restores add folders; anything cached about the subscription is stale.
        $store->clearCache($record['subscription']);
    } finally {
        RestoreStore::unlock($slot);
        RestoreStore::unlock($lock);
    }
} catch (Throwable $e) {
    ScriptIO::fail('restore-run: ' . $e->getMessage());
}

if ($record['state'] !== 'done') {
    ScriptIO::fail((string) $record['error']);
}
ScriptIO::ok($record);

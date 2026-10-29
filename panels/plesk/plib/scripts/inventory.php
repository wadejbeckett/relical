<?php

declare(strict_types=1);

/**
 * Prints the inventory the core reads (its `inventory_command`).
 *
 * The core runs this as root through sbin/relical-root
 * (`relical-root inventory.php`, Paths::inventoryCommand) and parses
 * stdout; `plesk bin extension --exec relical inventory.php` hands itself
 * over to relical-root. On success stdout is the inventory JSON and the exit code is 0; on
 * any failure stdout is `{"error": "..."}`, the same message goes to stderr
 * and the exit code is 1, so the core never backs up from a partial list.
 */

use Noiz\Relical\Command;
use Noiz\Relical\ConfigFiles;
use Noiz\Relical\Coverage;
use Noiz\Relical\Inventory;
use Noiz\Relical\Paths;
use Noiz\Relical\PleskSource;
use Noiz\Relical\RootFile;
use Noiz\Relical\ScriptIO;
use Noiz\Relical\Settings;

// Plesk runs every plib/scripts/* file (install hooks, `plesk bin extension
// --exec`, scheduled tasks) with its SDK prepended (`php
// -dauto_prepend_file=sdk.php`), so the pm_* classes autoload. Neither the
// Extensions guide nor pm-api-stubs says whether the module context is set
// for these scripts. Cloudflare DNS Sync's post-install reads pm_Settings and
// pm_Context::getVarDir() without calling init() and works on Plesk 18.0.81,
// so Plesk sets it at least for install hooks. init() with the extension's
// own id only names the module, so it is called here in every script to make
// pm_Settings safe either way; the class_exists() guards keep the script
// loadable without the SDK (php -l, tests). The Docker Plesk test confirms
// both points (tests/docker/TEST-PLAN.md).
if (class_exists('pm_Loader')) {
    pm_Loader::registerAutoload();
}
if (class_exists('pm_Context')) {
    pm_Context::init('relical');
}

require_once __DIR__ . '/../library/autoload.php';

try {
    Command::hookArgs('inventory.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}

ScriptIO::requireRoot('inventory.php');

try {
    $scope = Settings::fromPlesk()->scope();
    if (!Coverage::isValidScope($scope)) {
        throw new RuntimeException(sprintf('the scope setting "%s" is not valid', $scope));
    }

    $source = new PleskSource();
    $webspaces = $source->webspaces($scope);

    // The rename hints are a convenience: a missing, unreadable or corrupt
    // hints file means no hints this run, never a failed inventory.
    $known = [];
    try {
        $raw = is_file(Paths::KNOWN_NAMES) ? @file_get_contents(Paths::KNOWN_NAMES) : false;
        $known = is_string($raw) ? json_decode($raw, true) : [];
    } catch (Throwable $e) {
        $known = [];
    }
    [$webspaces, $nextKnown] = Inventory::trackRenames($known, $webspaces);

    $inventory = Inventory::build($webspaces, $scope);
    $inventory['skipped'] = array_merge($inventory['skipped'], $source->notes());

    // Plesk's MySQL admin password can change (a repair, --set-mysql-password);
    // the option file follows it here, every run, not only on a settings save.
    try {
        ConfigFiles::refreshMysqlOptionFile();
    } catch (Throwable $e) {
        $inventory['skipped'][] = Inventory::skip('', 'MySQL option file: ' . $e->getMessage());
    }

    try {
        RootFile::ensureDir(Paths::STATE_DIR);
        RootFile::write(Paths::KNOWN_NAMES, json_encode($nextKnown, JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR) . "\n");
    } catch (Throwable $e) {
        $inventory['skipped'][] = Inventory::skip('', 'rename tracking: ' . $e->getMessage());
    }

    echo json_encode($inventory, JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR), "\n";
    exit(0);
} catch (Throwable $e) {
    // The core reports a failed inventory_command's stderr; stdout carries
    // the same message as JSON for anything that reads it.
    $message = 'inventory: ' . $e->getMessage();
    fwrite(STDERR, $message . "\n");
    echo json_encode(['error' => $message], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
    exit(1);
}

<?php

declare(strict_types=1);

/**
 * Prints the inventory the core reads (its `inventory_command`).
 *
 * The core runs this as root through
 * `/usr/sbin/plesk bin extension --exec relical inventory.php` and parses
 * stdout. On success stdout is the inventory JSON and the exit code is 0; on
 * any failure stdout is `{"error": "..."}`, the same message goes to stderr
 * and the exit code is 1, so the core never backs up from a partial list.
 */

use Noiz\Relical\Coverage;
use Noiz\Relical\Inventory;
use Noiz\Relical\Paths;
use Noiz\Relical\PleskSource;
use Noiz\Relical\RootFile;
use Noiz\Relical\Settings;

pm_Loader::registerAutoload();
pm_Context::init('relical');

require_once __DIR__ . '/../library/autoload.php';

try {
    $scope = Settings::fromPlesk()->scope();
    if (!Coverage::isValidScope($scope)) {
        throw new RuntimeException(sprintf('the scope setting "%s" is not valid', $scope));
    }

    $source = new PleskSource();
    $webspaces = $source->webspaces();

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

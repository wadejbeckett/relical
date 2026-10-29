<?php

declare(strict_types=1);

/**
 * The extension's only way to run the core binary.
 *
 * Run as root through `plesk bin extension --exec relical cli.php <command> [flags]`.
 * Relical\Command accepts only the commands and flags of the CLI contract
 * and validates every value; the core then runs from an argv array (no
 * shell), its JSON goes straight to stdout and its exit code is this
 * script's exit code. A refused command prints `{"error": "..."}` and exits 2.
 */

use Noiz\Relical\Command;
use Noiz\Relical\Process;

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
    $command = Command::parse(array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    fwrite(STDERR, 'refused: ' . $e->getMessage() . "\n");
    echo json_encode(['error' => 'refused: ' . $e->getMessage()], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
    exit(2);
}

try {
    exit(Process::run($command->argv(), STDOUT, STDERR));
} catch (Throwable $e) {
    fwrite(STDERR, $e->getMessage() . "\n");
    echo json_encode(['error' => $e->getMessage()], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
    exit(1);
}

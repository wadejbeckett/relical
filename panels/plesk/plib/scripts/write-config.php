<?php

declare(strict_types=1);

/**
 * Writes the core's configuration from the extension's settings.
 *
 * Run as root through `plesk bin extension --exec relical write-config.php`
 * (by post-install and whenever the settings are saved). It writes
 * /etc/relical/config.json and /etc/relical/mysql.cnf, both 0600, each
 * atomically. The MySQL option file carries Plesk's database admin password
 * from /etc/psa/.psa.shadow; the password is never printed.
 *
 * Takes no arguments. Exit 0 with `{"ok": true, ...}`, exit 1 with
 * `{"error": "..."}` (the message also goes to stderr), or exit 2 when
 * arguments were given.
 */

use Noiz\Relical\Command;
use Noiz\Relical\ConfigFiles;
use Noiz\Relical\Paths;
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
    Command::scriptArgs('write-config.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}

try {
    ConfigFiles::write(Settings::fromPlesk()->configInput(), (string) gethostname());

    echo json_encode(['ok' => true, 'config' => Paths::CONFIG, 'mysql_defaults_file' => Paths::MYSQL_CNF], JSON_UNESCAPED_SLASHES), "\n";
    exit(0);
} catch (Throwable $e) {
    // Messages come from ConfigFiles/ConfigWriter/RootFile and name paths, never values
    // read from the shadow file.
    $message = 'write-config: ' . $e->getMessage();
    fwrite(STDERR, $message . "\n");
    echo json_encode(['error' => $message], JSON_UNESCAPED_SLASHES | JSON_INVALID_UTF8_SUBSTITUTE), "\n";
    exit(1);
}

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
 * Exit 0 with `{"ok": true, ...}`, or exit 1 with `{"error": "..."}` (the
 * message also goes to stderr).
 */

use Noiz\Relical\ConfigFiles;
use Noiz\Relical\Paths;
use Noiz\Relical\Settings;

pm_Loader::registerAutoload();
pm_Context::init('relical');

require_once __DIR__ . '/../library/autoload.php';

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

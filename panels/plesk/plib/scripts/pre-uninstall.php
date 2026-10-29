<?php

declare(strict_types=1);

/**
 * Removes Relical's timers, units and binaries. Plesk runs it as root before
 * removing the extension.
 *
 * It keeps /etc/relical (configuration, the Storage Box key, the pinned host
 * key and the repository keys) and /var/lib/relical (state and log), so a
 * reinstall carries on with the same repositories and the backups stay
 * readable. It deletes only what the installer put in /usr/local/lib/relical
 * (the two binaries and its own staging and temporary copies); if anything
 * else is in that directory, the directory is kept and the names are printed
 * as a note, without failing the uninstall. Every step runs even when an
 * earlier one fails; exit code 1 means something Relical installed could not
 * be removed and is named on stderr (Plesk then stops the removal).
 */

use Noiz\Relical\Command;
use Noiz\Relical\InstallRunner;
use Noiz\Relical\Installer;
use Noiz\Relical\LocalProbe;
use Noiz\Relical\Paths;
use Noiz\Relical\ScriptIO;

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
    Command::hookArgs('pre-uninstall.php', array_slice($argv, 1));
} catch (InvalidArgumentException $e) {
    ScriptIO::fail('refused: ' . $e->getMessage(), 2);
}

// Plesk runs the install hooks as its panel user (psaadm), not root, so the
// work is handed to sbin/relical-root through Plesk's mod_wrapper, with the
// same arguments; its output is this script's output.
if (function_exists('posix_geteuid') && posix_geteuid() !== 0) {
    exit(ScriptIO::runAsRoot('pre-uninstall.php', array_values(array_map('strval', array_slice($argv, 1)))));
}

try {

    $probe = new LocalProbe();
    $installer = new Installer(
        __DIR__ . '/../bin',
        $probe,
        Installer::findTool($probe, ['/usr/bin/systemctl', '/bin/systemctl']),
        '/usr/bin/ssh-keygen' // not used on uninstall
    );

    $runner = new InstallRunner(STDOUT, STDERR);
    $runner->run($installer->uninstall());

    fwrite(STDERR, sprintf(
        "Relical: WARNING: the repository keys in %s were left in place. Without them the backups cannot be read: keep them, with a copy away from this server, for as long as the backups matter.\n",
        Paths::KEYS_DIR
    ));

    if ($runner->warnings() !== []) {
        fwrite(STDERR, 'Relical: some steps failed; see the warnings above' . "\n");
        exit(1);
    }
    exit(0);
} catch (Throwable $e) {
    fwrite(STDERR, 'Relical: uninstall failed: ' . $e->getMessage() . "\n");
    echo 'Relical: uninstall failed: ' . $e->getMessage() . "\n";
    exit(1);
}

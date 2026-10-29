<?php

declare(strict_types=1);

/**
 * Installs Relical on this server. Plesk runs it as root after installing
 * the extension, and again with `upgrade <previous version>` after an
 * upgrade.
 *
 * 1. Checks plib/bin/relical and plib/bin/restic against plib/bin/SHA256SUMS
 *    and refuses to go on if either is missing or differs.
 * 2. Checks that the saved settings render a valid configuration.
 *    Nothing is changed before steps 1 and 2 pass.
 * 3. Copies both binaries into /usr/local/lib/relical/ under staging names
 *    (.relical.new, .restic.new), checks each copy, and only then renames
 *    both into place (0755, root).
 * 4. Creates /etc/relical, /etc/relical/keys, /etc/relical/ssh and
 *    /var/lib/relical (0700).
 * 5. Generates the Storage Box key /etc/relical/ssh/id_ed25519 if there is
 *    none, under a staging name renamed into place. An existing key,
 *    known_hosts and the repository keys are never touched.
 * 6. Writes /etc/relical/mysql.cnf and, once the destination is set,
 *    /etc/relical/config.json (as write-config.php does).
 * 7. Installs the backup and check units into /etc/systemd/system/, reloads
 *    systemd and enables both timers.
 *
 * Every step is idempotent and printed. Any failure stops the run with exit
 * code 1 and a message on stderr naming the failed step, the steps already
 * completed, the steps not done, and the command to run it all again.
 */

use Noiz\Relical\InstallRunner;
use Noiz\Relical\Installer;
use Noiz\Relical\LocalProbe;
use Noiz\Relical\Settings;
use Noiz\Relical\Units;

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

// Plesk passes `upgrade <previous-version>` on an upgrade; older builds pass
// nothing. Install and upgrade run the same steps.
$isUpgrade = isset($argv[1]) && (string) $argv[1] === 'upgrade';
$previousVersion = $isUpgrade && isset($argv[2]) ? trim((string) $argv[2]) : '';

try {
    if (function_exists('posix_geteuid') && posix_geteuid() !== 0) {
        throw new RuntimeException('post-install must run as root');
    }
    if ($isUpgrade) {
        echo 'Relical: upgrading' . ($previousVersion !== '' ? ' from ' . $previousVersion : '') . "\n";
    }

    $probe = new LocalProbe();
    $installer = new Installer(
        __DIR__ . '/../bin',
        $probe,
        Installer::findTool($probe, ['/usr/bin/systemctl', '/bin/systemctl']),
        Installer::findTool($probe, ['/usr/bin/ssh-keygen', '/bin/ssh-keygen'])
    );

    $settings = Settings::fromPlesk();
    $units = Units::renderAll(Units::loadTemplates(__DIR__ . '/../resources/systemd'), $settings->backupTime());

    // Plans every step, verifies both binaries and renders the configuration
    // before changing anything.
    $plan = $installer->install($isUpgrade, $units, $settings->configInput(), (string) gethostname());

    (new InstallRunner(STDOUT, STDERR))->run(
        $plan,
        'Every step is safe to repeat: fix the cause and run `plesk bin extension --exec relical post-install.php` again.'
    );
    echo "Relical: done\n";
    exit(0);
} catch (Throwable $e) {
    fwrite(STDERR, 'Relical: installation failed: ' . $e->getMessage() . "\n");
    echo 'Relical: installation failed: ' . $e->getMessage() . "\n";
    exit(1);
}

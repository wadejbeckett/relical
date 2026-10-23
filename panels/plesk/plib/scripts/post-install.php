<?php

declare(strict_types=1);

/**
 * Installs Relical on this server. Plesk runs it as root after installing
 * the extension, and again with `upgrade <previous version>` after an
 * upgrade.
 *
 * 1. Checks plib/bin/relical and plib/bin/restic against plib/bin/SHA256SUMS
 *    and refuses to go on if either is missing or differs.
 * 2. Installs both into /usr/local/lib/relical/ (0755, root), each through a
 *    temporary copy renamed into place.
 * 3. Creates /etc/relical, /etc/relical/keys, /etc/relical/ssh and
 *    /var/lib/relical (0700).
 * 4. Generates the Storage Box key /etc/relical/ssh/id_ed25519 if there is
 *    none. An existing key, known_hosts and the repository keys are never
 *    touched.
 * 5. Writes /etc/relical/mysql.cnf and, once the destination is set,
 *    /etc/relical/config.json (as write-config.php does).
 * 6. Installs the backup and check units into /etc/systemd/system/, reloads
 *    systemd and enables both timers.
 *
 * Every step is idempotent and printed. Any failure stops the run with a
 * message on stderr and exit code 1.
 */

use Noiz\Relical\InstallRunner;
use Noiz\Relical\Installer;
use Noiz\Relical\LocalProbe;
use Noiz\Relical\Settings;
use Noiz\Relical\Units;

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

    // Plans every step (and verifies both binaries) before changing anything.
    $plan = $installer->install($isUpgrade, $units, $settings->configInput(), (string) gethostname());

    (new InstallRunner(STDOUT, STDERR))->run($plan);
    echo "Relical: done\n";
    exit(0);
} catch (Throwable $e) {
    fwrite(STDERR, 'Relical: installation failed: ' . $e->getMessage() . "\n");
    echo 'Relical: installation failed: ' . $e->getMessage() . "\n";
    exit(1);
}

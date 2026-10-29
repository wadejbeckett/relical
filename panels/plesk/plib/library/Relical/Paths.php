<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * Every fixed path and command the extension uses, in one place.
 *
 * None of these come from settings: the administrator chooses the
 * destination and retention, never where Relical keeps its keys or which
 * binary runs as root.
 */
final class Paths
{
    public const EXTENSION_ID = 'relical';

    public const PLESK = '/usr/sbin/plesk';

    /**
     * Where Plesk's `extension` utility may be when PRODUCT_ROOT_D is not
     * usable, in Cloudflare DNS Sync's order (PleskBin::FALLBACKS): the
     * `plesk` wrapper (then `bin extension`), Debian's /opt/psa, RHEL's
     * /usr/local/psa.
     */
    public const EXTENSION_FALLBACKS = ['/usr/sbin/plesk', '/opt/psa/bin/extension', '/usr/local/psa/bin/extension'];

    public const LIB_DIR = '/usr/local/lib/relical';
    public const RELICAL_BIN = self::LIB_DIR . '/relical';
    public const RESTIC_BIN = self::LIB_DIR . '/restic';

    public const ETC_DIR = '/etc/relical';
    public const CONFIG = self::ETC_DIR . '/config.json';
    public const KEYS_DIR = self::ETC_DIR . '/keys';
    public const SSH_DIR = self::ETC_DIR . '/ssh';
    public const SSH_KEY = self::SSH_DIR . '/id_ed25519';
    public const KNOWN_HOSTS = self::SSH_DIR . '/known_hosts';
    public const MYSQL_CNF = self::ETC_DIR . '/mysql.cnf';

    public const STATE_DIR = '/var/lib/relical';
    /**
     * Webspace id => last seen name, kept by inventory.php to report renames
     * (a file, because pm_Settings values are capped at 5,000 characters).
     */
    public const KNOWN_NAMES = self::STATE_DIR . '/inventory-names.json';
    /** The core's log of each run (core Task 5) and its run lock. */
    public const LOG = self::STATE_DIR . '/relical.log';
    public const LOCK = self::STATE_DIR . '/relical.lock';

    public const DOVEADM_BIN = '/usr/bin/doveadm';

    /** Where the OpenSSH tools may be (Plesk runs scripts with a minimal PATH). */
    public const SSH_KEYSCAN = ['/usr/bin/ssh-keyscan', '/bin/ssh-keyscan'];
    public const SFTP = ['/usr/bin/sftp', '/bin/sftp'];
    public const SYSTEMCTL = ['/usr/bin/systemctl', '/bin/systemctl'];
    public const SSH_KEYGEN = ['/usr/bin/ssh-keygen', '/bin/ssh-keygen'];
    public const MAILBOX_PATTERN = '/var/qmail/mailnames/{domain}/{mailbox}/Maildir';
    public const FILES_ROOT = '/var/www/vhosts';
    public const MAIL_ROOT = '/var/qmail/mailnames';

    /** Plesk's MySQL admin password (root-only, read by write-config.php). */
    public const PSA_SHADOW = '/etc/psa/.psa.shadow';

    /** The name the core reserves for the Plesk configuration repository. */
    public const SERVER_REPO = '_server';

    /**
     * The argv prefix that runs Plesk's `extension` utility from the panel,
     * resolved as Cloudflare DNS Sync's PleskBin::extensionUtility does
     * (proven from the panel process on Plesk 18.0.81):
     * `<PRODUCT_ROOT_D>/bin/extension` when PRODUCT_ROOT_D is a plain
     * absolute path and that file is executable, else the first executable
     * fallback, else `/opt/psa/bin/extension`. Argv form, so nothing is
     * ever parsed by a shell; a PRODUCT_ROOT_D with any other character is
     * ignored.
     *
     * @param ?string                    $productRoot  PRODUCT_ROOT_D when defined
     * @param ?callable(string): bool    $isExecutable is_executable, replaceable in tests
     * @return list<string>
     */
    public static function extensionUtility(?string $productRoot, ?callable $isExecutable = null): array
    {
        $isExecutable ??= static fn (string $path): bool => is_executable($path);
        if ($productRoot !== null && preg_match('#^/[A-Za-z0-9._/-]+$#D', $productRoot) === 1 && !str_contains($productRoot, '..')) {
            $candidate = rtrim($productRoot, '/') . '/bin/extension';
            if ($isExecutable($candidate)) {
                return [$candidate];
            }
        }
        foreach (self::EXTENSION_FALLBACKS as $path) {
            if ($isExecutable($path)) {
                return $path === self::PLESK ? [$path, 'bin', 'extension'] : [$path];
            }
        }

        return ['/opt/psa/bin/extension'];
    }

    /**
     * extensionUtility() for this process: Plesk's PHP defines
     * PRODUCT_ROOT_D. Resolved once per request.
     *
     * @return list<string>
     */
    public static function extensionCommand(): array
    {
        static $resolved = null;

        return $resolved ??= self::extensionUtility(defined('PRODUCT_ROOT_D') ? (string) constant('PRODUCT_ROOT_D') : null);
    }

    /**
     * How the core asks this extension for the inventory (it runs this as
     * root and reads the JSON on stdout).
     *
     * @return list<string>
     */
    public static function inventoryCommand(): array
    {
        return [self::PLESK, 'bin', 'extension', '--exec', self::EXTENSION_ID, 'inventory.php'];
    }

    /**
     * The configuration-only server backup; `{output}` is replaced by the
     * core with its staging file.
     *
     * @return list<string>
     */
    public static function panelConfigCommand(): array
    {
        return [
            self::PLESK, 'bin', 'pleskbackup', '--server',
            '-exclude-mail', '-exclude-files', '-exclude-databases', '-exclude-logs',
            '-include-server-settings', '-output-file', '{output}',
        ];
    }
}

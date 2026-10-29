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
     * The extension's root entry point, shipped in the package's sbin/.
     * Plesk installs it as <PRODUCT_ROOT_D>/admin/sbin/modules/relical/
     * relical-root and links <PRODUCT_ROOT_D>/admin/bin/modules/relical/
     * relical-root to its setuid mod_wrapper, which runs it as root for the
     * panel user (the path pm_ApiCli::callSbin() runs). `plesk bin
     * extension --exec` cannot be used for root work: it runs every script
     * as the panel user (psaadm), even when root starts it (Plesk 18.0.81).
     */
    public const ROOT_SCRIPT = 'relical-root';

    /** Plesk's install root: Debian and Ubuntu, then RHEL-like systems. */
    public const PRODUCT_ROOTS = ['/opt/psa', '/usr/local/psa'];

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
     * Plesk's install root: PRODUCT_ROOT_D when it is a plain absolute path
     * holding admin/sbin/mod_wrapper, else the first of PRODUCT_ROOTS that
     * holds it, else /opt/psa. A PRODUCT_ROOT_D with any other character is
     * ignored.
     *
     * @param ?string                 $productRoot PRODUCT_ROOT_D when defined
     * @param ?callable(string): bool $exists      is_file, replaceable in tests
     */
    public static function productRootOf(?string $productRoot, ?callable $exists = null): string
    {
        $exists ??= static fn (string $path): bool => is_file($path);
        $candidates = self::PRODUCT_ROOTS;
        if ($productRoot !== null && preg_match('#^/[A-Za-z0-9._/-]+$#D', $productRoot) === 1 && !str_contains($productRoot, '..')) {
            array_unshift($candidates, rtrim($productRoot, '/'));
        }
        foreach ($candidates as $root) {
            if ($exists($root . '/admin/sbin/mod_wrapper')) {
                return $root;
            }
        }

        return self::PRODUCT_ROOTS[0];
    }

    /** productRootOf() for this process (Plesk's PHP defines PRODUCT_ROOT_D), resolved once. */
    public static function productRoot(): string
    {
        static $resolved = null;

        return $resolved ??= self::productRootOf(defined('PRODUCT_ROOT_D') ? (string) constant('PRODUCT_ROOT_D') : null);
    }

    /**
     * The command that runs a root script as root from the panel user:
     * the mod_wrapper link Plesk makes for sbin/relical-root. Argv form, so
     * nothing is parsed by a shell.
     *
     * @return list<string>
     */
    public static function rootCommand(?string $productRoot = null): array
    {
        return [($productRoot ?? self::productRoot()) . '/admin/bin/modules/' . self::EXTENSION_ID . '/' . self::ROOT_SCRIPT];
    }

    /**
     * relical-root itself, for a caller that already is root (the core's
     * inventory command, an administrator's shell).
     */
    public static function rootScript(?string $productRoot = null): string
    {
        return ($productRoot ?? self::productRoot()) . '/admin/sbin/modules/' . self::EXTENSION_ID . '/' . self::ROOT_SCRIPT;
    }

    /**
     * How the core asks this extension for the inventory: it runs this as
     * root and reads the JSON on stdout. Not `plesk bin extension --exec`,
     * which would run inventory.php as the panel user and mixes its stderr
     * into stdout.
     *
     * @return list<string>
     */
    public static function inventoryCommand(?string $productRoot = null): array
    {
        return [self::rootScript($productRoot), 'inventory.php'];
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

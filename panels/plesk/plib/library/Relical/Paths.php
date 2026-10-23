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

    public const DOVEADM_BIN = '/usr/bin/doveadm';
    public const MAILBOX_PATTERN = '/var/qmail/mailnames/{domain}/{mailbox}/Maildir';
    public const FILES_ROOT = '/var/www/vhosts';
    public const MAIL_ROOT = '/var/qmail/mailnames';

    /** Plesk's MySQL admin password (root-only, read by write-config.php). */
    public const PSA_SHADOW = '/etc/psa/.psa.shadow';

    /** The name the core reserves for the Plesk configuration repository. */
    public const SERVER_REPO = '_server';

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

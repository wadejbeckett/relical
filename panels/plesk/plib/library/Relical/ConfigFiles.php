<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Writes /etc/relical/config.json and /etc/relical/mysql.cnf, both 0600 and
 * each atomically. Used by write-config.php (on every settings save) and by
 * post-install.php; inventory.php (every run) refreshes mysql.cnf when
 * Plesk's MySQL admin password has changed.
 *
 * The configuration is rendered before Plesk's database password is read,
 * so a bad setting never gets as far as the secret. The password is never
 * printed or put in an exception message.
 */
final class ConfigFiles
{
    /**
     * @param array<string, mixed> $input    Settings::configInput()
     * @param string               $serverId this server's host name
     */
    public static function write(array $input, string $serverId): void
    {
        $config = ConfigWriter::render($input, $serverId);
        self::writeMysqlOptionFile();
        RootFile::write(Paths::CONFIG, ConfigWriter::toJson($config));
    }

    public static function writeMysqlOptionFile(): void
    {
        RootFile::ensureDir(Paths::ETC_DIR);
        RootFile::write(Paths::MYSQL_CNF, self::mysqlOptionFileFrom(Paths::PSA_SHADOW));
    }

    /**
     * Rewrites the option file when it does not hold the password Plesk's
     * shadow file holds now (`plesk bin admin --set-mysql-password`, a
     * repair), so database backups do not fail from that night on. A file
     * with the right password is rewritten too when its mode is not 0600
     * or its owner is not $owner (the user this runs as: root for
     * inventory.php), so loosened permissions are put back.
     *
     * @param ?int $owner the owner the file must have; null: this process's effective user
     * @return bool whether the file was rewritten
     */
    public static function refreshMysqlOptionFile(string $shadow = Paths::PSA_SHADOW, string $target = Paths::MYSQL_CNF, ?int $owner = null): bool
    {
        $want = self::mysqlOptionFileFrom($shadow);
        if (is_link($target)) {
            throw new RuntimeException(sprintf('%s is a symbolic link', $target));
        }
        $owner ??= function_exists('posix_geteuid') ? posix_geteuid() : 0;
        clearstatcache(true, $target);
        $stat = is_file($target) ? @stat($target) : false;
        $current = $stat !== false ? @file_get_contents($target) : false;
        if (is_string($current) && self::optionFileMatches($current, $want)
            && ($stat['mode'] & 07777) === 0600 && $stat['uid'] === $owner) {
            return false;
        }
        RootFile::write($target, $want);

        return true;
    }

    /** Whether the option file on disk is the one the current password gives. */
    public static function optionFileMatches(string $current, string $want): bool
    {
        return hash_equals($want, $current);
    }

    private static function mysqlOptionFileFrom(string $shadow): string
    {
        $password = @file_get_contents($shadow);
        if ($password === false) {
            throw new RuntimeException(sprintf('could not read %s', $shadow));
        }

        return ConfigWriter::mysqlOptionFile(rtrim($password, "\r\n"));
    }
}

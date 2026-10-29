<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Writes /etc/relical/config.json and /etc/relical/mysql.cnf, both 0600 and
 * each atomically. Used by write-config.php (on every settings save) and by
 * post-install.php.
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
        $password = @file_get_contents(Paths::PSA_SHADOW);
        if ($password === false) {
            throw new RuntimeException(sprintf('could not read %s', Paths::PSA_SHADOW));
        }
        $optionFile = ConfigWriter::mysqlOptionFile(rtrim($password, "\r\n"));
        unset($password);

        RootFile::ensureDir(Paths::ETC_DIR);
        RootFile::write(Paths::MYSQL_CNF, $optionFile);
    }
}

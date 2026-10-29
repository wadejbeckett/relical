<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * The core's own validation rules, mirrored so the extension can leave out
 * (and report) an entry the core would refuse, instead of handing the core
 * an inventory it refuses or skips. Every check here must be at least as
 * strict as the core's.
 *
 * Source: core/internal/config/config.go (ValidDomain, CleanAbs, Under) and
 * core/internal/inventory/inventory.go (sysUserRE, dbNameRE, dbTypeRE, idRE,
 * reservedDatabases, isWebspaceOf), including the 29 October 2026 change
 * that lets a system user contain ".".
 *
 * tests/fixtures/core-rules.json lists the cases both sides must accept or
 * refuse; CoreRulesTest runs them here, and the core's tests are meant to
 * read the same file.
 */
final class CoreRules
{
    public const DOMAIN = '/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/D';
    public const SYS_USER = '/^[a-z_][a-z0-9_.-]{0,31}$/D';
    /** Never a leading "-" or "$": mysqldump would read an option. */
    public const DB_NAME = '/^[A-Za-z0-9_][A-Za-z0-9_$-]{0,63}$/D';

    /**
     * MySQL's and Plesk's own databases, compared case-insensitively; any
     * name starting with RESERVED_DB_PREFIX is reserved too.
     *
     * @var list<string>
     */
    public const RESERVED_DB = [
        'mysql', 'information_schema', 'performance_schema', 'sys',
        'psa', 'apsc', 'horde', 'roundcubemail', 'atmail', 'sitebuilder5',
    ];
    public const RESERVED_DB_PREFIX = 'phpmyadmin';
    public const DB_TYPE = '/^[a-z0-9]{1,16}$/D';
    public const ID = '/^[A-Za-z0-9_-]{1,64}$/D';

    /** config.ValidDomain: lower-case punycode, at least one dot. */
    public static function validDomain(string $name): bool
    {
        return strlen($name) <= 253 && preg_match(self::DOMAIN, $name) === 1;
    }

    public static function validSysUser(string $user): bool
    {
        return preg_match(self::SYS_USER, $user) === 1;
    }

    /** The name's characters only; see validDatabase() for the full rule. */
    public static function validDbName(string $name): bool
    {
        return preg_match(self::DB_NAME, $name) === 1;
    }

    /** inventory.reservedDB: a server or panel database, never a subscription's. */
    public static function reservedDb(string $name): bool
    {
        $lower = strtolower($name);

        return in_array($lower, self::RESERVED_DB, true) || str_starts_with($lower, self::RESERVED_DB_PREFIX);
    }

    /** inventory.ValidDatabaseName: valid characters and not reserved. */
    public static function validDatabase(string $name): bool
    {
        return self::validDbName($name) && !self::reservedDb($name);
    }

    public static function validDbType(string $type): bool
    {
        return preg_match(self::DB_TYPE, $type) === 1;
    }

    public static function validId(string $id): bool
    {
        return preg_match(self::ID, $id) === 1;
    }

    /**
     * config.CleanAbs: absolute and equal to Go's filepath.Clean of itself
     * (no "//", no trailing "/", no "." or ".." segments).
     */
    public static function cleanAbs(string $path): bool
    {
        if ($path === '/') {
            return true;
        }
        if ($path === '' || $path[0] !== '/' || str_ends_with($path, '/') || str_contains($path, "\0")) {
            return false;
        }
        foreach (explode('/', substr($path, 1)) as $part) {
            if ($part === '' || $part === '.' || $part === '..') {
                return false;
            }
        }

        return true;
    }

    /**
     * inventory.isWebspaceOf: the home is exactly <files root>/<name> for one
     * of the roots.
     *
     * @param list<string> $roots
     */
    public static function webspaceOf(string $home, string $name, array $roots): bool
    {
        foreach ($roots as $root) {
            if ($home === $root . '/' . $name) {
                return true;
            }
        }

        return false;
    }
}

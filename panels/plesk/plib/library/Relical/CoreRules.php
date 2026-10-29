<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * The core's own validation rules, mirrored exactly so the extension can
 * leave out (and report) an entry the core would refuse. The core refuses
 * the whole inventory when one entry breaks a rule, so every check here
 * must be at least as strict as the core's.
 *
 * Source: core/internal/config/config.go (ValidDomain, CleanAbs, Under) and
 * core/internal/inventory/inventory.go (sysUserRE, dbNameRE, dbTypeRE, idRE)
 * on main at 5eb8e2e.
 */
final class CoreRules
{
    public const DOMAIN = '/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$/D';
    public const SYS_USER = '/^[a-z_][a-z0-9_-]{0,31}$/D';
    public const DB_NAME = '/^[A-Za-z0-9_$-]{1,64}$/D';
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

    public static function validDbName(string $name): bool
    {
        return preg_match(self::DB_NAME, $name) === 1;
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
     * config.Under: strictly inside one of the roots.
     *
     * @param list<string> $roots
     */
    public static function under(string $path, array $roots): bool
    {
        foreach ($roots as $root) {
            if (str_starts_with($path, $root . '/')) {
                return true;
            }
        }

        return false;
    }
}

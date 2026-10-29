<?php

declare(strict_types=1);

/**
 * Minimal PSR-4 autoloader for the extension's own library.
 *
 * The extension has no Composer runtime dependencies, so rather than ship a
 * Composer-generated autoloader it registers this tiny loader, mapping the
 * `Noiz\Relical\` namespace onto plib/library/Relical/.
 *
 * Hooks, controllers and scripts `require_once` this file before using any
 * `Noiz\Relical\` class.
 */

spl_autoload_register(static function (string $class): void {
    $prefix = 'Noiz\\Relical\\';
    $length = strlen($prefix);

    if (strncmp($class, $prefix, $length) !== 0) {
        return;
    }

    $file = __DIR__ . '/Relical/' . str_replace('\\', '/', substr($class, $length)) . '.php';

    if (is_file($file)) {
        require $file;
    }
});

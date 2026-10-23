<?php

declare(strict_types=1);

namespace Noiz\Relical;

/**
 * What Installer needs to know about the filesystem, so its plans can be
 * computed and tested without the real one.
 */
interface Probe
{
    public function exists(string $path): bool;

    /** The file's contents, or null when it cannot be read. */
    public function read(string $path): ?string;

    /** The file's sha256 in lower-case hex, or null when it cannot be read. */
    public function sha256(string $path): ?string;
}

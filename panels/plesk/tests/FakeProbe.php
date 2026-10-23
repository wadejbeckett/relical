<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\Probe;

/** An in-memory filesystem for planning tests: path => contents (null for a directory). */
final class FakeProbe implements Probe
{
    /** @param array<string, ?string> $files */
    public function __construct(public array $files = [])
    {
    }

    public function exists(string $path): bool
    {
        return array_key_exists($path, $this->files);
    }

    public function read(string $path): ?string
    {
        return $this->files[$path] ?? null;
    }

    public function sha256(string $path): ?string
    {
        $content = $this->files[$path] ?? null;

        return $content === null ? null : hash('sha256', $content);
    }
}

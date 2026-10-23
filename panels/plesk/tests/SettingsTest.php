<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\Settings;
use PHPUnit\Framework\TestCase;

final class SettingsTest extends TestCase
{
    public function testDefaults(): void
    {
        $settings = new Settings(static fn (string $name, ?string $default): ?string => $default);

        self::assertSame('all', $settings->scope());
        self::assertSame(23, $settings->sftpPort());
        self::assertSame('relical', $settings->sftpPath());
        self::assertSame(['daily' => 14, 'weekly' => 8, 'monthly' => 12], $settings->retention());
        self::assertSame('02:30', $settings->backupTime());
        self::assertSame(0, $settings->limitUploadKib());
    }

    public function testANonNumericNumberReadsAsInvalidRatherThanZero(): void
    {
        $settings = new Settings(static fn (string $name, ?string $default): ?string => $name === 'sftp_port' ? '23abc' : $default);

        self::assertSame(-1, $settings->sftpPort());
    }
}

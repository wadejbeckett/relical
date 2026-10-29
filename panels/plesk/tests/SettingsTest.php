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
        self::assertSame('01:00', $settings->backupTime());
        self::assertSame(0, $settings->limitUploadKib());
    }

    public function testANonNumericNumberReadsAsInvalidRatherThanZero(): void
    {
        $settings = new Settings(static fn (string $name, ?string $default): ?string => $name === 'sftp_port' ? '23abc' : $default);

        self::assertSame(-1, $settings->sftpPort());
    }

    /** @return array<string, string> a complete, valid settings form */
    private static function form(): array
    {
        return [
            'scope' => 'plan-items',
            'sftp_host' => ' U000000-sub1.your-storagebox.example ',
            'sftp_port' => '23',
            'sftp_user' => 'u000000-sub1',
            'sftp_path' => 'relical/',
            'retention_daily' => '7',
            'retention_weekly' => '4',
            'retention_monthly' => '0',
            'backup_time' => '03:15',
            'limit_upload_kib' => '',
        ];
    }

    public function testValidatedFormIsStoredNormalised(): void
    {
        self::assertSame([], Settings::errors(self::form()));
        self::assertSame([
            'scope' => 'plan-items',
            'sftp_host' => 'u000000-sub1.your-storagebox.example',
            'sftp_port' => '23',
            'sftp_user' => 'u000000-sub1',
            'sftp_path' => 'relical',
            'retention_daily' => '7',
            'retention_weekly' => '4',
            'retention_monthly' => '0',
            'backup_time' => '03:15',
            'limit_upload_kib' => '0',
        ], Settings::validated(self::form()));
    }

    public function testABlankPathAndTimeTakeTheirDefaults(): void
    {
        $form = ['sftp_path' => ' ', 'backup_time' => ''] + self::form();

        self::assertSame([], Settings::errors($form));
        $stored = Settings::validated($form);
        self::assertSame('relical', $stored['sftp_path']);
        self::assertSame('01:00', $stored['backup_time']);
    }

    /** @return iterable<string, array{array<string, mixed>, string, string}> */
    public static function badForms(): iterable
    {
        yield 'no scope' => [['scope' => ''], 'scope', 'choose'];
        yield 'unknown scope' => [['scope' => 'some'], 'scope', 'choose'];
        yield 'no host' => [['sftp_host' => ''], 'sftp_host', 'host'];
        yield 'host as an option' => [['sftp_host' => '-oProxyCommand=id'], 'sftp_host', 'host'];
        yield 'port text' => [['sftp_port' => '23a'], 'sftp_port', 'port'];
        yield 'port too high' => [['sftp_port' => '70000'], 'sftp_port', 'port'];
        yield 'user' => [['sftp_user' => 'Admin'], 'sftp_user', 'user'];
        yield 'path' => [['sftp_path' => '../x'], 'sftp_path', '..'];
        yield 'weekly' => [['retention_weekly' => 'many'], 'retention_weekly', 'weekly'];
        yield 'nothing kept' => [['retention_daily' => '0', 'retention_weekly' => '0', 'retention_monthly' => '0'], 'retention_daily', 'at least one'];
        yield 'time' => [['backup_time' => '24:00'], 'backup_time', 'HH:MM'];
        yield 'time without colon' => [['backup_time' => '0100'], 'backup_time', 'HH:MM'];
        yield 'limit negative' => [['limit_upload_kib' => '-5'], 'limit_upload_kib', 'upload limit'];
        yield 'array value' => [['sftp_user' => ['x']], 'sftp_user', 'user'];
    }

    /**
     * @dataProvider badForms
     * @param array<string, mixed> $change
     */
    public function testErrorsNameTheField(array $change, string $field, string $message): void
    {
        $errors = Settings::errors($change + self::form());

        self::assertArrayHasKey($field, $errors);
        self::assertStringContainsString($message, $errors[$field]);
        $this->expectException(\InvalidArgumentException::class);
        Settings::validated($change + self::form());
    }
}

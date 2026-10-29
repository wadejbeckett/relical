<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\RestoreRecord;
use PHPUnit\Framework\TestCase;

final class RestoreRecordTest extends TestCase
{
    private const ID = '20261029104000-0123456789abcdef01234567';
    private const NOW = 1793275200;
    private const ARGS = ['restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', 'abcdef12', '--path', 'httpdocs'];

    public function testARunningRecordNamesTheRequest(): void
    {
        $record = RestoreRecord::running(self::ID, self::ARGS, self::NOW);

        self::assertSame('example.test', $record['subscription']);
        self::assertSame('files', $record['unit']);
        self::assertSame(['snapshot' => 'abcdef12', 'path' => 'httpdocs'], $record['request']);
        self::assertSame('running', $record['state']);
        self::assertSame(gmdate(DATE_ATOM, self::NOW), $record['started']);
    }

    public function testOnlyACustomerRestoreIsRecorded(): void
    {
        $this->expectException(InvalidArgumentException::class);
        RestoreRecord::running(self::ID, ['restore', '--subscription', '_server', '--unit', 'panel-config'], self::NOW);
    }

    public function testDoneNeedsExitZeroAndRestoredTo(): void
    {
        $record = RestoreRecord::running(self::ID, self::ARGS, self::NOW);
        $out = '{"subscription":"example.test","unit":"files","restored_to":"/var/www/vhosts/example.test/restored-2026-10-28-abcdef12","files":3,"bytes":2048}';

        $done = RestoreRecord::finished($record, 0, $out, '', false, self::NOW + 5);
        self::assertSame('done', $done['state']);
        self::assertSame(3, $done['result']['files']);

        self::assertSame('failed', RestoreRecord::finished($record, 0, '{"files":3}', '', false, self::NOW)['state']);
        self::assertSame('failed', RestoreRecord::finished($record, 1, $out, '', false, self::NOW)['state']);
        self::assertSame('failed', RestoreRecord::finished($record, 0, $out, '', true, self::NOW)['state']);
    }

    public function testTheFailureReasonComesFromTheCoreThenStderr(): void
    {
        $record = RestoreRecord::running(self::ID, self::ARGS, self::NOW);

        self::assertSame('no backup snapshots', RestoreRecord::finished($record, 1, '{"error": "no backup snapshots"}', 'x', false, self::NOW)['error']);
        self::assertSame('last line', RestoreRecord::finished($record, 2, '', "first\nlast line\n", false, self::NOW)['error']);
        self::assertSame('exit 1', RestoreRecord::finished($record, 1, '', '', false, self::NOW)['error']);
        self::assertSame('the restore did not finish in time', RestoreRecord::finished($record, -1, '', '', true, self::NOW)['error']);
    }

    public function testViewOfADoneFilesRestore(): void
    {
        $record = RestoreRecord::finished(
            RestoreRecord::running(self::ID, self::ARGS, self::NOW),
            0,
            '{"restored_to":"/var/www/vhosts/example.test/restored-2026-10-28-abcdef12","files":1,"bytes":2048}',
            '',
            false,
            self::NOW
        );
        $view = RestoreRecord::view($record, 'example.test', self::NOW, self::NOW, false);

        self::assertSame('done', $view['state']);
        self::assertSame('restored-2026-10-28-abcdef12', $view['location']);
        self::assertSame('1 file, ' . \Noiz\Relical\Status::bytes(2048), $view['count']);
        self::assertFalse($view['refresh']);
    }

    public function testLocations(): void
    {
        self::assertSame('Restored 2026-10-28 abcdef12', RestoreRecord::location('mail', 'Restored 2026-10-28 abcdef12'));
        self::assertSame('restored-2026-10-28-abcdef12/example_wp.sql', RestoreRecord::location('db', '/var/www/vhosts/example.test/restored-2026-10-28-abcdef12/example_wp.sql'));
    }

    public function testARecordOfAnotherSubscriptionReadsAsMissing(): void
    {
        $record = RestoreRecord::running(self::ID, self::ARGS, self::NOW);

        self::assertSame('missing', RestoreRecord::view($record, 'other.example.test', self::NOW, self::NOW, false)['state']);
        self::assertSame('missing', RestoreRecord::view($record, 'other.example.test', self::NOW, self::NOW, true)['state']);
        self::assertSame('running', RestoreRecord::view($record, 'example.test', self::NOW, self::NOW, false)['state']);
        self::assertTrue(RestoreRecord::view($record, 'example.test', self::NOW, self::NOW, false)['refresh']);
    }

    public function testNoRecordIsStartingOnlyJustAfterTheStart(): void
    {
        self::assertSame('starting', RestoreRecord::view(null, 'example.test', self::NOW, self::NOW + 10, false)['state']);
        self::assertSame('missing', RestoreRecord::view(null, 'example.test', self::NOW, self::NOW + 600, false)['state']);
        self::assertSame('missing', RestoreRecord::view(null, 'example.test', null, self::NOW, false)['state']);
    }

    public function testACustomerSeesOnlyPlainErrors(): void
    {
        $record = RestoreRecord::failed(RestoreRecord::running(self::ID, self::ARGS, self::NOW), 'Fatal: unable to open repository at sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/example.test', self::NOW);

        $customer = RestoreRecord::view($record, 'example.test', self::NOW, self::NOW, false);
        self::assertSame('failed', $customer['state']);
        self::assertStringNotContainsString('storagebox', $customer['error']);
        self::assertStringContainsString('Nothing was changed', $customer['error']);

        $admin = RestoreRecord::view($record, 'example.test', self::NOW, self::NOW, true);
        self::assertStringContainsString('sftp:u000000-sub1', $admin['error']);
    }

    public function testPlainCoreErrorsReachTheCustomer(): void
    {
        self::assertSame(
            'Folder "Restored-x" already exists in info@example.test (delete or rename it).',
            RestoreRecord::customerError('folder "Restored-x" already exists in info@example.test (delete or rename it)')
        );
        self::assertSame('No backup snapshots.', RestoreRecord::customerError('no backup snapshots'));
        self::assertSame(RestoreRecord::BUSY, RestoreRecord::customerError(RestoreRecord::BUSY));
        self::assertStringContainsString('Contact your hosting provider', RestoreRecord::customerError('repository at sftp:x already exists'));
        self::assertStringContainsString('Contact your hosting provider', RestoreRecord::customerError('home /var/www/vhosts/example.test is owned by uid 0'));
    }

    /** @return iterable<string, array{string, string}> */
    public static function readErrors(): iterable
    {
        yield 'no key yet' => ['no key for example.test: no key file (/etc/relical/keys/example.test.key)', RestoreRecord::NO_BACKUPS_YET];
        yield 'no repository' => ['Fatal: repository does not exist: unable to open config file', RestoreRecord::NO_BACKUPS_YET];
        yield 'no snapshots' => ['no backup snapshots', RestoreRecord::NO_BACKUPS_YET];
        yield 'storage box down' => ['Fatal: unable to open repository at sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/example.test: ssh: connection refused', RestoreRecord::CANNOT_READ];
        yield 'anything else' => ['the server did not finish in time', RestoreRecord::CANNOT_READ];
    }

    /** @dataProvider readErrors */
    public function testReadErrorsACustomerSees(string $core, string $shown): void
    {
        self::assertSame($shown, RestoreRecord::customerReadError($core));
        self::assertStringNotContainsString('/etc/relical', $shown);
    }
}

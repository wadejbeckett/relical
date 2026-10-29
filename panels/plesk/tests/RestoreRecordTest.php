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
        self::assertSame('Restored 2026-10-28 abcdef12', RestoreRecord::location('mail', 'INBOX.Restored 2026-10-28 abcdef12'));
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
        self::assertSame(RestoreRecord::SERVER_BUSY, RestoreRecord::customerError(RestoreRecord::SERVER_BUSY));
        self::assertStringContainsString('Contact your hosting provider', RestoreRecord::customerError('repository at sftp:x already exists'));
        self::assertStringContainsString('Contact your hosting provider', RestoreRecord::customerError('home /var/www/vhosts/example.test is owned by uid 0'));
    }

    /**
     * The core's exact words (core/internal/restic/runner.go LockedMessage,
     * restore/restore.go, restore/files.go, restore/webspace.go), as a
     * customer of example.test sees them.
     *
     * @return iterable<string, array{string, string}>
     */
    public static function coreErrors(): iterable
    {
        yield 'locked by a check or prune' => [
            'restic restore: the backup is being checked or pruned, try again in a few minutes (exit 11: repo already locked, waiting up to 2m0s for the lock | unable to create lock in backend: repository is already locked by PID 4242 on host.example.test by root (UID 0, GID 0) | lock was created at 2026-10-29 02:00:01 (1m2s ago) | storage ID 1a2b3c4d)',
            RestoreRecord::LOCKED,
        ];
        yield 'mail folder not in the snapshot' => [
            'folder "INBOX.Old" is not in snapshot 1a2b3c4d of info@example.test',
            'Folder "INBOX.Old" is not in snapshot 1a2b3c4d of info@example.test.',
        ];
        yield 'mailbox not in the snapshot' => [
            'mailbox info@example.test is not in snapshot 1a2b3c4d',
            'Mailbox info@example.test is not in snapshot 1a2b3c4d.',
        ];
        yield 'path not in the snapshot' => [
            'path "httpdocs/old.php" is not in snapshot 1a2b3c4d',
            'Path "httpdocs/old.php" is not in snapshot 1a2b3c4d.',
        ];
        yield 'left over by an interrupted restore' => [
            "/var/www/vhosts/example.test/restored-2026-10-29-1a2b3c4d already exists and is still owned by root: it is left from a restore of this snapshot that was interrupted; ask the server's administrator to remove it, then restore again",
            "/var/www/vhosts/example.test/restored-2026-10-29-1a2b3c4d already exists and is still owned by root: it is left from a restore of this snapshot that was interrupted; ask the server's administrator to remove it, then restore again.",
        ];
        yield 'an earlier restore of this snapshot' => [
            '/var/www/vhosts/example.test/restored-2026-10-29-1a2b3c4d already exists (it may be from an earlier, possibly incomplete restore of this snapshot); delete or rename it to restore this snapshot again',
            '/var/www/vhosts/example.test/restored-2026-10-29-1a2b3c4d already exists (it may be from an earlier, possibly incomplete restore of this snapshot); delete or rename it to restore this snapshot again.',
        ];
        yield 'a folder that appeared while the restore ran' => [
            "restored folder /var/www/vhosts/example.test/restored-2026-10-29-1a2b3c4d appeared in the webspace while the restore ran; nothing was replaced; ask the server's administrator",
            "Restored folder /var/www/vhosts/example.test/restored-2026-10-29-1a2b3c4d appeared in the webspace while the restore ran; nothing was replaced; ask the server's administrator.",
        ];
    }

    /** @dataProvider coreErrors */
    public function testCoreErrorsACustomerReads(string $core, string $shown): void
    {
        self::assertSame($shown, RestoreRecord::customerError($core, 'example.test'));
        $record = RestoreRecord::failed(RestoreRecord::running(self::ID, self::ARGS, self::NOW), $core, self::NOW);
        self::assertSame($shown, RestoreRecord::view($record, 'example.test', self::NOW, self::NOW, false)['error']);
    }

    /**
     * Allowlisted words that name more than the customer's subscription.
     *
     * @return iterable<string, array{string}>
     */
    public static function leakingErrors(): iterable
    {
        yield 'repository' => ['Fatal: create repository at sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/example.test failed: config file already exists'];
        yield 'sftp address' => ['folder "x" is not in snapshot 1a2b3c4d of sftp:u1@box'];
        yield 'relical config' => ['/etc/relical/keys/example.test.key already exists'];
        yield 'host name' => ['path "x" is not in snapshot 1a2b3c4d on host.example.test'];
        yield 'the word host' => ['folder "x" already exists on host web1'];
        yield 'another subscription' => ['/var/www/vhosts/other.test/restored-x already exists and is still owned by root: ask the server\'s administrator to remove it'];
        yield 'another subscription in a home path' => ['/var/www/vhosts/other.test/example.test/restored-x already exists'];
        yield 'a look-alike subscription' => ['/var/www/vhosts/example.test.evil/restored-x already exists'];
        yield 'a path outside any home' => ['/root/restored-x already exists'];
        yield 'another subscription\'s folder appeared' => ["restored folder /var/www/vhosts/other.test/restored-x appeared in the webspace while the restore ran; nothing was replaced; ask the server's administrator"];
        yield 'another domain' => ['mailbox info@example.test is not in snapshot 1a2b3c4d (other.test)'];
    }

    /** @dataProvider leakingErrors */
    public function testCoreErrorsNamingMoreStayHidden(string $core): void
    {
        self::assertStringContainsString('Contact your hosting provider', RestoreRecord::customerError($core, 'example.test'));
    }

    public function testAReadDuringACheckOrPruneSaysSo(): void
    {
        self::assertSame(RestoreRecord::LOCKED, RestoreRecord::customerReadError('restic snapshots: the backup is being checked or pruned, try again in a few minutes (exit 11: repository is already locked by PID 1 on host.example.test)'));
    }

    /** @return iterable<string, array{string, string}> */
    public static function readErrors(): iterable
    {
        yield 'no key yet' => ['no key for example.test: no key file (/etc/relical/keys/example.test.key)', RestoreRecord::NO_BACKUPS_YET];
        yield 'no repository' => ['Fatal: repository does not exist: unable to open config file', RestoreRecord::NO_BACKUPS_YET];
        yield 'no snapshots' => ['no backup snapshots', RestoreRecord::NO_BACKUPS_YET];
        yield 'storage box down' => ['Fatal: unable to open repository at sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/example.test: ssh: connection refused', RestoreRecord::CANNOT_READ];
        yield 'anything else' => ['the server did not finish in time', RestoreRecord::CANNOT_READ];
        yield 'another page is reading' => [RestoreRecord::READ_BUSY, RestoreRecord::READ_BUSY];
        yield 'the server is busy reading' => [RestoreRecord::READ_SERVER_BUSY, RestoreRecord::READ_SERVER_BUSY];
    }

    /** @dataProvider readErrors */
    public function testReadErrorsACustomerSees(string $core, string $shown): void
    {
        self::assertSame($shown, RestoreRecord::customerReadError($core));
        self::assertStringNotContainsString('/etc/relical', $shown);
    }

    public function testWhenANewRestoreMayStart(): void
    {
        $free = ['running' => false, 'server_busy' => false, 'restored_folders' => 4];

        self::assertNull(RestoreRecord::startRefusal($free, 'files', false));
        self::assertSame(RestoreRecord::BUSY, RestoreRecord::startRefusal(['running' => true] + $free, 'mail', true));
        self::assertSame(RestoreRecord::SERVER_BUSY, RestoreRecord::startRefusal(['server_busy' => true] + $free, 'mail', false));
        self::assertSame(RestoreRecord::SERVER_BUSY, RestoreRecord::startRefusal(['server_busy' => true] + $free, 'files', true), 'the server cap holds for administrators too');
    }

    public function testFiveRestoredCopiesStopAFilesOrDatabaseRestore(): void
    {
        $full = ['running' => false, 'server_busy' => false, 'restored_folders' => 5];

        self::assertStringContainsString('already 5 restored copies', (string) RestoreRecord::startRefusal($full, 'files', false));
        self::assertNotNull(RestoreRecord::startRefusal($full, 'db', false));
        self::assertNull(RestoreRecord::startRefusal($full, 'mail', false), 'mail goes into the mailbox, not the home directory');
        self::assertNull(RestoreRecord::startRefusal($full, 'files', true), 'administrators are exempt');
    }

    public function testAnUnreadableStateRefuses(): void
    {
        self::assertSame(RestoreRecord::CANNOT_START, RestoreRecord::startRefusal(null, 'mail', true));
        self::assertSame(RestoreRecord::CANNOT_START, RestoreRecord::startRefusal(['running' => false], 'mail', true));
        self::assertSame(RestoreRecord::CANNOT_START, RestoreRecord::startRefusal(['error' => 'x'], 'mail', true));
    }

    /** @return array<string, mixed> */
    private static function record(string $unit, string $started, array $extra = []): array
    {
        return array_merge(['subscription' => 'example.test', 'unit' => $unit, 'state' => 'done', 'started' => $started, 'error' => ''], $extra);
    }

    public function testRecentCustomerRestoresCountFilesAndDatabasesOfTheLastDay(): void
    {
        $now = strtotime('2026-10-29T12:00:00Z');
        $records = [
            self::record('files', '2026-10-29T11:00:00Z'),
            self::record('db', '2026-10-28T12:00:01Z'),
            self::record('files', '2026-10-29T10:00:00Z', ['state' => 'failed', 'error' => 'restic: exit 1']), // ran, counts
            self::record('files', '2026-10-29T09:00:00Z', ['state' => 'running']),
            self::record('files', '2026-10-28T12:00:00Z'),                                // 24 h ago: out
            self::record('mail', '2026-10-29T11:00:00Z'),                                 // mail: out
            self::record('files', '2026-10-29T11:00:00Z', ['by_admin' => true]),          // administrator: out
            self::record('files', '2026-10-29T11:00:00Z', ['state' => 'failed', 'error' => RestoreRecord::BUSY]),
            self::record('files', '2026-10-29T11:00:00Z', ['state' => 'failed', 'error' => RestoreRecord::SERVER_BUSY]),
            self::record('files', '2026-10-29T11:00:00Z', ['state' => 'failed', 'error' => RestoreRecord::TOO_MANY_RESTORES]),
            self::record('files', '2026-10-29T11:00:00Z', ['subscription' => 'other.example.test']),
            self::record('files', 'not a date'),
            'garbage',
        ];

        self::assertSame(4, RestoreRecord::recentCustomerRestores($records, 'example.test', $now));
    }

    /** @return iterable<string, array{string}> */
    public static function refusalsThatRestoredNothing(): iterable
    {
        yield 'busy' => [RestoreRecord::BUSY];
        yield 'server busy' => [RestoreRecord::SERVER_BUSY];
        yield 'over the limit' => [RestoreRecord::TOO_MANY_RESTORES];
        yield 'already exists' => ['/var/www/vhosts/example.test/restored-2026-10-29-6779981c already exists (it may be from an earlier, possibly incomplete restore of this snapshot); delete or rename it to restore this snapshot again'];
        yield 'left by root' => ['/var/www/vhosts/example.test/restored-2026-10-29-6779981c already exists and is still owned by root: it is left from a restore of this snapshot that was interrupted; ask the server\'s administrator to remove it, then restore again'];
        yield 'not in snapshot' => ['path "httpdocs/x" is not in snapshot 6779981c'];
        yield 'no snapshots' => ['no backup snapshots'];
        yield 'snapshot not found' => ['snapshot 6779981c not found'];
        yield 'no database backup' => ['snapshot 6779981c has no backup of database example_wp'];
        yield 'free space' => ['not enough free space for this restore in /var/www/vhosts/example.test (need 2.0 GiB, free 1.0 GiB)'];
        yield 'locked' => ['restic: unable to create lock in backend: ' . RestoreRecord::CORE_LOCKED];
        yield 'run lock' => ['another relical run holds the lock'];
    }

    /** @dataProvider refusalsThatRestoredNothing */
    public function testARestoreRefusedBeforeAnythingWasRestoredDoesNotCount(string $error): void
    {
        $now = strtotime('2026-10-29T12:00:00Z');
        $records = [
            self::record('files', '2026-10-29T11:00:00Z'),
            self::record('db', '2026-10-29T11:00:00Z'),
            self::record('files', '2026-10-29T11:30:00Z', ['state' => 'failed', 'error' => $error]),
        ];

        self::assertSame(2, RestoreRecord::recentCustomerRestores($records, 'example.test', $now));
    }

    public function testARestoreThatRanAndFailedStillCounts(): void
    {
        $now = strtotime('2026-10-29T12:00:00Z');
        $ran = [
            'the restore did not finish in time', RestoreRecord::STOPPED, 'restic: exit 1', 'exit 3', '',
            // The core's errTargetAppeared (core/internal/restore/webspace.go): restic ran in full.
            "restored folder /var/www/vhosts/example.test/restored-2026-10-29-6779981c appeared in the webspace while the restore ran; nothing was replaced; ask the server's administrator",
            // db.go renameInPlace, after the dump was written.
            '/var/www/vhosts/example.test/restored-2026-10-29-6779981c/example_wp.sql already exists',
        ];
        foreach ($ran as $error) {
            $records = [self::record('files', '2026-10-29T11:00:00Z', ['state' => 'failed', 'error' => $error])];
            self::assertSame(1, RestoreRecord::recentCustomerRestores($records, 'example.test', $now), $error);
        }
    }

    public function testRunningRecordsSayWhoStartedThem(): void
    {
        $args = ['restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', 'abcdef12'];

        self::assertFalse(RestoreRecord::running('20261029120000-abcdef0123456789abcdef01', $args, 1)['by_admin']);
        self::assertTrue(RestoreRecord::running('20261029120000-abcdef0123456789abcdef01', $args, 1, true)['by_admin']);
    }

    public function testThreeRestoresADayStopTheFourthForCustomersOnly(): void
    {
        $state = ['running' => false, 'server_busy' => false, 'restored_folders' => 0, 'recent_restores' => 3];

        self::assertSame(RestoreRecord::TOO_MANY_RESTORES, RestoreRecord::startRefusal($state, 'files', false));
        self::assertSame(RestoreRecord::TOO_MANY_RESTORES, RestoreRecord::startRefusal($state, 'db', false));
        self::assertNull(RestoreRecord::startRefusal($state, 'mail', false));
        self::assertNull(RestoreRecord::startRefusal($state, 'files', true), 'administrators are exempt');
        self::assertNull(RestoreRecord::startRefusal(['recent_restores' => 2] + $state, 'files', false));
        self::assertSame(RestoreRecord::TOO_MANY_RESTORES, RestoreRecord::customerError(RestoreRecord::TOO_MANY_RESTORES));
    }
}

<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use DateTimeZone;
use InvalidArgumentException;
use Noiz\Relical\RestoreRequest;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class RestoreRequestTest extends TestCase
{
    private const MAIL_OLD = '1111111111111111111111111111111111111111111111111111111111111111';
    private const MAIL_NEW = '2222222222222222222222222222222222222222222222222222222222222222';
    private const FILES = 'aaaaaaaabbbbbbbbccccccccddddddddeeeeeeeeffffffff0000000011111111';
    private const DB_WP = 'dddddddd00000000000000000000000000000000000000000000000000000001';
    private const DB_SHOP = 'dddddddd00000000000000000000000000000000000000000000000000000002';

    /** @return list<array<string, mixed>> what `snapshots` prints */
    private static function snapshots(): array
    {
        return [
            ['id' => self::MAIL_OLD, 'time' => '2026-10-27T01:00:05.1+02:00', 'paths' => ['/var/qmail/mailnames/example.test/info/Maildir'], 'tags' => ['mail']],
            ['id' => self::MAIL_NEW, 'time' => '2026-10-28T01:00:05+02:00', 'paths' => [], 'tags' => ['mail']],
            ['id' => self::FILES, 'time' => '2026-10-28T01:02:00+02:00', 'paths' => ['/var/www/vhosts/example.test'], 'tags' => ['files']],
            ['id' => self::DB_WP, 'time' => '2026-10-28T01:03:00+02:00', 'tags' => ['db', 'db:example_wp']],
            ['id' => self::DB_SHOP, 'time' => '2026-10-28T01:03:00+02:00', 'tags' => ['db', 'db:shop']],
            ['id' => 'eeeeeeee00000000', 'time' => '2026-10-28T01:04:00+02:00', 'tags' => ['db']],
            ['id' => 'eeeeeeee00000001', 'time' => '2026-10-28T01:04:00+02:00', 'tags' => ['db', 'db:x`;drop']],
            ['id' => 'ffffffff00000000', 'time' => '2026-10-28T01:05:00+02:00', 'tags' => ['panel-config']],
            ['id' => 'not-hex!', 'time' => '2026-10-28T01:05:00+02:00', 'tags' => ['files']],
            ['id' => 'abababab', 'time' => 'yesterday-ish?', 'tags' => ['files']],
            ['id' => 'abababac', 'tags' => ['files']],
            'junk',
        ];
    }

    /** @return array<string, list<array<string, string>>> */
    private static function points(): array
    {
        return RestoreRequest::restorePoints(self::snapshots(), new DateTimeZone('UTC'));
    }

    /** @return array<string, array{domain: string, mailbox: string}> */
    private static function mailboxes(): array
    {
        return RestoreRequest::mailboxes([
            ['domain' => 'example.test', 'mailbox' => 'info', 'login' => 'info@example.test', 'path' => '/x', 'uid' => 1, 'gid' => 1],
            ['domain' => 'shop.example.test', 'mailbox' => 'sales', 'login' => 'sales@shop.example.test'],
        ]);
    }

    public function testRestorePointsAreGroupedByUnitNewestFirst(): void
    {
        $points = self::points();

        self::assertSame([self::MAIL_NEW, self::MAIL_OLD], array_column($points['mail'], 'id'));
        self::assertSame([self::FILES], array_column($points['files'], 'id'));
        self::assertSame(['example_wp', 'shop'], array_column($points['db'], 'database'));
        self::assertSame('22222222', $points['mail'][0]['short']);
        self::assertSame('2026-10-27 23:00', $points['mail'][0]['label']);
        self::assertSame(['mail', 'files', 'db'], array_keys($points));
    }

    public function testRestorePointsLeaveOutWhatTheyCannotUse(): void
    {
        $ids = array_merge(...array_map(static fn (array $l): array => array_column($l, 'id'), array_values(self::points())));

        self::assertNotContains('eeeeeeee00000000', $ids, 'a db snapshot without a name tag');
        self::assertNotContains('eeeeeeee00000001', $ids, 'a db snapshot with a bad name');
        self::assertNotContains('ffffffff00000000', $ids, 'the panel configuration');
        self::assertNotContains('abababab', $ids, 'a snapshot without a readable time');
        self::assertNotContains('abababac', $ids);
        self::assertCount(5, $ids);
    }

    public function testTheCoresErrorIsPassedOn(): void
    {
        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('repository does not exist');
        RestoreRequest::restorePoints(['error' => 'repository does not exist']);
    }

    public function testMailboxesAreKeyedByAddressAndValidated(): void
    {
        $boxes = RestoreRequest::mailboxes([
            ['domain' => 'shop.example.test', 'mailbox' => 'sales'],
            ['domain' => 'example.test', 'mailbox' => 'info'],
            ['domain' => 'example.test', 'mailbox' => '-x'],
            ['domain' => '_server', 'mailbox' => 'info'],
            ['domain' => 'example.test'],
        ]);

        self::assertSame([
            'info@example.test' => ['domain' => 'example.test', 'mailbox' => 'info'],
            'sales@shop.example.test' => ['domain' => 'shop.example.test', 'mailbox' => 'sales'],
        ], $boxes);
    }

    public function testMailRestoreUsesTheListedMailboxAndRestorePoint(): void
    {
        self::assertSame(
            ['restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'shop.example.test', '--mailbox', 'sales', '--snapshot', self::MAIL_OLD, '--folder', 'INBOX'],
            RestoreRequest::mail('example.test', ['mailbox' => 'sales@shop.example.test', 'snapshot' => self::MAIL_OLD, 'folder' => ' INBOX '], self::mailboxes(), self::points())
        );
        self::assertSame(
            ['restore', '--subscription', 'example.test', '--unit', 'mail', '--domain', 'example.test', '--mailbox', 'info', '--snapshot', self::MAIL_NEW],
            RestoreRequest::mail('example.test', ['mailbox' => 'info@example.test', 'snapshot' => self::MAIL_NEW, 'folder' => ''], self::mailboxes(), self::points())
        );
    }

    /** @return iterable<string, array{array<string, mixed>}> */
    public static function refusedMail(): iterable
    {
        yield 'a mailbox not in the list' => [['mailbox' => 'boss@other.example.test', 'snapshot' => self::MAIL_NEW]];
        yield 'no mailbox' => [['snapshot' => self::MAIL_NEW]];
        yield 'a mailbox as an array' => [['mailbox' => ['info@example.test'], 'snapshot' => self::MAIL_NEW]];
        yield 'a files restore point' => [['mailbox' => 'info@example.test', 'snapshot' => self::FILES]];
        yield 'latest' => [['mailbox' => 'info@example.test', 'snapshot' => 'latest']];
        yield 'an unknown restore point' => [['mailbox' => 'info@example.test', 'snapshot' => '3333333333333333']];
        yield 'a folder that reads as an option' => [['mailbox' => 'info@example.test', 'snapshot' => self::MAIL_NEW, 'folder' => '-x']];
        yield 'a folder with a control character' => [['mailbox' => 'info@example.test', 'snapshot' => self::MAIL_NEW, 'folder' => "In\nbox"]];
    }

    /**
     * @dataProvider refusedMail
     * @param array<string, mixed> $input
     */
    public function testMailRestoreRefuses(array $input): void
    {
        $this->expectException(InvalidArgumentException::class);
        RestoreRequest::mail('example.test', $input, self::mailboxes(), self::points());
    }

    public function testFilesRestoreOfAFolderOrEverything(): void
    {
        self::assertSame(
            ['restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', self::FILES, '--path', 'httpdocs/wp-content'],
            RestoreRequest::files('example.test', ['snapshot' => self::FILES, 'path' => '/httpdocs/wp-content/', 'what' => 'folder'], self::points())
        );
        self::assertSame(
            ['restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', self::FILES],
            RestoreRequest::files('example.test', ['snapshot' => self::FILES, 'path' => 'httpdocs', 'what' => 'all'], self::points())
        );
        self::assertSame(
            ['restore', '--subscription', 'example.test', '--unit', 'files', '--snapshot', self::FILES],
            RestoreRequest::files('example.test', ['snapshot' => self::FILES, 'path' => '', 'what' => 'folder'], self::points())
        );
    }

    /** @return iterable<string, array{array<string, mixed>}> */
    public static function refusedFiles(): iterable
    {
        yield 'a mail restore point' => [['snapshot' => self::MAIL_NEW, 'path' => 'httpdocs']];
        yield 'climbing out' => [['snapshot' => self::FILES, 'path' => 'httpdocs/../../other.example.test']];
        yield 'dot dot' => [['snapshot' => self::FILES, 'path' => '..']];
        yield 'an empty segment' => [['snapshot' => self::FILES, 'path' => 'httpdocs//x']];
        yield 'an option' => [['snapshot' => self::FILES, 'path' => '-x']];
        yield 'a control character' => [['snapshot' => self::FILES, 'path' => "httpdocs\0"]];
    }

    /**
     * @dataProvider refusedFiles
     * @param array<string, mixed> $input
     */
    public function testFilesRestoreRefuses(array $input): void
    {
        $this->expectException(InvalidArgumentException::class);
        RestoreRequest::files('example.test', $input + ['what' => 'folder'], self::points());
    }

    public function testDatabaseComesFromTheRestorePointNotTheForm(): void
    {
        self::assertSame(
            ['restore', '--subscription', 'example.test', '--unit', 'db', '--database', 'shop', '--snapshot', self::DB_SHOP],
            RestoreRequest::database('example.test', ['snapshot' => self::DB_SHOP, 'database' => 'someone_elses'], self::points())
        );
    }

    public function testDatabaseRestoreRefusesAPointThatIsNotADatabase(): void
    {
        $this->expectException(InvalidArgumentException::class);
        RestoreRequest::database('example.test', ['snapshot' => self::FILES], self::points());
    }

    public function testTheServerRepositoryIsNeverBuilt(): void
    {
        foreach ([
            static fn () => RestoreRequest::files('_server', ['snapshot' => self::FILES], self::points()),
            static fn () => RestoreRequest::snapshotsArgs('_server'),
            static fn () => RestoreRequest::mailboxesArgs('_server'),
            static fn () => RestoreRequest::lsArgs('_server', self::FILES, ''),
        ] as $build) {
            try {
                $build();
                self::fail('_server was accepted');
            } catch (InvalidArgumentException $e) {
                self::assertStringContainsString('domain', $e->getMessage());
            }
        }
    }

    public function testReadingArguments(): void
    {
        self::assertSame(['snapshots', '--subscription', 'example.test'], RestoreRequest::snapshotsArgs('example.test'));
        self::assertSame(['mailboxes', '--subscription', 'example.test'], RestoreRequest::mailboxesArgs('example.test'));
        self::assertSame(['ls', '--subscription', 'example.test', '--snapshot', self::FILES], RestoreRequest::lsArgs('example.test', self::FILES, '/'));
        self::assertSame(['ls', '--subscription', 'example.test', '--snapshot', self::FILES, '--path', 'httpdocs/a b'], RestoreRequest::lsArgs('example.test', self::FILES, 'httpdocs/a b/'));
    }

    public function testLsRefusesLatestAndBadPaths(): void
    {
        foreach (['latest', 'ABCDEF12', 'abc'] as $snapshot) {
            try {
                RestoreRequest::lsArgs('example.test', $snapshot, '');
                self::fail($snapshot . ' was accepted');
            } catch (InvalidArgumentException $e) {
                self::assertStringContainsString('restore point', $e->getMessage());
            }
        }
        $this->expectException(InvalidArgumentException::class);
        RestoreRequest::lsArgs('example.test', self::FILES, 'httpdocs/../..');
    }

    public function testListingPutsFoldersFirstAndOnlyOffersSafeFolderLinks(): void
    {
        $listing = RestoreRequest::listing([
            'path' => '/var/www/vhosts/example.test/httpdocs',
            'entries' => [
                ['name' => 'index.php', 'type' => 'file', 'size' => 405, 'mtime' => '2026-10-01T10:00:00Z'],
                ['name' => 'wp-content', 'type' => 'dir', 'size' => 0, 'mtime' => '2026-10-01T10:00:00Z'],
                ['name' => '..', 'type' => 'dir'],
                ['name' => 'a/b', 'type' => 'dir'],
                ['name' => "bad\nname", 'type' => 'dir'],
                ['name' => 'link', 'type' => 'symlink'],
                'junk',
            ],
            'truncated' => true,
        ], 'httpdocs');

        self::assertSame(['..', "bad\nname", 'wp-content', 'index.php', 'link'], array_column($listing['entries'], 'name'));
        self::assertSame(['', '', 'httpdocs/wp-content', '', ''], array_column($listing['entries'], 'browse'));
        self::assertTrue($listing['truncated']);
    }

    public function testListingPassesOnTheCoresError(): void
    {
        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('path /x is not in snapshot');
        RestoreRequest::listing(['error' => 'path /x is not in snapshot abcdef12'], '');
    }

    public function testBreadcrumb(): void
    {
        self::assertSame([['name' => '', 'path' => '']], RestoreRequest::breadcrumb(''));
        self::assertSame(
            [['name' => '', 'path' => ''], ['name' => 'httpdocs', 'path' => 'httpdocs'], ['name' => 'wp-content', 'path' => 'httpdocs/wp-content']],
            RestoreRequest::breadcrumb('/httpdocs/wp-content/')
        );
    }

    public function testRestoreIdCarriesItsStartTime(): void
    {
        $now = 1793275200; // 2026-10-29 10:40:00 UTC
        $id = RestoreRequest::newId($now, str_repeat("\xab", 12));

        self::assertSame(gmdate('YmdHis', $now) . '-' . str_repeat('ab', 12), $id);
        self::assertSame($now, RestoreRequest::startedAt($id));
        self::assertNull(RestoreRequest::startedAt('../../etc/passwd'));
    }

    public function testRestoreIdNeedsTwelveRandomBytes(): void
    {
        $this->expectException(InvalidArgumentException::class);
        RestoreRequest::newId(0, 'short');
    }
}

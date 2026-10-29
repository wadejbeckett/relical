<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\RestoreRecord;
use Noiz\Relical\RestoreStore;
use PHPUnit\Framework\TestCase;

final class RestoreStoreTest extends TestCase
{
    private const ID = '20261029104000-0123456789abcdef01234567';

    private string $dir;

    protected function setUp(): void
    {
        $this->dir = sys_get_temp_dir() . '/relical-restore-store-' . bin2hex(random_bytes(4));
    }

    protected function tearDown(): void
    {
        foreach (glob($this->dir . '/{,.}*', GLOB_BRACE) ?: [] as $file) {
            if (is_file($file) || is_link($file)) {
                unlink($file);
            }
        }
        if (is_dir($this->dir)) {
            rmdir($this->dir);
        }
    }

    public function testRecordsAreWrittenRootOnlyAndReadBack(): void
    {
        $store = new RestoreStore($this->dir);
        $store->ensure();
        $record = RestoreRecord::running(self::ID, ['restore', '--subscription', 'example.test', '--unit', 'db', '--database', 'example_wp'], 1793275200);
        $store->write($record);

        self::assertSame(0700, fileperms($this->dir) & 0777);
        self::assertSame(0600, fileperms($store->recordPath(self::ID)) & 0777);
        self::assertSame($record, $store->read(self::ID));
        self::assertNull($store->read('20261029104000-ffffffffffffffffffffffff'));
    }

    public function testNamesAreValidatedBeforeAnyPathIsBuilt(): void
    {
        $store = new RestoreStore($this->dir);
        foreach ([static fn () => $store->recordPath('../../etc/passwd'), static fn () => $store->lockPath('../x'), static fn () => $store->lockPath('_server')] as $path) {
            try {
                $path();
                self::fail('accepted');
            } catch (InvalidArgumentException $e) {
                self::assertNotSame('', $e->getMessage());
            }
        }
    }

    public function testASymlinkedRecordIsNotRead(): void
    {
        $store = new RestoreStore($this->dir);
        $store->ensure();
        file_put_contents($this->dir . '/target', '{"id": "x"}');
        symlink($this->dir . '/target', $store->recordPath(self::ID));

        self::assertNull($store->read(self::ID));
    }

    public function testTheLockStopsASecondRestoreOfTheSameSubscription(): void
    {
        $store = new RestoreStore($this->dir);
        self::assertFalse($store->isRunning('example.test'), 'no directory yet: nothing runs');
        $store->ensure();

        $lock = $store->lock('example.test');
        self::assertNotNull($lock);
        self::assertSame(0600, fileperms($store->lockPath('example.test')) & 0777);
        self::assertNull($store->lock('example.test'));
        self::assertTrue($store->isRunning('example.test'));
        self::assertFalse($store->isRunning('other.example.test'));

        RestoreStore::unlock($lock);
        self::assertFalse($store->isRunning('example.test'));
    }

    public function testPruneRemovesOnlyOldRecords(): void
    {
        $store = new RestoreStore($this->dir);
        $store->ensure();
        $old = '20260901000000-0123456789abcdef01234567';
        $store->write(['id' => $old]);
        $store->write(['id' => self::ID]);
        $now = time();
        touch($store->recordPath($old), $now - RestoreStore::MAX_AGE - 10);

        self::assertSame(1, $store->prune($now));
        self::assertNull($store->read($old));
        self::assertNotNull($store->read(self::ID));
    }

    public function testTheServerRunsAtMostTwoRestores(): void
    {
        $store = new RestoreStore($this->dir);
        $store->ensure();

        $first = $store->slot();
        $second = $store->slot();
        self::assertNotNull($first);
        self::assertNotNull($second);
        self::assertTrue($store->serverBusy());
        // A third subscription gets its own lock but no slot.
        $third = $store->lock('third.example.test');
        self::assertNotNull($third);
        self::assertNull($store->slot());

        RestoreStore::unlock($first);
        self::assertFalse($store->serverBusy());
        $again = $store->slot();
        self::assertNotNull($again);
        foreach ([$second, $third, $again] as $lock) {
            RestoreStore::unlock($lock);
        }
    }

    public function testAskingWhetherARestoreRunsTakesNoLock(): void
    {
        $asked = 0;
        $store = new RestoreStore($this->dir, static function () use (&$asked): string {
            $asked++;
            return (string) file_get_contents('/proc/locks');
        });
        $store->ensure();
        $lock = $store->lock('example.test');
        self::assertNotNull($lock);
        RestoreStore::unlock($lock);

        self::assertFalse($store->isRunning('example.test'));
        self::assertSame(1, $asked, 'answered from /proc/locks');
        self::assertFalse($store->serverBusy(), 'no slot file yet: not busy');
    }

    public function testASecondReadOfTheSameSubscriptionIsRefused(): void
    {
        $store = new RestoreStore($this->dir);
        $store->ensure();

        $read = $store->readLock('example.test');
        self::assertNotNull($read);
        self::assertNull($store->readLock('example.test'));
        $other = $store->readLock('other.example.test');
        self::assertNotNull($other);
        self::assertNotNull($store->lock('example.test'), 'a read does not block a restore');

        RestoreStore::unlock($read);
        RestoreStore::unlock($other);
    }

    public function testTheCacheIsReusedForFiveMinutesAndOnlyForItsSubscription(): void
    {
        $store = new RestoreStore($this->dir);
        $store->ensure();
        $now = 1793275200;

        self::assertNull($store->cached('example.test', 'snapshots', $now));
        $store->cache('example.test', 'snapshots', '[{"id":"abcdef12"}]', $now);
        self::assertSame(0600, fileperms($store->cachePath('example.test', 'snapshots')) & 0777);
        self::assertSame('[{"id":"abcdef12"}]', $store->cached('example.test', 'snapshots', $now + 299));
        self::assertNull($store->cached('example.test', 'snapshots', $now + 300), 'stale');
        self::assertNull($store->cached('example.test', 'snapshots', $now - 10), 'from the future');
        self::assertNull($store->cached('example.test', 'mailboxes', $now));
        self::assertNull($store->cached('other.example.test', 'snapshots', $now));

        // An entry stored for another subscription is never returned, even under this name.
        copy($store->cachePath('example.test', 'snapshots'), $store->cachePath('other.example.test', 'snapshots'));
        self::assertNull($store->cached('other.example.test', 'snapshots', $now));
    }

    public function testOnlySnapshotsAndMailboxesAreCached(): void
    {
        $this->expectException(InvalidArgumentException::class);
        (new RestoreStore($this->dir))->cachePath('example.test', 'ls');
    }

    public function testClearingTheCache(): void
    {
        $store = new RestoreStore($this->dir);
        $store->ensure();
        $store->cache('example.test', 'snapshots', '[]', 1);
        $store->cache('example.test', 'mailboxes', '[]', 1);
        $store->cache('other.example.test', 'snapshots', '[]', 1);

        self::assertSame(2, $store->clearCache('example.test'));
        self::assertSame(1, $store->clearCache());
        self::assertSame([], glob($this->dir . '/*.cache'));
    }

    public function testRestoredFoldersAreCountedByName(): void
    {
        mkdir($this->dir, 0700);
        $home = $this->dir . '/home';
        mkdir($home);
        mkdir($home . '/httpdocs');
        mkdir($home . '/restored-2026-10-27-11111111');
        mkdir($home . '/restored-2026-10-28-22222222');
        touch($home . '/restored-notes.txt');
        symlink('/', $home . '/restored-link');

        self::assertSame(4, RestoreStore::restoredFolders($home));
        self::assertSame(0, RestoreStore::restoredFolders($this->dir . '/none'));

        foreach (['restored-2026-10-27-11111111', 'restored-2026-10-28-22222222', 'httpdocs'] as $d) {
            rmdir($home . '/' . $d);
        }
        unlink($home . '/restored-notes.txt');
        unlink($home . '/restored-link');
        rmdir($home);
    }
}

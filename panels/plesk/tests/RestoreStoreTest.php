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
}

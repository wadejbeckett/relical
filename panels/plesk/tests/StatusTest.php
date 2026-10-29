<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use DateTimeZone;
use Noiz\Relical\Status;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class StatusTest extends TestCase
{
    /** `relical status` output in the core's shape: subscription -> unit -> action -> result. */
    private const SAMPLE = <<<'JSON'
{
  "example.test": {
    "mail": {"backup": {"subscription": "example.test", "unit": "mail", "action": "backup", "time": "2026-10-28T01:05:00.123456789Z", "ok": true, "snapshot": "abcdef12"}},
    "files": {"backup": {"subscription": "example.test", "unit": "files", "action": "backup", "time": "2026-10-28T01:20:00Z", "ok": true}},
    "db:example_wp": {"backup": {"subscription": "example.test", "unit": "db", "database": "example_wp", "action": "backup", "time": "2026-10-28T01:21:00Z", "ok": false, "error": "mysqldump: exit 2"}},
    "repository": {
      "forget": {"subscription": "example.test", "action": "forget", "time": "2026-10-28T01:22:00Z", "ok": true},
      "stats": {"subscription": "example.test", "action": "stats", "time": "2026-10-28T01:23:00Z", "ok": true, "stored_bytes": 1610612736}
    }
  },
  "_server": {
    "panel-config": {"backup": {"subscription": "_server", "unit": "panel-config", "action": "backup", "time": "2026-10-28T02:00:00Z", "ok": true}}
  },
  "another.example.test": {
    "mail": {"backup": {"subscription": "another.example.test", "unit": "mail", "action": "backup", "time": "2026-10-27T01:00:00Z", "ok": false, "error": "repository locked"}},
    "repository": {"check": {"subscription": "another.example.test", "action": "check", "time": "2026-10-28T13:00:00Z", "ok": false, "error": "pack damaged"}}
  }
}
JSON;

    public function testRowsFromSampleStatus(): void
    {
        $rows = Status::rows(Status::parse(self::SAMPLE), new DateTimeZone('UTC'));

        self::assertSame(['another.example.test', 'example.test', '_server'], array_column($rows, 'subscription'));

        $example = $rows[1];
        self::assertSame([
            ['unit' => 'mail', 'ok' => true, 'time' => '2026-10-28 01:05'],
            ['unit' => 'files', 'ok' => true, 'time' => '2026-10-28 01:20'],
            ['unit' => 'db:example_wp', 'ok' => false, 'time' => '2026-10-28 01:21'],
        ], $example['units']);
        self::assertSame('2026-10-28 01:20', $example['last_success']);
        self::assertSame('db:example_wp backup: mysqldump: exit 2 (2026-10-28 01:21)', $example['last_error']);
        self::assertSame(1610612736, $example['stored_bytes']);

        $another = $rows[0];
        self::assertSame('', $another['last_success']);
        self::assertSame('check: pack damaged (2026-10-28 13:00); mail backup: repository locked (2026-10-27 01:00)', $another['last_error']);
        self::assertNull($another['stored_bytes']);

        self::assertSame([['unit' => 'panel-config', 'ok' => true, 'time' => '2026-10-28 02:00']], $rows[2]['units']);
    }

    public function testTimesAreShownInTheServerZone(): void
    {
        $rows = Status::rows(Status::parse(self::SAMPLE), new DateTimeZone('Africa/Johannesburg'));

        self::assertSame('2026-10-28 03:20', $rows[1]['last_success']);
    }

    public function testNoRunsYetIsAnEmptyTable(): void
    {
        self::assertSame([], Status::rows(Status::parse("{}\n")));
    }

    public function testTheCoresErrorIsRaised(): void
    {
        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage('config: open /etc/relical/config.json: no such file or directory');
        Status::parse('{"error": "config: open /etc/relical/config.json: no such file or directory"}');
    }

    public function testOutputThatIsNotJsonIsRefused(): void
    {
        $this->expectException(RuntimeException::class);
        Status::parse('usage: relical ...');
    }

    public function testEntriesOfTheWrongShapeAreDropped(): void
    {
        $state = Status::parse('{"example.test": {"mail": "x", "files": {"backup": 1, "check": {"ok": true}}}, "x": 3}');

        self::assertSame(['example.test' => ['files' => ['check' => ['ok' => true]]]], $state);
    }

    public function testBytes(): void
    {
        self::assertSame('', Status::bytes(null));
        self::assertSame('0 B', Status::bytes(0));
        self::assertSame('1023 B', Status::bytes(1023));
        self::assertSame('1.5 KiB', Status::bytes(1536));
        self::assertSame('1.5 GiB', Status::bytes(1610612736));
    }

    public function testLockHeldReadsProcLocks(): void
    {
        $locks = "1: POSIX  ADVISORY  WRITE 812 00:1a:131074 0 EOF\n"
            . "2: FLOCK  ADVISORY  WRITE 4242 08:01:524301 0 EOF\n"
            . "2: -> FLOCK  ADVISORY  WRITE 4250 08:01:524302 0 EOF\n";

        self::assertTrue(Status::lockHeld($locks, 524301));
        self::assertTrue(Status::lockHeld($locks, 524302), 'a waiter also means a run is there');
        self::assertFalse(Status::lockHeld($locks, 131074), 'a POSIX lock is not the run lock');
        self::assertFalse(Status::lockHeld($locks, 52430));
        self::assertFalse(Status::lockHeld('', 524301));
    }
}

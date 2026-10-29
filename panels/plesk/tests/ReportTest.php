<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\Report;
use PHPUnit\Framework\TestCase;

final class ReportTest extends TestCase
{
    public function testUnknownCommandMeansNotAvailableYet(): void
    {
        $report = Report::fromCli(2, '', "unknown command report\nusage: relical [-config PATH] COMMAND [flags]\n");

        self::assertFalse($report['available']);
        self::assertStringContainsString('no report yet', $report['message']);
    }

    public function testTheCoresErrorIsShown(): void
    {
        $report = Report::fromCli(1, '{"error": "state.json: unexpected end of JSON input"}', '');

        self::assertFalse($report['available']);
        self::assertSame('The report could not be made: state.json: unexpected end of JSON input', $report['message']);
    }

    /** tests/fixtures/report.json is the core's own output (core/internal/job TestReportSample). */
    public function testTheCoresReportShape(): void
    {
        $report = Report::fromCli(0, (string) file_get_contents(__DIR__ . '/fixtures/report.json'), '');

        self::assertTrue($report['available']);
        self::assertSame('2026-10-29T06:00:00Z', $report['generated']);
        self::assertSame([
            ['label' => 'Subscriptions', 'value' => '3'],
            ['label' => 'Succeeded', 'value' => '4'],
            ['label' => 'Failed', 'value' => '2'],
            ['label' => 'Skipped', 'value' => '2'],
            ['label' => 'Units skipped', 'value' => '1'],
            ['label' => 'Stored', 'value' => '1.5 KiB'],
        ], $report['summary']);
        self::assertSame([
            'example.test db:example_wp: mysqldump: exit 2 (2026-10-29T01:03:00Z)',
            'bad.example.test: invalid inventory entry: databases[0].name: "phpmyadmin_old" is a server or panel database and never belongs to a subscription',
        ], $report['failures']);
        self::assertSame([
            'new.example.test was old.example.test; the old repository is kept (renamed from old.example.test: backups now go to a new repository for new.example.test; the repository for old.example.test is kept as it is and is no longer backed up, pruned or checked)',
        ], $report['renamed']);
        self::assertSame([
            'bad.example.test: invalid inventory entry: databases[0].name: "phpmyadmin_old" is a server or panel database and never belongs to a subscription',
            'mailonly.example.test: nothing to back up: the subscription has no system user (no web hosting)',
        ], $report['skipped']);
        self::assertSame(2, $report['skipped_count']);
        self::assertSame(1, $report['skipped_rows']);
        self::assertSame(2, $report['failed_count']);
        self::assertSame([
            'other.example.test files: the last backup (2026-10-26T02:00:00Z) is older than 48 hours',
            'other.example.test: the last check (2026-10-18T06:00:00Z) is older than 10 days',
        ], $report['warnings']);
    }

    public function testAnInventoryRowWithoutAnErrorSaysInvalid(): void
    {
        $report = Report::fromCli(0, (string) json_encode(['rows' => [['subscription' => 'bad.example.test', 'unit' => 'inventory', 'ok' => false, 'time' => '2026-10-29T06:00:00Z', 'error' => '']]]), '');

        self::assertSame(['bad.example.test: invalid inventory entry'], $report['failures']);
    }

    public function testASkippedEntryWithoutAReasonShowsItsName(): void
    {
        $report = Report::fromCli(0, (string) json_encode(['skipped' => [['name' => 'a.test', 'reason' => '']]]), '');

        self::assertSame(['a.test'], $report['skipped']);
    }

    public function testAnOlderCoresSkippedCountIsStillRead(): void
    {
        $report = Report::fromCli(0, (string) json_encode(['skipped' => 4, 'failed' => 0, 'rows' => []]), '');

        self::assertSame([], $report['skipped']);
        self::assertSame(4, $report['skipped_count']);
        self::assertSame(0, $report['skipped_rows']);
        self::assertContains(['label' => 'Skipped', 'value' => '4'], $report['summary']);
    }

    public function testMalformedSkippedEntriesAreLeftOut(): void
    {
        $report = Report::fromCli(0, (string) json_encode(['skipped' => [['name' => 'a.test', 'reason' => 'why'], ['name' => 3], 'text', ['reason' => 'no name']]]), '');

        self::assertSame(['a.test: why'], $report['skipped']);
        self::assertSame(1, $report['skipped_count']);
    }

    public function testWarningsThatAreNotTextAreSkipped(): void
    {
        $report = Report::fromCli(0, (string) json_encode(['warnings' => ['stale', 3, null, '', ['x']], 'rows' => []]), '');

        self::assertSame(['stale'], $report['warnings']);
    }

    public function testAFailedRowWithoutAnErrorSaysFailed(): void
    {
        $report = Report::fromCli(0, (string) json_encode(['rows' => [['subscription' => 'other.example.test', 'unit' => 'files', 'ok' => false, 'time' => '', 'error' => '']]]), '');

        self::assertSame(['other.example.test files: failed'], $report['failures']);
    }

    public function testGarbageIsNotAReport(): void
    {
        $report = Report::fromCli(1, 'panic: runtime error', '');

        self::assertFalse($report['available']);
        self::assertSame('The report could not be read (exit 1).', $report['message']);
    }
}

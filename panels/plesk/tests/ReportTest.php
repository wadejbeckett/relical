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

    public function testTheCoresReportShape(): void
    {
        $report = Report::fromCli(0, (string) file_get_contents(__DIR__ . '/fixtures/report.json'), '');

        self::assertTrue($report['available']);
        self::assertSame('2026-10-29T06:00:00Z', $report['generated']);
        self::assertSame([
            ['label' => 'Subscriptions', 'value' => '3'],
            ['label' => 'Succeeded', 'value' => '7'],
            ['label' => 'Failed', 'value' => '1'],
            ['label' => 'Skipped', 'value' => '0'],
            ['label' => 'Stored', 'value' => '1.5 KiB'],
        ], $report['summary']);
        self::assertSame(['example.test db:example_wp: mysqldump: exit 2 (2026-10-29T01:03:00Z)'], $report['failures']);
        self::assertSame(['new.example.test was old.example.test; the old repository is kept (backups go to a new repository; the old one is kept)'], $report['renamed']);
        self::assertSame([
            'other.example.test files: the last backup (2026-10-26T01:02:00Z) is older than 48 hours',
            'example.test: backed up since 2026-10-20T01:00:00Z but never checked',
        ], $report['warnings']);
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

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

    public function testSummaryFailuresAndRenames(): void
    {
        $json = json_encode([
            'generated' => '2026-10-28T02:10:00Z',
            'subscriptions' => 3, 'ok' => 7, 'failed' => 1, 'skipped' => 0,
            'stored_bytes' => 1536,
            'rows' => [
                ['subscription' => 'example.test', 'unit' => 'mail', 'ok' => true, 'time' => '2026-10-28T01:00:09Z', 'error' => '', 'stored_bytes' => 1024],
                ['subscription' => 'example.test', 'unit' => 'db:example_wp', 'ok' => false, 'time' => '2026-10-28T01:03:00Z', 'error' => 'mysqldump: exit 2', 'stored_bytes' => 0],
                ['subscription' => 'other.example.test', 'unit' => 'files', 'ok' => false, 'time' => '', 'error' => ''],
            ],
            'renamed' => [['name' => 'new.example.test', 'renamed_from' => 'old.example.test']],
        ]);

        $report = Report::fromCli(0, (string) $json, '');

        self::assertTrue($report['available']);
        self::assertSame('2026-10-28T02:10:00Z', $report['generated']);
        self::assertSame([
            ['label' => 'Subscriptions', 'value' => '3'],
            ['label' => 'Succeeded', 'value' => '7'],
            ['label' => 'Failed', 'value' => '1'],
            ['label' => 'Skipped', 'value' => '0'],
            ['label' => 'Stored', 'value' => '1.5 KiB'],
        ], $report['summary']);
        self::assertSame(['example.test db:example_wp: mysqldump: exit 2 (2026-10-28T01:03:00Z)', 'other.example.test files: failed'], $report['failures']);
        self::assertSame(['new.example.test was old.example.test; the old repository is kept'], $report['renamed']);
    }

    public function testGarbageIsNotAReport(): void
    {
        $report = Report::fromCli(1, 'panic: runtime error', '');

        self::assertFalse($report['available']);
        self::assertSame('The report could not be read (exit 1).', $report['message']);
    }
}

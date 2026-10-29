<?php

declare(strict_types=1);

namespace Noiz\Relical;

use Throwable;

/**
 * The "last report" block, from `relical report` (the nightly summary:
 * counts, failures, stored bytes and per-subscription rows). The command
 * arrives with core Task 5; until then the core answers "unknown command"
 * and the block says the report is not available yet.
 *
 * The shape is core/internal/job/report.go's Report (TestReportShape pins
 * the keys there; tests/fixtures/report.json is that core's own output for
 * a sample state, written by its TestReportSample):
 *
 *     {generated, subscriptions, ok, failed, skipped_rows, stored_bytes,
 *      rows: [{subscription, unit, ok, time, error, stored_bytes}],
 *      renamed: [{name, renamed_from, note}],
 *      skipped: [{name, reason}],
 *      warnings: [string]}
 *
 * `skipped` is the inventory's list of subscriptions left out and why: by
 * the adapter, or by validation (an invalid inventory entry, which also
 * has a failed row with unit `inventory`; its time is the report's, so it
 * is not shown). An older core sends a count there instead, and both are
 * read. `skipped_rows` counts the units that were skipped rather than
 * backed up (a database of a type the core cannot dump). Warnings are a
 * stale backup or check, or an inventory that could not be loaded.
 * Unknown keys are ignored.
 */
final class Report
{
    /** The summary counters shown, in order, with their labels. */
    private const SUMMARY = [
        'subscriptions' => 'Subscriptions',
        'ok' => 'Succeeded',
        'failed' => 'Failed',
        'skipped' => 'Skipped',
        'skipped_rows' => 'Units skipped',
    ];

    /** The unit of a failed row for an invalid inventory entry (core job.InventoryUnit). */
    public const INVENTORY_UNIT = 'inventory';

    /**
     * @return array{available: bool, message: string, generated: string, summary: list<array{label: string, value: string}>, warnings: list<string>, failures: list<string>, renamed: list<string>, skipped: list<string>, skipped_count: int, skipped_rows: int, failed_count: int}
     */
    public static function fromCli(int $code, string $stdout, string $stderr): array
    {
        $empty = ['available' => false, 'message' => '', 'generated' => '', 'summary' => [], 'warnings' => [], 'failures' => [], 'renamed' => [], 'skipped' => [], 'skipped_count' => 0, 'skipped_rows' => 0, 'failed_count' => 0];

        if ($code === 2 && str_contains($stderr, 'unknown command')) {
            return ['message' => 'This version of the Relical core has no report yet.'] + $empty;
        }
        try {
            $data = json_decode($stdout, true, 32, JSON_THROW_ON_ERROR);
        } catch (Throwable $e) {
            $data = null;
        }
        if (!is_array($data)) {
            return ['message' => sprintf('The report could not be read (exit %d).', $code)] + $empty;
        }
        if (isset($data['error']) && is_string($data['error'])) {
            return ['message' => 'The report could not be made: ' . $data['error']] + $empty;
        }

        $skipped = [];
        if (is_array($data['skipped'] ?? null)) {
            foreach ($data['skipped'] as $entry) {
                if (is_array($entry) && is_string($entry['name'] ?? null) && is_string($entry['reason'] ?? null)) {
                    $skipped[] = implode(': ', array_filter([$entry['name'], $entry['reason']], static fn (string $s): bool => $s !== ''));
                }
            }
            $data['skipped'] = count($skipped);
        }

        $summary = [];
        foreach (self::SUMMARY as $key => $label) {
            if (is_int($data[$key] ?? null)) {
                $summary[] = ['label' => $label, 'value' => (string) $data[$key]];
            }
        }
        if (is_int($data['stored_bytes'] ?? null)) {
            $summary[] = ['label' => 'Stored', 'value' => Status::bytes($data['stored_bytes'])];
        }

        $failures = [];
        foreach (is_array($data['rows'] ?? null) ? $data['rows'] : [] as $row) {
            if (!is_array($row) || ($row['ok'] ?? true) !== false) {
                continue;
            }
            // An invalid inventory entry: the error says so, and the time is the report's own.
            $entry = ($row['unit'] ?? null) === self::INVENTORY_UNIT;
            $what = implode(' ', array_filter([
                is_string($row['subscription'] ?? null) ? $row['subscription'] : '',
                is_string($row['unit'] ?? null) && !$entry ? $row['unit'] : '',
            ], static fn (string $s): bool => $s !== ''));
            $error = is_string($row['error'] ?? null) && $row['error'] !== '' ? $row['error'] : ($entry ? 'invalid inventory entry' : 'failed');
            if (!$entry && is_string($row['time'] ?? null) && $row['time'] !== '') {
                $error .= ' (' . $row['time'] . ')';
            }
            $failures[] = ($what !== '' ? $what . ': ' : '') . $error;
        }

        $renamed = [];
        foreach (is_array($data['renamed'] ?? null) ? $data['renamed'] : [] as $entry) {
            if (is_array($entry) && is_string($entry['name'] ?? null) && is_string($entry['renamed_from'] ?? null)) {
                $renamed[] = sprintf('%s was %s; the old repository is kept', $entry['name'], $entry['renamed_from'])
                    . (is_string($entry['note'] ?? null) && $entry['note'] !== '' ? ' (' . $entry['note'] . ')' : '');
            } elseif (is_string($entry)) {
                $renamed[] = $entry;
            }
        }

        return [
            'available' => true,
            'message' => '',
            'generated' => is_string($data['generated'] ?? null) ? $data['generated'] : '',
            'summary' => $summary,
            'warnings' => array_values(array_filter(is_array($data['warnings'] ?? null) ? $data['warnings'] : [], static fn ($w): bool => is_string($w) && $w !== '')),
            'failures' => $failures,
            'renamed' => $renamed,
            'skipped' => $skipped,
            'skipped_count' => is_int($data['skipped'] ?? null) ? max(0, $data['skipped']) : 0,
            'skipped_rows' => is_int($data['skipped_rows'] ?? null) ? max(0, $data['skipped_rows']) : 0,
            'failed_count' => is_int($data['failed'] ?? null) ? max(0, $data['failed']) : count($failures),
        ];
    }
}

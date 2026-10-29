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
 * The shape is the one core Task 5 defines (plan
 * docs/plans/2026-10-23-phase-2-full-backup-plesk-extension.md, Task 5;
 * Task 8 review finding 8):
 *
 *     {generated, subscriptions, ok, failed, skipped, stored_bytes,
 *      rows: [{subscription, unit, ok, time, error, stored_bytes}],
 *      renamed: [{name, renamed_from, note}],
 *      warnings: [string]}
 *
 * as core/internal/job/report.go builds it (TestReportShape pins the keys
 * there; tests/fixtures/report.json is a copy of that shape). Warnings are
 * a stale backup or check, or an inventory that could not be loaded.
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
    ];

    /**
     * @return array{available: bool, message: string, generated: string, summary: list<array{label: string, value: string}>, warnings: list<string>, failures: list<string>, renamed: list<string>}
     */
    public static function fromCli(int $code, string $stdout, string $stderr): array
    {
        $empty = ['available' => false, 'message' => '', 'generated' => '', 'summary' => [], 'warnings' => [], 'failures' => [], 'renamed' => []];

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
            $what = implode(' ', array_filter([
                is_string($row['subscription'] ?? null) ? $row['subscription'] : '',
                is_string($row['unit'] ?? null) ? $row['unit'] : '',
            ], static fn (string $s): bool => $s !== ''));
            $error = is_string($row['error'] ?? null) && $row['error'] !== '' ? $row['error'] : 'failed';
            if (is_string($row['time'] ?? null) && $row['time'] !== '') {
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
        ];
    }
}

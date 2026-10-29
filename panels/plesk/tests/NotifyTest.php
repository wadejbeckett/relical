<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\Notify;
use PHPUnit\Framework\TestCase;
use SimpleXMLElement;

final class NotifyTest extends TestCase
{
    private const NOW = 1793235600; // 2026-10-29T01:00:00Z

    public function testOnlyTheTwoServicesAreReported(): void
    {
        self::assertSame('relical-backup.service', Notify::unit('relical-backup.service'));
        self::assertSame('relical-check.service', Notify::unit('relical-check.service'));
        foreach (['', 'relical-notify@x.service', 'sshd.service', '../x', 'relical-backup.service; id'] as $bad) {
            try {
                Notify::unit($bad);
                self::fail($bad . ' was accepted');
            } catch (InvalidArgumentException $e) {
                self::assertStringContainsString('notify.php takes', $e->getMessage());
            }
        }
    }

    public function testLogLineAndRecord(): void
    {
        self::assertSame(
            "2026-10-29T01:00:00+00:00 relical-notify: relical-backup.service failed; see journalctl -u relical-backup.service\n",
            Notify::logLine('relical-backup.service', self::NOW)
        );
        $record = Notify::record('relical-check.service', self::NOW);
        self::assertSame(['unit' => 'relical-check.service', 'time' => '2026-10-29T01:00:00+00:00'], $record);
        self::assertSame($record, Notify::parseRecord(json_decode((string) json_encode($record), true)));
    }

    public function testMalformedRecordsReadAsNone(): void
    {
        foreach ([null, 'x', [], ['unit' => 'sshd.service', 'time' => '2026-10-29T01:00:00Z'], ['unit' => 'relical-backup.service', 'time' => 'never'], ['unit' => 'relical-backup.service']] as $bad) {
            self::assertNull(Notify::parseRecord($bad));
        }
    }

    public function testMessageNamesTheRunTheHostAndTheLogTail(): void
    {
        $tail = array_map(static fn (int $i): string => 'line ' . $i, range(1, 40));
        $mail = Notify::message('relical-backup.service', "host.example.test\nBcc: x@y", self::NOW, $tail);

        self::assertSame('Relical: the nightly backup failed on host.example.testBccxy', $mail['subject']);
        self::assertStringNotContainsString("\n", $mail['subject']);
        self::assertStringContainsString('relical-backup.service', $mail['body']);
        self::assertStringContainsString('2026-10-29 01:00 UTC', $mail['body']);
        self::assertStringContainsString('journalctl -u relical-backup.service', $mail['body']);
        self::assertStringContainsString("line 40\n", $mail['body']);
        self::assertStringNotContainsString("line 10\n", $mail['body'], 'only the last 30 lines');
        self::assertStringContainsString('weekly repository check', Notify::message('relical-check.service', 'h.test', self::NOW, [])['subject']);
        self::assertStringContainsString('The run log is empty.', Notify::message('relical-check.service', 'h.test', self::NOW, [])['body']);
    }

    public function testTheAdministratorAddressComesFromApiRpc(): void
    {
        $ok = new SimpleXMLElement('<packet><server><get><result><status>ok</status><admin><admin_cname>Example</admin_cname><admin_email>admin@example.test</admin_email></admin></result></get></server></packet>');
        self::assertSame('admin@example.test', Notify::adminEmail($ok));

        $none = new SimpleXMLElement('<packet><server><get><result><status>error</status></result></get></server></packet>');
        self::assertSame('', Notify::adminEmail($none));
        self::assertSame('', Notify::adminEmail(null));
    }

    public function testOnlyOnePlainAddressIsAccepted(): void
    {
        self::assertSame('a@example.test', Notify::validEmail(' a@example.test '));
        foreach (['', 'nobody', "a@example.test\nBcc: b@example.test", 'a@example.test, b@example.test', '<a@example.test>', 'a b@example.test'] as $bad) {
            self::assertSame('', Notify::validEmail($bad), $bad);
        }
    }
}

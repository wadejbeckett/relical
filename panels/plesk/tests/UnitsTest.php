<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use InvalidArgumentException;
use Noiz\Relical\Units;
use PHPUnit\Framework\TestCase;

final class UnitsTest extends TestCase
{
    private const TEMPLATES = __DIR__ . '/../plib/resources/systemd';

    /** @return array<string, string> */
    private static function rendered(string $time = '01:00'): array
    {
        return Units::renderAll(Units::loadTemplates(self::TEMPLATES), $time);
    }

    public function testRendersAllFourUnitsWithNoPlaceholderLeft(): void
    {
        $units = self::rendered('03:15');

        self::assertSame(Units::ALL, array_keys($units));
        foreach ($units as $name => $unit) {
            self::assertDoesNotMatchRegularExpression('/\{[a-z_]+\}/', $unit, $name);
        }
        self::assertStringContainsString("OnCalendar=*-*-* 03:15:00\n", $units[Units::BACKUP_TIMER]);
        self::assertStringContainsString("OnCalendar=Sun *-*-* 15:15:00\n", $units[Units::CHECK_TIMER]);
    }

    public function testTimersAreRandomisedAndPersistent(): void
    {
        foreach (Units::TIMERS as $name) {
            $unit = self::rendered()[$name];
            self::assertStringContainsString("RandomizedDelaySec=30min\n", $unit, $name);
            self::assertStringContainsString("Persistent=true\n", $unit, $name);
            self::assertStringContainsString("WantedBy=timers.target\n", $unit, $name);
        }
    }

    public function testServicesRunTheInstalledBinaryByAbsolutePath(): void
    {
        $units = self::rendered();
        $backup = $units[Units::BACKUP_SERVICE];
        $check = $units[Units::CHECK_SERVICE];

        self::assertStringContainsString("ExecStart=/usr/local/lib/relical/relical -config /etc/relical/config.json backup --all\n", $backup);
        self::assertStringContainsString("ExecStart=/usr/local/lib/relical/relical -config /etc/relical/config.json check --all --read-data-subset 5%%\n", $check);
        foreach ($units as $name => $unit) {
            self::assertStringNotContainsString('/usr/local/bin/relical', $unit, $name);
            preg_match_all('/^Exec[A-Za-z]*=(.*)$/m', $unit, $m);
            foreach ($m[1] as $command) {
                self::assertStringStartsWith('/', $command, $name);
            }
        }
        foreach (Units::SERVICES as $name) {
            self::assertStringContainsString("ConditionPathExists=/etc/relical/config.json\n", $units[$name], $name);
        }
    }

    public function testRetentionRunsInsideTheBackupAndStopsAreBounded(): void
    {
        $units = self::rendered();

        self::assertStringNotContainsString('ExecStopPost', $units[Units::BACKUP_SERVICE], 'a stop or reboot must not start a prune');
        self::assertDoesNotMatchRegularExpression('/^Exec.*forget/m', $units[Units::BACKUP_SERVICE]);
        foreach (Units::SERVICES as $name) {
            self::assertStringNotContainsString('TimeoutStopSec=infinity', $units[$name], $name);
            self::assertStringContainsString("\nTimeoutStopSec=15min\n", $units[$name], $name);
            self::assertStringContainsString("\nRuntimeMaxSec=20h\n", $units[$name], $name);
        }
    }

    public function testServicesYieldCpuAndDiskLikeTheCoreUnits(): void
    {
        $units = self::rendered();
        foreach (Units::SERVICES as $name) {
            $service = substr($units[$name], strpos($units[$name], "[Service]\n"));
            self::assertStringContainsString("\nNice=19\n", $service, $name);
            self::assertStringContainsString("\nIOSchedulingClass=idle\n", $service, $name);
            $core = dirname(__DIR__, 3) . '/packaging/systemd/' . $name;
            if (is_file($core)) {
                self::assertStringContainsString("\nNice=19\n", (string) file_get_contents($core), $name);
                self::assertStringContainsString("\nIOSchedulingClass=idle\n", (string) file_get_contents($core), $name);
            }
        }
    }

    /** @return iterable<string, array{string, string}> */
    public static function checkTimes(): iterable
    {
        yield 'night' => ['01:00', '13:00'];
        yield 'midday wraps' => ['12:30', '00:30'];
        yield 'late evening' => ['23:59', '11:59'];
        yield 'midnight' => ['00:00', '12:00'];
    }

    /** @dataProvider checkTimes */
    public function testCheckRunsTwelveHoursAfterTheBackup(string $backup, string $check): void
    {
        self::assertSame($check, Units::checkTime($backup));
    }

    /** @return iterable<array{string}> */
    public static function badTimes(): iterable
    {
        foreach (['', '1:00', '24:00', '12:60', '01:00:00', '01:00 ', "01:00\n", 'ab:cd', '01:00; rm', '-1:00'] as $time) {
            yield [$time];
        }
    }

    /** @dataProvider badTimes */
    public function testRefusesATimeThatIsNotHhMm(string $time): void
    {
        $this->expectException(InvalidArgumentException::class);
        self::rendered($time);
    }

    public function testRefusesAnUnknownPlaceholder(): void
    {
        $this->expectException(InvalidArgumentException::class);
        $this->expectExceptionMessage('{other}');
        Units::render("[Timer]\nOnCalendar={other}\n", '01:00');
    }

    public function testRefusesARelativeExecLine(): void
    {
        $this->expectException(InvalidArgumentException::class);
        Units::render("[Service]\nExecStopPost=relical forget --all\n", '01:00');
    }

    public function testRefusesAnIncompleteSetOfTemplates(): void
    {
        $templates = Units::loadTemplates(self::TEMPLATES);
        unset($templates[Units::CHECK_TIMER]);

        $this->expectException(InvalidArgumentException::class);
        Units::renderAll($templates, '01:00');
    }

    public function testRenderedUnitsPassSystemdAnalyzeWhereAvailable(): void
    {
        $analyze = '/usr/bin/systemd-analyze';
        if (!is_executable($analyze)) {
            self::markTestSkipped('systemd-analyze is not installed');
        }
        // Verify the units as written, with a stand-in binary at a temporary
        // path (verify checks that ExecStart= binaries exist).
        $dir = sys_get_temp_dir() . '/relical-units-' . bin2hex(random_bytes(6));
        mkdir($dir, 0700);
        try {
            $paths = [];
            foreach (self::rendered() as $name => $unit) {
                $unit = str_replace('/usr/local/lib/relical/relical', '/bin/true', $unit);
                file_put_contents($dir . '/' . $name, $unit);
                $paths[] = $dir . '/' . $name;
            }
            $process = proc_open([$analyze, 'verify', ...$paths], [1 => ['pipe', 'w'], 2 => ['pipe', 'w']], $pipes);
            self::assertIsResource($process);
            $output = stream_get_contents($pipes[1]) . stream_get_contents($pipes[2]);
            $code = proc_close($process);
            // Only problems in our own files count; the host's units are not ours.
            $ours = array_filter(explode("\n", $output), static fn (string $l): bool => str_contains($l, $dir));
            self::assertSame([], array_values($ours), $output);
            self::assertSame(0, $code, $output);
        } finally {
            array_map('unlink', glob($dir . '/*') ?: []);
            rmdir($dir);
        }
    }
}

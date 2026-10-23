<?php

declare(strict_types=1);

namespace Noiz\Relical\Tests;

use Noiz\Relical\Action;
use Noiz\Relical\Installer;
use Noiz\Relical\Paths;
use Noiz\Relical\Units;
use PHPUnit\Framework\TestCase;
use RuntimeException;

final class InstallerTest extends TestCase
{
    private const BIN = '/ext/plib/bin';
    private const SYSTEMCTL = '/usr/bin/systemctl';
    private const KEYGEN = '/usr/bin/ssh-keygen';

    private const CONFIG_INPUT = [
        'sftp_host' => 'u000000-sub1.your-storagebox.example',
        'sftp_port' => 23,
        'sftp_user' => 'u000000-sub1',
        'sftp_path' => 'relical',
        'retention' => ['daily' => 14, 'weekly' => 8, 'monthly' => 12],
        'backup_time' => '01:00',
        'limit_upload_kib' => 0,
    ];

    /** @return array<string, ?string> a package with matching checksums */
    private static function package(string $relical = 'relical-binary', string $restic = 'restic-binary'): array
    {
        return [
            self::BIN . '/relical' => $relical,
            self::BIN . '/restic' => $restic,
            self::BIN . '/SHA256SUMS' => hash('sha256', $relical) . "  relical\n" . hash('sha256', $restic) . "  restic\n",
        ];
    }

    /** @return array<string, string> */
    private static function units(string $time = '01:00'): array
    {
        return Units::renderAll(Units::loadTemplates(__DIR__ . '/../plib/resources/systemd'), $time);
    }

    /** @return array<string, ?string> a server Relical was installed on, with keys */
    private static function installedServer(): array
    {
        $files = self::package() + [
            Paths::LIB_DIR => null,
            Paths::RELICAL_BIN => 'relical-binary',
            Paths::RESTIC_BIN => 'restic-binary',
            Paths::ETC_DIR => null,
            Paths::KEYS_DIR => null,
            Paths::KEYS_DIR . '/example.test' => 'repository key',
            Paths::SSH_DIR => null,
            Paths::SSH_KEY => 'private key',
            Paths::SSH_KEY . '.pub' => 'public key',
            Paths::KNOWN_HOSTS => 'pinned host key',
            Paths::CONFIG => '{}',
        ];
        foreach (self::units() as $name => $unit) {
            $files[Units::SYSTEMD_DIR . '/' . $name] = $unit;
        }

        return $files;
    }

    private static function installer(FakeProbe $probe): Installer
    {
        return new Installer(self::BIN, $probe, self::SYSTEMCTL, self::KEYGEN);
    }

    /**
     * @param list<Action> $plan
     * @return list<string> one line per step: type and path or argv
     */
    private static function summary(array $plan): array
    {
        return array_map(static function (Action $a): string {
            if ($a->type === Action::RUN) {
                return 'run ' . implode(' ', $a->params['argv']);
            }
            if ($a->type === Action::NOTE) {
                return 'note';
            }

            return $a->type . ' ' . $a->path();
        }, $plan);
    }

    public function testFreshInstallPlan(): void
    {
        $plan = self::installer(new FakeProbe(self::package()))->install(false, self::units(), self::CONFIG_INPUT, 'host.example.test');

        self::assertSame([
            'note',
            'ensure_dir /usr/local/lib/relical',
            'install_binary /usr/local/lib/relical/relical',
            'install_binary /usr/local/lib/relical/restic',
            'ensure_dir /etc/relical',
            'ensure_dir /etc/relical/keys',
            'ensure_dir /etc/relical/ssh',
            'ensure_dir /var/lib/relical',
            "run /usr/bin/ssh-keygen -q -t ed25519 -N  -C relical@host.example.test -f /etc/relical/ssh/id_ed25519",
            'write_config /etc/relical/config.json',
            'write_file /etc/systemd/system/relical-backup.service',
            'write_file /etc/systemd/system/relical-backup.timer',
            'write_file /etc/systemd/system/relical-check.service',
            'write_file /etc/systemd/system/relical-check.timer',
            'run /usr/bin/systemctl daemon-reload',
            'run /usr/bin/systemctl enable --now relical-backup.timer relical-check.timer',
            'run /usr/bin/systemctl restart relical-backup.timer relical-check.timer',
        ], self::summary($plan));

        $keygen = $plan[8]->params['argv'];
        self::assertSame(['/usr/bin/ssh-keygen', '-q', '-t', 'ed25519', '-N', '', '-C', 'relical@host.example.test', '-f', '/etc/relical/ssh/id_ed25519'], $keygen);

        foreach ($plan as $action) {
            if ($action->type === Action::ENSURE_DIR) {
                self::assertSame($action->path() === Paths::LIB_DIR ? 0755 : 0700, $action->params['mode'], (string) $action->path());
            }
            if ($action->type === Action::INSTALL_BINARY) {
                self::assertSame(0755, $action->params['mode']);
                self::assertSame(self::BIN . '/' . basename((string) $action->path()), $action->params['from']);
            }
            if ($action->type === Action::WRITE_FILE) {
                self::assertSame(0644, $action->params['mode']);
            }
        }
    }

    public function testUpgradeOfAnUpToDateServerOnlyReloadsAndEnables(): void
    {
        $plan = self::installer(new FakeProbe(self::installedServer()))->install(true, self::units(), self::CONFIG_INPUT, 'host.example.test');

        self::assertSame([
            'note',
            'ensure_dir /usr/local/lib/relical',
            'note',
            'note',
            'ensure_dir /etc/relical',
            'ensure_dir /etc/relical/keys',
            'ensure_dir /etc/relical/ssh',
            'ensure_dir /var/lib/relical',
            'note',
            'write_config /etc/relical/config.json',
            'note', 'note', 'note', 'note',
            'run /usr/bin/systemctl daemon-reload',
            'run /usr/bin/systemctl enable --now relical-backup.timer relical-check.timer',
        ], self::summary($plan));
    }

    public function testUpgradeReplacesChangedBinariesAndRestartsAChangedTimer(): void
    {
        $server = array_merge(self::installedServer(), self::package('relical-v2', 'restic-binary'));
        $plan = self::installer(new FakeProbe($server))->install(true, self::units('04:30'), self::CONFIG_INPUT, 'host.example.test');
        $summary = self::summary($plan);

        self::assertContains('install_binary /usr/local/lib/relical/relical', $summary);
        self::assertNotContains('install_binary /usr/local/lib/relical/restic', $summary);
        self::assertContains('write_file /etc/systemd/system/relical-backup.timer', $summary);
        self::assertContains('write_file /etc/systemd/system/relical-check.timer', $summary);
        self::assertNotContains('write_file /etc/systemd/system/relical-backup.service', $summary);
        self::assertSame('run /usr/bin/systemctl restart relical-backup.timer relical-check.timer', end($summary));
    }

    public function testUpgradeNeverTouchesKeysKnownHostsOrAnExistingSshKey(): void
    {
        $plan = self::installer(new FakeProbe(self::installedServer()))->install(true, self::units('05:00'), self::CONFIG_INPUT, 'host.example.test');

        foreach ($plan as $action) {
            $path = $action->path();
            if ($path !== null) {
                self::assertStringStartsNotWith(Paths::KEYS_DIR . '/', $path);
                self::assertStringStartsNotWith(Paths::SSH_DIR . '/', $path);
            }
            if ($action->type === Action::RUN) {
                self::assertNotSame(self::KEYGEN, $action->params['argv'][0], 'no new SSH key over an existing one');
                foreach ($action->params['argv'] as $arg) {
                    self::assertStringStartsNotWith(Paths::SSH_DIR . '/', $arg);
                    self::assertStringStartsNotWith(Paths::KEYS_DIR . '/', $arg);
                }
            }
        }
    }

    public function testFreshInstallWithoutADestinationWritesOnlyTheMysqlOptionFile(): void
    {
        $input = ['sftp_host' => ''] + self::CONFIG_INPUT;
        $plan = self::installer(new FakeProbe(self::package()))->install(false, self::units(), $input, 'host.example.test');

        $config = array_values(array_filter($plan, static fn (Action $a): bool => $a->type === Action::WRITE_CONFIG));
        self::assertCount(1, $config);
        self::assertFalse($config[0]->params['with_destination']);
    }

    /** @return iterable<string, array{array<string, ?string>, string}> */
    public static function badPackages(): iterable
    {
        $package = self::package();

        yield 'relical differs' => [[self::BIN . '/relical' => 'tampered'] + $package, 'does not match'];
        yield 'restic differs' => [[self::BIN . '/restic' => 'tampered'] + $package, 'does not match'];
        $missing = $package;
        unset($missing[self::BIN . '/restic']);
        yield 'restic missing' => [$missing, 'restic is missing'];
        $noSums = $package;
        unset($noSums[self::BIN . '/SHA256SUMS']);
        yield 'SHA256SUMS missing' => [$noSums, 'SHA256SUMS is missing'];
        yield 'no line for restic' => [[self::BIN . '/SHA256SUMS' => hash('sha256', 'relical-binary') . "  relical\n"] + $package, 'no checksum for restic'];
        yield 'malformed line' => [[self::BIN . '/SHA256SUMS' => "not a checksum\n"] + $package, 'line 1'];
        yield 'listed twice' => [[self::BIN . '/SHA256SUMS' => $package[self::BIN . '/SHA256SUMS'] . hash('sha256', 'x') . "  restic\n"] + $package, 'twice'];
    }

    /**
     * @dataProvider badPackages
     * @param array<string, ?string> $files
     */
    public function testRefusesAPackageWhoseBinariesDoNotMatch(array $files, string $message): void
    {
        $this->expectException(RuntimeException::class);
        $this->expectExceptionMessage($message);
        self::installer(new FakeProbe($files))->install(false, self::units(), self::CONFIG_INPUT, 'host.example.test');
    }

    public function testParsesSha256sumOutputIncludingBinaryMode(): void
    {
        $a = str_repeat('a', 64);
        $b = str_repeat('B', 64);

        self::assertSame(['relical' => $a, 'restic' => strtolower($b)], Installer::parseSums("$a  relical\n$b *restic\n\n"));
    }

    public function testKeyCommentUsesOnlySafeHostCharacters(): void
    {
        $plan = self::installer(new FakeProbe(self::package()))->install(false, self::units(), self::CONFIG_INPUT, 'Host Name;x.Example.TEST');
        $keygen = array_values(array_filter($plan, static fn (Action $a): bool => $a->type === Action::RUN && $a->params['argv'][0] === self::KEYGEN));

        self::assertSame('relical@hostnamex.example.test', $keygen[0]->params['argv'][7]);
    }

    public function testUninstallRemovesUnitsAndBinariesAndKeepsEtcAndState(): void
    {
        $plan = self::installer(new FakeProbe(self::installedServer()))->uninstall();

        self::assertSame([
            'run /usr/bin/systemctl disable --now relical-backup.timer relical-check.timer',
            'run /usr/bin/systemctl stop relical-backup.service relical-check.service',
            'remove_file /etc/systemd/system/relical-backup.service',
            'remove_file /etc/systemd/system/relical-backup.timer',
            'remove_file /etc/systemd/system/relical-check.service',
            'remove_file /etc/systemd/system/relical-check.timer',
            'run /usr/bin/systemctl daemon-reload',
            'remove_file /usr/local/lib/relical/relical',
            'remove_file /usr/local/lib/relical/restic',
            'remove_dir /usr/local/lib/relical',
            'note',
        ], self::summary($plan));

        foreach ($plan as $action) {
            self::assertTrue($action->optional || $action->type === Action::NOTE, 'every uninstall step carries on after a failure');
            $path = (string) $action->path();
            self::assertStringStartsNotWith(Paths::ETC_DIR, $path);
            self::assertStringStartsNotWith(Paths::STATE_DIR, $path);
        }
    }

    public function testUninstallOfAPartialInstallSkipsWhatIsNotThere(): void
    {
        $plan = self::installer(new FakeProbe([Paths::LIB_DIR => null, Paths::RELICAL_BIN => 'x']))->uninstall();

        self::assertSame([
            'run /usr/bin/systemctl daemon-reload',
            'remove_file /usr/local/lib/relical/relical',
            'remove_dir /usr/local/lib/relical',
            'note',
        ], self::summary($plan));
    }

    public function testFindToolReturnsTheFirstThatExists(): void
    {
        $probe = new FakeProbe(['/bin/systemctl' => 'x']);

        self::assertSame('/bin/systemctl', Installer::findTool($probe, ['/usr/bin/systemctl', '/bin/systemctl']));
        $this->expectException(RuntimeException::class);
        Installer::findTool($probe, ['/usr/bin/ssh-keygen']);
    }
}

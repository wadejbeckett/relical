<?php

declare(strict_types=1);

namespace Noiz\Relical;

use RuntimeException;

/**
 * Plans what post-install.php and pre-uninstall.php do, as a list of
 * Actions, from what a Probe reports about the system. Nothing here changes
 * anything; InstallRunner carries the plan out.
 *
 * Install and upgrade plan the same steps, so an upgrade repairs whatever a
 * fresh install would create. Neither ever writes into /etc/relical/keys,
 * /etc/relical/ssh/known_hosts or over an existing SSH key, and uninstall
 * leaves /etc/relical and /var/lib/relical in place.
 */
final class Installer
{
    /** The bundled binaries (plib/bin/<name>) and where each is installed. */
    public const BINARIES = ['relical' => Paths::RELICAL_BIN, 'restic' => Paths::RESTIC_BIN];

    public const SUMS_FILE = 'SHA256SUMS';

    /**
     * Names the installer may leave in /usr/local/lib/relical after an
     * interrupted run: the staging copies (stagedPath()) and RootFile's
     * temporary files. Uninstall deletes these and nothing else it did not
     * install.
     */
    public const OWN_LIB_FILES = '/^(\.(relical|restic)\.new|\.\.?(relical|restic)(\.new)?\.[0-9a-f]{12}\.tmp)$/D';

    /**
     * @param string $binDir    the extension's plib/bin
     * @param string $systemctl absolute path of systemctl
     * @param string $sshKeygen absolute path of ssh-keygen
     */
    public function __construct(
        private string $binDir,
        private Probe $probe,
        private string $systemctl,
        private string $sshKeygen
    ) {
    }

    /**
     * @param bool                  $upgrade     Plesk passed `upgrade`
     * @param array<string, string> $units       Units::renderAll()
     * @param array<string, mixed>  $configInput Settings::configInput()
     * @param string                $serverId    this server's host name
     * @return list<Action>
     * @throws RuntimeException when a bundled binary is missing or does not match SHA256SUMS
     * @throws \InvalidArgumentException when the saved destination settings are not valid
     */
    public function install(bool $upgrade, array $units, array $configInput, string $serverId): array
    {
        $binaries = $this->verifiedBinaries();
        $withDestination = trim((string) ($configInput['sftp_host'] ?? '')) !== '';
        if ($withDestination) {
            // A bad setting stops the install here, before anything changes,
            // rather than after the new binaries are live.
            ConfigWriter::render($configInput, $serverId);
        }

        $plan = [Action::note($upgrade ? 'upgrading the installed files and timers' : 'installing')];

        $plan[] = Action::ensureDir(Paths::LIB_DIR, 0755);
        // Both binaries are copied and checked under staging names first and
        // only then renamed into place one after the other, so a failed copy
        // leaves the old pair in place rather than a new relical beside an
        // old restic.
        $renames = [];
        foreach ($binaries as $name => $sha256) {
            $target = self::BINARIES[$name];
            if ($this->probe->sha256($target) === $sha256) {
                $plan[] = Action::note(sprintf('%s is already this version', $target));
                continue;
            }
            $staged = self::stagedPath($target);
            $plan[] = Action::installBinary($this->binDir . '/' . $name, $staged, $sha256, 'stage');
            $renames[] = Action::rename($staged, $target);
        }
        array_push($plan, ...$renames);

        foreach ([Paths::ETC_DIR, Paths::KEYS_DIR, Paths::SSH_DIR, Paths::STATE_DIR] as $dir) {
            $plan[] = Action::ensureDir($dir, 0700);
        }

        if ($this->probe->exists(Paths::SSH_KEY)) {
            $plan[] = Action::note(sprintf('keeping the existing SSH key %s', Paths::SSH_KEY));
        } else {
            // Generated under a staging name and renamed into place (public
            // half first), so an interrupted ssh-keygen never leaves a
            // truncated key that a later run would keep.
            $staged = self::stagedPath(Paths::SSH_KEY);
            $plan[] = Action::removeFile($staged);
            $plan[] = Action::removeFile($staged . '.pub');
            $plan[] = Action::run([
                $this->sshKeygen, '-q', '-t', 'ed25519', '-N', '', '-C', 'relical@' . self::commentHost($serverId), '-f', $staged,
            ]);
            $plan[] = Action::rename($staged . '.pub', Paths::SSH_KEY . '.pub');
            $plan[] = Action::rename($staged, Paths::SSH_KEY);
        }

        $plan[] = Action::writeConfig($configInput, $serverId, $withDestination);

        array_push($plan, ...$this->unitActions($units));

        return $plan;
    }

    /** Where a file is staged before it is renamed over $target (same directory). */
    public static function stagedPath(string $target): string
    {
        return dirname($target) . '/.' . basename($target) . '.new';
    }

    /**
     * Writes the units that differ from what is installed, reloads systemd,
     * enables and starts both timers, and restarts an installed timer whose
     * schedule changed. Also what a settings save needs when the backup time changes.
     *
     * @param array<string, string> $units Units::renderAll()
     * @return list<Action>
     */
    public function unitActions(array $units): array
    {
        $plan = [];
        $changedTimers = [];
        foreach (Units::ALL as $name) {
            if (!isset($units[$name])) {
                throw new RuntimeException(sprintf('the unit %s was not rendered', $name));
            }
            $path = Units::SYSTEMD_DIR . '/' . $name;
            $current = $this->probe->read($path);
            if ($current === $units[$name]) {
                $plan[] = Action::note(sprintf('%s is unchanged', $path));
                continue;
            }
            $plan[] = Action::writeFile($path, $units[$name], 0644);
            if ($current !== null && in_array($name, Units::TIMERS, true)) {
                $changedTimers[] = $name;
            }
        }
        $plan[] = Action::run([$this->systemctl, 'daemon-reload']);
        $plan[] = Action::run([$this->systemctl, 'enable', '--now', ...Units::TIMERS]);
        if ($changedTimers !== []) {
            // Restart a changed timer so its new schedule is in force.
            $plan[] = Action::run([$this->systemctl, 'restart', ...$changedTimers]);
        }

        return $plan;
    }

    /**
     * Stops and removes the timers and units and the installed binaries.
     * Every step runs even when an earlier one fails, so as much as possible
     * is removed.
     *
     * @return list<Action>
     */
    public function uninstall(): array
    {
        $installed = array_values(array_filter(
            Units::ALL,
            fn (string $name): bool => $this->probe->exists(Units::SYSTEMD_DIR . '/' . $name)
        ));
        $timers = array_values(array_intersect(Units::TIMERS, $installed));
        $services = array_values(array_intersect(Units::SERVICES, $installed));

        $plan = [];
        if ($timers !== []) {
            $plan[] = Action::run([$this->systemctl, 'disable', '--now', ...$timers], true);
        }
        if ($services !== []) {
            // A backup or check still running when the extension goes is stopped.
            $plan[] = Action::run([$this->systemctl, 'stop', ...$services], true);
        }
        foreach (Units::ALL as $name) {
            $path = Units::SYSTEMD_DIR . '/' . $name;
            if ($this->probe->exists($path)) {
                $plan[] = Action::removeFile($path, true);
            }
        }
        $plan[] = Action::run([$this->systemctl, 'daemon-reload'], true);
        foreach (self::BINARIES as $target) {
            if ($this->probe->exists($target)) {
                $plan[] = Action::removeFile($target, true);
            }
        }
        if ($this->probe->exists(Paths::LIB_DIR)) {
            $plan[] = Action::removeDir(Paths::LIB_DIR, true, self::OWN_LIB_FILES);
        }
        $plan[] = Action::note(sprintf('kept %s and %s', Paths::ETC_DIR, Paths::STATE_DIR));

        return $plan;
    }

    /**
     * Checks both bundled binaries against SHA256SUMS before anything is
     * installed.
     *
     * @return array<string, string> name => sha256
     */
    public function verifiedBinaries(): array
    {
        $sumsPath = $this->binDir . '/' . self::SUMS_FILE;
        $sums = $this->probe->read($sumsPath);
        if ($sums === null) {
            throw new RuntimeException(sprintf('%s is missing; the package is incomplete', $sumsPath));
        }
        $expected = self::parseSums($sums);

        $out = [];
        foreach (array_keys(self::BINARIES) as $name) {
            if (!isset($expected[$name])) {
                throw new RuntimeException(sprintf('%s has no checksum for %s', $sumsPath, $name));
            }
            $path = $this->binDir . '/' . $name;
            $actual = $this->probe->sha256($path);
            if ($actual === null) {
                throw new RuntimeException(sprintf('%s is missing; the package is incomplete', $path));
            }
            if (!hash_equals($expected[$name], $actual)) {
                throw new RuntimeException(sprintf('%s does not match its checksum in %s; refusing to install it', $path, $sumsPath));
            }
            $out[$name] = $actual;
        }

        return $out;
    }

    /**
     * Parses `sha256sum` output: `<64 hex>  <name>` (or ` *<name>`) per line.
     *
     * @return array<string, string> name => lower-case sha256
     */
    public static function parseSums(string $sums): array
    {
        $out = [];
        foreach (preg_split('/\R/', $sums) ?: [] as $number => $line) {
            if (trim($line) === '') {
                continue;
            }
            if (preg_match('/^([0-9a-fA-F]{64}) [ *]([A-Za-z0-9._-]+)$/D', $line, $m) !== 1) {
                throw new RuntimeException(sprintf('SHA256SUMS line %d is not "<sha256>  <file>"', $number + 1));
            }
            if (isset($out[$m[2]])) {
                throw new RuntimeException(sprintf('SHA256SUMS lists %s twice', $m[2]));
            }
            $out[$m[2]] = strtolower($m[1]);
        }

        return $out;
    }

    /**
     * The first of $candidates that exists, for tools whose location differs
     * between distributions (Plesk runs the scripts with a minimal PATH).
     *
     * @param list<string> $candidates absolute paths
     */
    public static function findTool(Probe $probe, array $candidates): string
    {
        foreach ($candidates as $path) {
            if ($probe->exists($path)) {
                return $path;
            }
        }

        throw new RuntimeException(sprintf('none of %s exists', implode(', ', $candidates)));
    }

    /** The host name for the SSH key's comment, reduced to safe characters. */
    private static function commentHost(string $serverId): string
    {
        $host = preg_replace('/[^a-z0-9.-]/', '', strtolower($serverId)) ?? '';

        return $host === '' ? 'localhost' : $host;
    }
}

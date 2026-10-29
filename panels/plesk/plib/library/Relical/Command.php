<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;

/**
 * The allowlisted pass-through from cli.php to the core binary.
 *
 * It accepts exactly the commands and flags of the CLI contract, validates
 * every value, and builds the argv array that cli.php hands to proc_open
 * (array form: no shell ever parses it):
 *
 *     /usr/local/lib/relical/relical -config /etc/relical/config.json <command> <flags>
 *
 * Flags are written `--name value`; boolean flags (`--all`) take no value.
 */
final class Command
{
    public const UNITS = ['mail', 'files', 'db', 'panel-config'];

    /**
     * The root scripts the screens run through sbin/relical-root
     * (Paths::rootCommand()), and the arguments each takes. cli.php takes
     * what parse() accepts. relical-root also runs inventory.php,
     * post-install.php and pre-uninstall.php, which the screens never do.
     */
    public const SCRIPTS = [
        'cli.php', 'write-config.php', 'install-timers.php', 'overview.php',
        'hostkey.php', 'probe.php', 'log-tail.php', 'running.php',
        'restore-run.php', 'restore-result.php', 'restore-running.php',
        'restore-read.php', 'cache-clear.php',
    ];

    /** The units a customer restores (the panel configuration is the administrator's). */
    public const CUSTOMER_UNITS = ['mail', 'files', 'db'];

    private const SNAPSHOT = '/^(latest|[0-9a-f]{8,64})$/D';
    private const MAILBOX = '/^[a-z0-9][a-z0-9._+-]{0,63}$/D';
    private const DATABASE = '/^[A-Za-z0-9_$][A-Za-z0-9_$-]{0,63}$/D';
    private const SUBSET = '/^(100|[1-9][0-9]?)%$/D';
    /** A restore's reference: its start time (UTC, YYYYmmddHHMMSS) and 96 random bits. */
    private const RESTORE_ID = '/^[0-9]{14}-[0-9a-f]{24}$/D';

    /** @var list<string> */
    private array $argv;

    /** @param list<string> $argv */
    private function __construct(array $argv)
    {
        $this->argv = $argv;
    }

    /**
     * @param list<string> $args the arguments after the script name
     * @throws InvalidArgumentException naming what was refused
     */
    public static function parse(array $args): self
    {
        $args = array_values(array_map('strval', $args));
        if ($args === []) {
            throw new InvalidArgumentException('no command given');
        }
        $command = array_shift($args);
        $flags = self::flags($args);

        switch ($command) {
            case 'backup':
                $out = self::target($flags);
                $unit = self::optional($flags, 'unit', [self::class, 'unit']);
                self::unitFitsTarget($flags, $unit);
                $out = array_merge($out, self::emit('unit', $unit));
                self::only($flags, ['subscription', 'all', 'unit']);
                break;

            case 'forget':
                $out = self::target($flags);
                self::only($flags, ['subscription', 'all']);
                break;

            case 'check':
                $out = self::target($flags);
                $out = array_merge($out, self::emit('read-data-subset', self::optional($flags, 'read-data-subset', [self::class, 'subset'])));
                self::only($flags, ['subscription', 'all', 'read-data-subset']);
                break;

            case 'mailboxes':
            case 'adopt':
                // A subscription's own repository, never _server.
                $out = self::emit('subscription', self::required($flags, 'subscription', [self::class, 'domain']));
                self::only($flags, ['subscription']);
                break;

            case 'snapshots':
                $out = self::emit('subscription', self::required($flags, 'subscription', [self::class, 'subscription']));
                $unit = self::optional($flags, 'unit', [self::class, 'unit']);
                self::unitFitsTarget($flags, $unit);
                $out = array_merge($out, self::emit('unit', $unit));
                self::only($flags, ['subscription', 'unit']);
                break;

            case 'ls':
                $out = array_merge(
                    self::emit('subscription', self::required($flags, 'subscription', [self::class, 'subscription'])),
                    self::emit('snapshot', self::required($flags, 'snapshot', [self::class, 'snapshot'])),
                    self::emit('path', self::optional($flags, 'path', [self::class, 'path']))
                );
                self::only($flags, ['subscription', 'snapshot', 'path']);
                break;

            case 'restore':
                $out = self::restore($flags);
                break;

            case 'status':
            case 'report':
            case 'inventory':
            case 'version':
                $out = [];
                self::only($flags, []);
                break;

            default:
                throw new InvalidArgumentException(sprintf('command "%s" is not allowed', self::printable($command)));
        }

        return new self(array_merge(
            [Paths::RELICAL_BIN, '-config', Paths::CONFIG, $command],
            $out
        ));
    }

    /** @return list<string> the argv for proc_open, binary first */
    public function argv(): array
    {
        return $this->argv;
    }

    /**
     * Validates a root script's arguments (each script calls this on its own
     * argv as well): cli.php as parse() does, hostkey.php `scan` or
     * `trust <SHA256 fingerprint>`, restore-run.php `<id> restore ...` (a
     * customer restore, see customerRestore()), restore-result.php `<id>`,
     * restore-running.php `<subscription>`, every other script none.
     *
     * @param list<string> $args the arguments after the script name
     * @return list<string> the arguments, unchanged
     * @throws InvalidArgumentException naming what was refused
     */
    public static function scriptArgs(string $script, array $args): array
    {
        $args = array_values(array_map('strval', $args));
        if (!in_array($script, self::SCRIPTS, true)) {
            throw new InvalidArgumentException(sprintf('script "%s" is not allowed', self::printable($script)));
        }
        switch ($script) {
            case 'cli.php':
                self::parse($args);
                return $args;
            case 'hostkey.php':
                if ($args === ['scan']) {
                    return $args;
                }
                if (count($args) === 2 && $args[0] === 'trust') {
                    HostKey::fingerprintArg($args[1]);
                    return $args;
                }
                throw new InvalidArgumentException('hostkey.php takes "scan" or "trust <fingerprint>"');
            case 'restore-run.php':
                if (count($args) < 2) {
                    throw new InvalidArgumentException('restore-run.php takes <id> restore <flags>');
                }
                self::restoreId($args[0]);
                self::customerRestore(array_slice($args, 1));
                return $args;
            case 'restore-result.php':
                if (count($args) !== 1) {
                    throw new InvalidArgumentException('restore-result.php takes <id>');
                }
                self::restoreId($args[0]);
                return $args;
            case 'restore-running.php':
                if (count($args) !== 1) {
                    throw new InvalidArgumentException('restore-running.php takes <subscription>');
                }
                self::domain($args[0]);
                return $args;
            case 'restore-read.php':
                self::readCliArgs($args);
                return $args;
            case 'cache-clear.php':
                if (count($args) > 1) {
                    throw new InvalidArgumentException('cache-clear.php takes at most <subscription>');
                }
                if ($args !== []) {
                    self::domain($args[0]);
                }
                return $args;
            default:
                if ($args !== []) {
                    throw new InvalidArgumentException(sprintf('%s takes no arguments', $script));
                }
                return $args;
        }
    }

    /**
     * The argv that runs a root script as root, for Process (no shell),
     * through Plesk's mod_wrapper link to sbin/relical-root
     * (Paths::rootCommand()).
     *
     * @param list<string> $args
     * @return list<string>
     */
    public static function rootArgv(string $script, array $args = []): array
    {
        return array_merge(
            Paths::rootCommand(),
            [$script],
            self::scriptArgs($script, $args)
        );
    }

    /**
     * A shell command that starts `cli.php <args>` as root in the background
     * and returns at once, as Cloudflare DNS Sync starts its syncs: every
     * word escaped, no input or output, detached with `&`. The web request
     * never waits for the run; its outcome lands in the state file and log.
     *
     * @param list<string> $cliArgs validated by parse()
     */
    public static function detached(array $cliArgs): string
    {
        return self::detachedScript('cli.php', $cliArgs);
    }

    /**
     * The same for any root script (restore-run.php for a customer restore,
     * whose outcome lands in its record file).
     *
     * @param list<string> $args validated by scriptArgs()
     */
    public static function detachedScript(string $script, array $args): string
    {
        $words = array_map([self::class, 'shellQuote'], self::rootArgv($script, $args));

        return implode(' ', $words) . ' < /dev/null > /dev/null 2>&1 &';
    }

    /**
     * One word for sh, in single quotes, byte for byte. Not escapeshellarg():
     * under a C locale it silently drops multibyte characters ("Envoyés"
     * becomes "Envoys"), which would restore the wrong folder.
     */
    public static function shellQuote(string $word): string
    {
        return "'" . str_replace("'", "'\\''", $word) . "'";
    }

    /**
     * A restore a customer may start: `restore` of mail, files or a
     * database in a subscription (never `_server`, never the panel
     * configuration), with everything else parse() checks.
     *
     * @param list<string> $cliArgs the arguments after cli.php
     * @return list<string> the arguments, unchanged
     * @throws InvalidArgumentException naming what was refused
     */
    public static function customerRestore(array $cliArgs): array
    {
        $cliArgs = array_values(array_map('strval', $cliArgs));
        if (($cliArgs[0] ?? '') !== 'restore') {
            throw new InvalidArgumentException('only a restore can be started here');
        }
        $flags = self::flags(array_slice($cliArgs, 1));
        self::domain(is_string($flags['subscription'] ?? null) ? $flags['subscription'] : '');
        if (!in_array($flags['unit'] ?? null, self::CUSTOMER_UNITS, true)) {
            throw new InvalidArgumentException('unit must be one of ' . implode(', ', self::CUSTOMER_UNITS));
        }
        self::parse($cliArgs);

        return $cliArgs;
    }

    /**
     * What restore-read.php asks the core, for the customer screens:
     * `snapshots <subscription>`, `mailboxes <subscription>` or
     * `ls <subscription> <snapshot id> [<path>]` (an id, never "latest"; a
     * relative path). Never `_server`.
     *
     * @param list<string> $args restore-read.php's arguments
     * @return list<string> the arguments for cli.php
     * @throws InvalidArgumentException naming what was refused
     */
    public static function readCliArgs(array $args): array
    {
        $args = array_values(array_map('strval', $args));
        $kind = $args[0] ?? '';
        $count = count($args);
        if (($kind === 'snapshots' || $kind === 'mailboxes') && $count === 2) {
            $cli = [$kind, '--subscription', self::domain($args[1])];
        } elseif ($kind === 'ls' && ($count === 3 || $count === 4)) {
            if (preg_match('/^[0-9a-f]{8,64}$/D', $args[2]) !== 1) {
                throw new InvalidArgumentException('ls needs a hexadecimal snapshot id');
            }
            $cli = ['ls', '--subscription', self::domain($args[1]), '--snapshot', $args[2]];
            if ($count === 4) {
                $path = self::path($args[3]);
                if ($path[0] === '/' || str_ends_with($path, '/') || str_contains($path, '//')) {
                    throw new InvalidArgumentException('path must be relative and clean');
                }
                $cli[] = '--path';
                $cli[] = $path;
            }
        } else {
            throw new InvalidArgumentException('restore-read.php takes "snapshots <subscription>", "mailboxes <subscription>" or "ls <subscription> <snapshot> [<path>]"');
        }
        self::parse($cli);

        return $cli;
    }

    /** The reference of a customer restore (its record file's name). */
    public static function restoreId(string $value): string
    {
        if (preg_match(self::RESTORE_ID, $value) !== 1) {
            throw new InvalidArgumentException('restore id must be YYYYmmddHHMMSS-<24 hexadecimal digits>');
        }

        return $value;
    }

    // ----- validators (public so the restore screens can reuse them) -----

    public static function subscription(string $value): string
    {
        if ($value === Paths::SERVER_REPO) {
            return $value;
        }
        if (!CoreRules::validDomain($value)) {
            throw new InvalidArgumentException('subscription must be a lower-case domain name or _server');
        }

        return $value;
    }

    public static function domain(string $value): string
    {
        if ($value === Paths::SERVER_REPO) {
            throw new InvalidArgumentException('domain must be a lower-case domain name');
        }

        return self::subscription($value);
    }

    public static function snapshot(string $value): string
    {
        if (preg_match(self::SNAPSHOT, $value) !== 1) {
            throw new InvalidArgumentException('snapshot must be "latest" or a hexadecimal snapshot id');
        }

        return $value;
    }

    public static function unit(string $value): string
    {
        if (!in_array($value, self::UNITS, true)) {
            throw new InvalidArgumentException('unit must be one of ' . implode(', ', self::UNITS));
        }

        return $value;
    }

    public static function mailbox(string $value): string
    {
        if (preg_match(self::MAILBOX, $value) !== 1) {
            throw new InvalidArgumentException('mailbox must be the local part of an address, starting with a-z or 0-9 (then a-z, 0-9, ".", "_", "+", "-")');
        }

        return $value;
    }

    /**
     * An IMAP folder name: valid UTF-8, no control characters, not "." or
     * "..", and no leading "-" (it could read as an option further down).
     */
    public static function folder(string $value): string
    {
        if ($value === '' || strlen($value) > 255 || preg_match('/^[^\p{Cc}]+$/uD', $value) !== 1) {
            throw new InvalidArgumentException('folder must be printable text of at most 255 bytes');
        }
        if ($value[0] === '-' || $value === '.' || $value === '..') {
            throw new InvalidArgumentException('folder must not start with "-" or be "." or ".."');
        }

        return $value;
    }

    /**
     * A path inside the webspace or snapshot, written with or without a
     * leading "/": no "." or ".." segments, no control characters, and no
     * leading "-" (it could read as an option further down).
     */
    public static function path(string $value): string
    {
        if ($value === '' || strlen($value) > 4096 || preg_match('/^[^\p{Cc}]+$/uD', $value) !== 1) {
            throw new InvalidArgumentException('path must be printable text');
        }
        if ($value[0] === '-') {
            throw new InvalidArgumentException('path must not start with "-"');
        }
        foreach (explode('/', $value) as $part) {
            if ($part === '..' || $part === '.') {
                throw new InvalidArgumentException('path must not contain "." or ".." segments');
            }
        }

        return $value;
    }

    public static function database(string $value): string
    {
        if (preg_match(self::DATABASE, $value) !== 1) {
            throw new InvalidArgumentException('database name may hold only letters, digits, "_", "$" and "-", and must not start with "-"');
        }

        return $value;
    }

    public static function subset(string $value): string
    {
        if (preg_match(self::SUBSET, $value) !== 1) {
            throw new InvalidArgumentException('read-data-subset must be a percentage from 1% to 100%');
        }

        return $value;
    }

    // ----- parsing -----

    /**
     * @param list<string> $args
     * @return array<string, string|true>
     */
    private static function flags(array $args): array
    {
        $flags = [];
        for ($i = 0, $n = count($args); $i < $n; $i++) {
            $arg = $args[$i];
            if (preg_match('/^--([a-z][a-z-]*)$/D', $arg, $m) !== 1) {
                throw new InvalidArgumentException(sprintf('unexpected argument "%s"', self::printable($arg)));
            }
            $name = $m[1];
            if (array_key_exists($name, $flags)) {
                throw new InvalidArgumentException(sprintf('--%s given twice', $name));
            }
            if ($name === 'all') {
                $flags[$name] = true;
                continue;
            }
            if ($i + 1 >= $n) {
                throw new InvalidArgumentException(sprintf('--%s needs a value', $name));
            }
            if (str_starts_with($args[$i + 1], '-')) {
                // Refused for every flag, so no value can turn into an
                // option of the core or of anything it runs.
                throw new InvalidArgumentException(sprintf('the value of --%s must not start with "-"', $name));
            }
            $flags[$name] = $args[++$i];
        }

        return $flags;
    }

    /**
     * `--subscription S` or `--all`, exactly one.
     *
     * @param array<string, string|true> $flags
     * @return list<string>
     */
    private static function target(array $flags): array
    {
        $hasSub = array_key_exists('subscription', $flags);
        $hasAll = array_key_exists('all', $flags);
        if ($hasSub === $hasAll) {
            throw new InvalidArgumentException('give either --subscription or --all');
        }

        return $hasAll ? ['--all'] : self::emit('subscription', self::required($flags, 'subscription', [self::class, 'subscription']));
    }

    /**
     * `_server` holds only the panel configuration, and the panel
     * configuration lives only in `_server`.
     *
     * @param array<string, string|true> $flags
     */
    private static function unitFitsTarget(array $flags, ?string $unit): void
    {
        if ($unit === null || !isset($flags['subscription'])) {
            return;
        }
        $isServer = $flags['subscription'] === Paths::SERVER_REPO;
        if ($isServer !== ($unit === 'panel-config')) {
            throw new InvalidArgumentException('the panel-config unit belongs to the _server subscription only');
        }
    }

    /**
     * @param array<string, string|true> $flags
     * @return list<string>
     */
    private static function restore(array $flags): array
    {
        $subscription = self::required($flags, 'subscription', [self::class, 'subscription']);
        $unit = self::required($flags, 'unit', [self::class, 'unit']);
        self::unitFitsTarget($flags, $unit);

        $out = array_merge(self::emit('subscription', $subscription), self::emit('unit', $unit));
        switch ($unit) {
            case 'mail':
                $out = array_merge(
                    $out,
                    self::emit('domain', self::required($flags, 'domain', [self::class, 'domain'])),
                    self::emit('mailbox', self::required($flags, 'mailbox', [self::class, 'mailbox'])),
                    self::emit('snapshot', self::optional($flags, 'snapshot', [self::class, 'snapshot'])),
                    self::emit('folder', self::optional($flags, 'folder', [self::class, 'folder']))
                );
                self::only($flags, ['subscription', 'unit', 'domain', 'mailbox', 'snapshot', 'folder']);
                break;
            case 'files':
                $out = array_merge(
                    $out,
                    self::emit('snapshot', self::optional($flags, 'snapshot', [self::class, 'snapshot'])),
                    self::emit('path', self::optional($flags, 'path', [self::class, 'path']))
                );
                self::only($flags, ['subscription', 'unit', 'snapshot', 'path']);
                break;
            case 'db':
                $out = array_merge(
                    $out,
                    self::emit('database', self::required($flags, 'database', [self::class, 'database'])),
                    self::emit('snapshot', self::optional($flags, 'snapshot', [self::class, 'snapshot']))
                );
                self::only($flags, ['subscription', 'unit', 'database', 'snapshot']);
                break;
            default: // panel-config
                $out = array_merge($out, self::emit('snapshot', self::optional($flags, 'snapshot', [self::class, 'snapshot'])));
                self::only($flags, ['subscription', 'unit', 'snapshot']);
        }

        return $out;
    }

    /**
     * @param array<string, string|true> $flags
     * @param callable(string): string    $validate
     */
    private static function required(array $flags, string $name, callable $validate): string
    {
        $value = self::optional($flags, $name, $validate);
        if ($value === null) {
            throw new InvalidArgumentException(sprintf('--%s is required', $name));
        }

        return $value;
    }

    /**
     * @param array<string, string|true> $flags
     * @param callable(string): string    $validate
     */
    private static function optional(array $flags, string $name, callable $validate): ?string
    {
        if (!array_key_exists($name, $flags)) {
            return null;
        }
        if (!is_string($flags[$name])) {
            throw new InvalidArgumentException(sprintf('--%s needs a value', $name));
        }

        return $validate($flags[$name]);
    }

    /**
     * @param array<string, string|true> $flags
     * @param list<string>               $allowed
     */
    private static function only(array $flags, array $allowed): void
    {
        foreach (array_keys($flags) as $name) {
            if (!in_array($name, $allowed, true)) {
                throw new InvalidArgumentException(sprintf('--%s is not allowed here', $name));
            }
        }
    }

    /** @return list<string> */
    private static function emit(string $name, ?string $value): array
    {
        return $value === null ? [] : ['--' . $name, $value];
    }

    /** For error messages: never echo control characters back. */
    private static function printable(string $value): string
    {
        return substr((string) preg_replace('/[^\x20-\x7e]/', '?', $value), 0, 80);
    }
}

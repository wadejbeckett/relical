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

    private const SUBSCRIPTION = '/^[a-z0-9][a-z0-9.-]{0,252}$/D';
    private const SNAPSHOT = '/^(latest|[0-9a-f]{8,64})$/D';
    private const MAILBOX = '/^[a-z0-9._+-]{1,64}$/D';
    private const DATABASE = '/^[A-Za-z0-9_$-]{1,64}$/D';
    private const SUBSET = '/^(100|[1-9][0-9]?)%$/D';

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

    // ----- validators (public so the restore screens can reuse them) -----

    public static function subscription(string $value): string
    {
        if ($value === Paths::SERVER_REPO) {
            return $value;
        }
        if (preg_match(self::SUBSCRIPTION, $value) !== 1 || str_contains($value, '..')) {
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
            throw new InvalidArgumentException('mailbox must be the local part of an address (a-z, 0-9, ".", "_", "+", "-")');
        }

        return $value;
    }

    /** An IMAP folder name: valid UTF-8, no control characters. */
    public static function folder(string $value): string
    {
        if ($value === '' || strlen($value) > 255 || preg_match('/^[^\p{Cc}]+$/uD', $value) !== 1) {
            throw new InvalidArgumentException('folder must be printable text of at most 255 bytes');
        }

        return $value;
    }

    /**
     * A path inside the webspace or snapshot, written with or without a
     * leading "/": no "..", no empty or control characters.
     */
    public static function path(string $value): string
    {
        if ($value === '' || strlen($value) > 4096 || preg_match('/^[^\p{Cc}]+$/uD', $value) !== 1) {
            throw new InvalidArgumentException('path must be printable text');
        }
        foreach (explode('/', $value) as $part) {
            if ($part === '..') {
                throw new InvalidArgumentException('path must not contain ".."');
            }
        }

        return $value;
    }

    public static function database(string $value): string
    {
        if (preg_match(self::DATABASE, $value) !== 1) {
            throw new InvalidArgumentException('database name may hold only letters, digits, "_", "$" and "-"');
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
            if ($i + 1 >= $n || str_starts_with($args[$i + 1], '--')) {
                throw new InvalidArgumentException(sprintf('--%s needs a value', $name));
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

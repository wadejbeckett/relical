<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;

/**
 * An SSH ed25519 public key: the Storage Box's host key (from ssh-keyscan)
 * or the extension's own key (the .pub file shown to the administrator).
 *
 * The fingerprint is OpenSSH's `SHA256:` form (as `ssh-keygen -l` prints
 * it). The administrator confirms a fingerprint on the screen, and the key
 * is pinned only if a second scan returns a key with that same fingerprint.
 */
final class HostKey
{
    public const TYPE = 'ssh-ed25519';
    public const FINGERPRINT = '/^SHA256:[A-Za-z0-9+\/]{43}$/D';

    /** @param string $blob the decoded key (wire format) */
    private function __construct(private string $blob)
    {
    }

    /** @throws InvalidArgumentException when $base64 is not an ed25519 public key */
    public static function fromBase64(string $base64): self
    {
        $blob = base64_decode($base64, true);
        // string "ssh-ed25519" then a 32-byte key, each with a 4-byte length.
        if ($blob === false || strlen($blob) !== 51 || substr($blob, 0, 15) !== pack('N', 11) . self::TYPE || substr($blob, 15, 4) !== pack('N', 32)) {
            throw new InvalidArgumentException('not an ed25519 public key');
        }

        return new self($blob);
    }

    /**
     * The host's ed25519 key from ssh-keyscan's output. Comment lines are
     * ignored; the output must hold exactly one ed25519 key, for exactly
     * the scanned host.
     *
     * @throws InvalidArgumentException naming what was wrong
     */
    public static function fromKeyscan(string $output, Destination $destination): self
    {
        $keys = [];
        foreach (preg_split('/\R/', $output) ?: [] as $line) {
            $line = trim($line);
            if ($line === '' || $line[0] === '#') {
                continue;
            }
            $fields = preg_split('/\s+/', $line) ?: [];
            if (count($fields) < 3 || $fields[1] !== self::TYPE) {
                continue;
            }
            if ($fields[0] !== $destination->knownHostsName()) {
                throw new InvalidArgumentException(sprintf('ssh-keyscan answered for %s, not %s', self::printable($fields[0]), $destination->knownHostsName()));
            }
            $keys[] = self::fromBase64($fields[2]);
        }
        if ($keys === []) {
            throw new InvalidArgumentException(sprintf('%s did not offer an ed25519 host key', $destination->knownHostsName()));
        }
        if (count($keys) > 1) {
            throw new InvalidArgumentException(sprintf('%s offered more than one ed25519 host key', $destination->knownHostsName()));
        }

        return $keys[0];
    }

    /**
     * The extension's own public key file: one line, `ssh-ed25519 <key>`
     * and an optional printable comment.
     *
     * @return string the line as it goes into authorized_keys
     * @throws InvalidArgumentException when the file holds anything else
     */
    public static function publicKeyLine(string $content): string
    {
        $line = rtrim($content, "\r\n");
        if (preg_match('/^ssh-ed25519 ([A-Za-z0-9+\/=]+)( [\x21-\x7e][\x20-\x7e]{0,99})?$/D', $line, $m) !== 1) {
            throw new InvalidArgumentException('the public key file is not one ed25519 public key');
        }
        self::fromBase64($m[1]);

        return $line;
    }

    /** @throws InvalidArgumentException when $value is not a `SHA256:` fingerprint */
    public static function fingerprintArg(string $value): string
    {
        if (preg_match(self::FINGERPRINT, $value) !== 1) {
            throw new InvalidArgumentException('fingerprint must be SHA256: followed by 43 base64 characters');
        }

        return $value;
    }

    public function fingerprint(): string
    {
        return 'SHA256:' . rtrim(base64_encode(hash('sha256', $this->blob, true)), '=');
    }

    public function matches(string $fingerprint): bool
    {
        return hash_equals($this->fingerprint(), $fingerprint);
    }

    /** The known_hosts line that pins this key for the destination. */
    public function knownHostsLine(Destination $destination): string
    {
        return sprintf("%s %s %s\n", $destination->knownHostsName(), self::TYPE, base64_encode($this->blob));
    }

    private static function printable(string $value): string
    {
        return substr((string) preg_replace('/[^\x20-\x7e]/', '?', $value), 0, 80);
    }
}

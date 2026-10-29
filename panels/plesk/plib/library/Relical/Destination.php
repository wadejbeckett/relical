<?php

declare(strict_types=1);

namespace Noiz\Relical;

use InvalidArgumentException;

/**
 * The Storage Box the backups go to: host, SFTP port, user and base path,
 * validated with ConfigWriter's rules. It builds the argv arrays of the
 * connection checks (ssh-keyscan and an sftp probe), which root scripts run
 * without a shell; nothing here comes from a request, only from saved
 * settings.
 */
final class Destination
{
    private function __construct(
        public readonly string $host,
        public readonly int $port,
        public readonly string $user,
        public readonly string $path
    ) {
    }

    /**
     * @param array<string, mixed> $input Settings::configInput() (sftp_host, sftp_port, sftp_user, sftp_path)
     * @throws InvalidArgumentException naming the first bad value
     */
    public static function fromInput(array $input): self
    {
        $host = trim((string) ($input['sftp_host'] ?? ''));
        if ($host === '') {
            throw new InvalidArgumentException('the Storage Box host is not set yet');
        }

        return new self(
            ConfigWriter::host($host),
            ConfigWriter::port($input['sftp_port'] ?? null),
            ConfigWriter::user(trim((string) ($input['sftp_user'] ?? ''))),
            ConfigWriter::path(trim((string) ($input['sftp_path'] ?? '')))
        );
    }

    /** The host's name in known_hosts and ssh-keyscan output: `[host]:port`, or the bare host on port 22. */
    public function knownHostsName(): string
    {
        return $this->port === 22 ? $this->host : sprintf('[%s]:%d', $this->host, $this->port);
    }

    /** For the screens: `user@host:path (port N)`. */
    public function display(): string
    {
        return sprintf('%s@%s:%s (port %d)', $this->user, $this->host, $this->path, $this->port);
    }

    /**
     * ssh-keyscan for the host's ed25519 key only.
     *
     * @return list<string>
     */
    public function keyscanArgv(string $keyscan, int $timeout = 10): array
    {
        return [$keyscan, '-p', (string) $this->port, '-t', 'ed25519', '-T', (string) $timeout, $this->host];
    }

    /**
     * An sftp session in batch mode (commands on stdin, see probeCommands())
     * with the same key and pinned host key restic uses: it proves that the
     * key is authorised and the host key matches, over the protocol the
     * backups use.
     *
     * @return list<string>
     */
    public function sftpProbeArgv(string $sftp, int $connectTimeout = 10): array
    {
        return [
            $sftp, '-b', '-', '-P', (string) $this->port,
            '-i', Paths::SSH_KEY,
            '-o', 'IdentitiesOnly=yes',
            '-o', 'UserKnownHostsFile=' . Paths::KNOWN_HOSTS,
            '-o', 'StrictHostKeyChecking=yes',
            '-o', 'BatchMode=yes',
            '-o', 'ConnectTimeout=' . $connectTimeout,
            $this->user . '@' . $this->host,
        ];
    }

    /** What the probe asks the server: only where it is, which changes nothing. */
    public static function probeCommands(): string
    {
        return "pwd\n";
    }
}

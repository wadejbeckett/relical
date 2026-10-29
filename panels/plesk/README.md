# Relical Backup for Plesk

Relical Backup is a Plesk extension. It backs up each subscription's mail,
website files and databases, and the Plesk configuration, into encrypted
restic repositories on an SFTP Storage Box, and restores them beside the
live data without overwriting it. The extension bundles the `relical` core
and a pinned restic release, so the server needs nothing else.

## Installing

Install `relical-<version>.zip` from Extensions > My Extensions > Upload
Extension, or with `plesk bin extension -i relical-<version>.zip`. The
installer prints each step. It:

1. Checks the bundled `relical` and `restic` against the package's
   `SHA256SUMS`, and stops if either is missing or differs.
2. Checks that the saved settings give a valid configuration. Nothing on
   the server changes until checks 1 and 2 pass.
3. Copies both binaries into `/usr/local/lib/relical/` under staging names,
   checks each copy, and only then renames both into place (root, 0755).
   A failed copy leaves the previous pair in place.
4. Creates `/etc/relical`, `/etc/relical/keys`, `/etc/relical/ssh` and
   `/var/lib/relical` (root, 0700).
5. Generates the Storage Box key `/etc/relical/ssh/id_ed25519` if there is
   none yet, under a staging name renamed into place. Its public half goes
   into the Storage Box's `authorized_keys`.
6. Writes `/etc/relical/mysql.cnf` (0600) for the database dumps, and
   `/etc/relical/config.json` (0600) once the Storage Box destination is
   saved in the extension's settings.
7. Installs `relical-backup.service`, `relical-backup.timer`,
   `relical-check.service` and `relical-check.timer` into
   `/etc/systemd/system/` and enables both timers.

The nightly backup starts at the backup time in the settings (01:00 server
local time by default, within a random delay of up to 30 minutes) and
applies the retention policy afterwards. The weekly repository check runs on
Sunday, twelve hours after the backup time. Until the destination is saved,
both are skipped rather than failed.

If any step fails, the installer exits with an error that names the failed
step, the steps already completed and the steps not done. Running it again
is safe, because every step checks what is already there:

    plesk bin extension --exec relical post-install.php

## Upgrading

An upgrade runs the same steps. It replaces the binaries and units that
changed and rewrites the configuration from the settings. It never changes
the repository keys in `/etc/relical/keys`, the pinned host key in
`/etc/relical/ssh/known_hosts` or an existing SSH key.

## Removing

Removing the extension stops and disables both timers, stops a backup or
check that is still running, removes the four units and
`/usr/local/lib/relical/`.

It keeps `/etc/relical` and `/var/lib/relical`. The repository keys in
`/etc/relical/keys` are the only way to read the backups. Keep them, with a
copy away from the server, for as long as the backups matter. Reinstalling
the extension carries on with the same keys and repositories.

## Building

From the repository root, with Go, zip and the pinned tools (`make tools`):

    build/build-panel.sh plesk 0.2.0

This writes `dist/relical-0.2.0.zip` and `dist/relical.zip`. The version
must match `meta.xml`. The package is built from the committed tree only:
the core is compiled from the same commit, restic is extracted from the
release archive pinned in `tools/SHA256SUMS.pinned`, and both checksums go
into `plib/bin/SHA256SUMS`.

## Developing

    composer install
    composer test

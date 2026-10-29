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

The default backup time is 01:00. Phase 1 builds used 02:30; on a server
where the backup time was never saved, an upgrade moves the timer to 01:00.
Save a backup time in the settings to keep another one.

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
check that is still running, and removes the four units and the two
binaries in `/usr/local/lib/relical/`. It deletes nothing else it did not
install: if other files are in that directory, the directory is kept and the
removal names them.

It keeps `/etc/relical` and `/var/lib/relical`. The repository keys in
`/etc/relical/keys` are the only way to read the backups. Keep them, with a
copy away from the server, for as long as the backups matter. Reinstalling
the extension carries on with the same keys and repositories.

## Settings and status

Extensions > Relical Backup is for administrators only. It holds:

- The settings: which subscriptions are backed up (every subscription, or
  those whose plan includes a Relical plan item), the Storage Box host,
  SFTP port (23), user and path (`relical`), how many daily, weekly and
  monthly backups are kept, the backup time and an upload limit. Saving
  writes `/etc/relical/config.json`; a new backup time also updates the
  timers.
- The extension's public key, to add to the Storage Box's
  `authorized_keys`.
- Check connection: shows the fingerprint of the Storage Box's host key.
  "Trust this host key" pins it in `/etc/relical/ssh/known_hosts`, but only
  if the host still offers a key with that fingerprint, then tests the
  connection. Test connection opens an SFTP session with the extension's
  key and the pinned host key, lists the backup path and changes nothing.
- "Adopt repository" on a subscription whose repository key has no
  recorded owner (the core then refuses to back it up and names
  `relical adopt`): records the subscription as the owner, after a
  confirmation.
- The status of each subscription: the units backed up, the last success,
  the last error and the space used. "Back up now" backs up one
  subscription and "Run everything now" backs up all of them and the Plesk
  configuration, in the background. Neither starts while another run is in
  progress.
- The last report and the last 200 lines of `/var/lib/relical/relical.log`.
  The key, the status and the report come from one root call
  (`overview.php`), each core call cut off after a few seconds.
- Every save also re-installs the timers (only changed units are
  restarted). A blank path or backup time takes its default (`relical`,
  01:00).

Every privileged step runs as root through
`plesk bin extension --exec relical <script>` with checked arguments; the
panel itself never runs the backup tools.

## Restores for customers

A covered subscription has a "Backups" button on its page, for the customer
and for administrators. It lists the restore points of the subscription's
mail, website files and databases, and restores:

- mail of one mailbox (or one of its folders) into a new folder
  `Restored <date> <id>` in that mailbox;
- website files, one folder or everything, into a new folder
  `restored-<date>-<id>/` in the subscription's home directory, beside
  `httpdocs`, owned by the subscription's user and not served by the
  website;
- a database as `<database>.sql` in such a folder, to import with
  phpMyAdmin.

Nothing live is overwritten. A restore runs in the background
(`restore-run.php`, one at a time per subscription) and a status page shows
where the copy is when it has finished; its record is kept for 30 days in
the extension's var directory (`restores/`, root only). A customer only
ever reaches their own subscription, and never the Plesk configuration;
one whose plan does not include Relical sees a page that says so.

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

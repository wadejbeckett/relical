# Changelog

All notable changes to Relical. Versions follow [Semantic Versioning](https://semver.org/).

## 0.2.0 (unreleased)

Phase 2: full backup per hosting subscription, and the core the Plesk extension calls.

### Added

- Configuration version 2 with an inventory of subscriptions, given statically or by a panel adapter's `inventory_command` (run as root with `PATH` only, 60-second timeout, 16 MiB output cap), validated against the configured file and mail roots.
- One restic repository and key per subscription, named by the subscription; the repository string must contain `{subscription}`.
- Repository owners: the inventory's `id` is recorded next to each key, and a repository recorded as another id's is refused. `adopt --subscription S` records the id of an existing repository that has a key but no owner file.
- Units per subscription: `mail`, `files` (with `file_excludes`, `--one-file-system`, `--exclude-caches`) and `db` (MySQL dumps streamed into restic, one snapshot per database), chosen by the subscription's `features`.
- `panel-config`: the panel's own configuration backup, run by `server.config_command` into the `_server` repository, encrypted with that repository's key by default.
- Restores of files and database dumps into a new `restored-<date>-<id>/` folder in the webspace, and of the panel configuration into a root-only file; nothing live is overwritten.
- `ls` to list one level of a snapshot, and `snapshots --unit`.
- `report`: the nightly summary from the state file (counts, stored bytes, a row per subscription and unit, the inventory's renames and warnings for stale backups and checks and for subscriptions no longer in the inventory). It never runs restic, and an inventory that cannot be loaded is a warning.
- The run log, `<state_dir>/relical.log`: one JSON line per result of `backup`, `forget`, `check`, `adopt` and `restore`, plus start and end lines; repository passwords and secret destination variables redacted; rotated at 10 MiB to `relical.log.1`.
- `sftp_args` for `sftp:` destinations (passed as `-o sftp.args=`), and a Hetzner Storage Box example in the README.
- `prune: remote` reports the exact `restic forget` command the destination host must run, with credentials redacted.
- `forget` and `check` report a repository not yet created as a note rather than a failure, and fail on a lost key.
- File and database restores build the finished `restored-<date>-<id>` folder in root-only staging, `<files root>/.relical/`, and move it into the webspace with one `renameat2(RENAME_NOREPLACE)`, so a customer cannot swap the folder for another directory while it is filled; the fallback for another file system or an old kernel accepts only the empty, root-owned 0700 directory it made.
- Restores clear set-user-id and set-group-id bits and leave out device nodes, fifos and sockets, listing them as `dropped` in the result (a Plesk chroot webspace has some under `dev/`).
- File and database restores refuse to start without room on the webspace's file system for the estimated size plus 10% plus a reserve of 5% of the file system (at least 10 GiB), and without the free inodes for it; block counts use the fragment size (`f_frsize`). Disk quotas are not checked. Mail restores check both the staging file system (`<state_dir>/restore`) and the mailbox's, and need room for both copies when those are one file system.
- Staging left by a killed restore is removed after 24 hours, by the next restore or `backup --all`; a running restore holds a `flock` on its staging directory, which is never removed while it is held.
- Every `sftp:` destination gets `-o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=4 -o BatchMode=yes` unless its `sftp_args` set those options, so a destination that accepts the connection and never answers cannot hang the run.
- A `mail` backup of a subscription without mailboxes is OK with the note "no mailboxes" instead of failing every night.
- A subscription whose inventory entry is invalid, or that conflicts with another (both are left out), is listed under `skipped` with `"invalid": true` and its reasons instead of failing the whole inventory; `--all` runs report it as a failed result and exit 1, and `report` gives it a failed `inventory` row. Only `server` errors and malformed output stay fatal.
- `report` lists the inventory's `skipped` subscriptions (`skipped: [{"name", "reason"}]`), and `--all` runs write them to the run log (`"event": "skipped"`). The count of rows skipped for an unsupported database type moves from `skipped` to `skipped_rows`.
- System user names may contain `.` (Plesk allows `john.smith`), still never as the first character.
- `server_id` is passed as `--host` on `backup` only; `RESTIC_HOST` is no longer set, since restic 0.17 also takes it as the host filter of `snapshots`, `forget` and restores, which hid (and never pruned) the snapshots of an earlier `server_id`.
- `backup`, `forget` and `check` record each subscription's results in `state.json` and the run log as soon as they are known, not only when the run ends.
- Mail restores pass `--` before doveadm's positional arguments (`import`, `mailbox delete`, `mailbox status`), refuse a `--folder` that is not in the restored copy with a clear error before importing, and glob-escape the mailbox path in restic's `--include`.
- `snapshots`, `ls`, restores, `dump` and `stats` wait up to 2 minutes for a repository lock (`--retry-lock 2m`), and restic's exit 11 reads "the backup is being checked or pruned, try again in a few minutes". `check --all` waits up to 2 hours for Relical's run lock instead of failing at once. Retention still prunes every night.
- `report` warns about each unit the inventory covers that has never been backed up, and leaves out the rows of units it no longer covers (a dropped database, a removed subscription): those are reported once, within 48 hours of their last backup, as "no longer backed up", instead of a stale-backup warning every day.
- A files or database restore refused because a root-owned partial folder from an interrupted restore on the fallback path is in the way says to ask the server's administrator to remove it.
- In `--all` runs, a destination that three subscriptions in a row could not connect to is given up on: the rest fail at once with "destination NAME unreachable; not tried". `relical-backup.service` has `TimeoutStartSec=20h` as a last resort.

### Changed

- The systemd units run `/usr/local/lib/relical/relical`, at `Nice=19` and `IOSchedulingClass=idle`; the weekly check runs on Sunday at 03:00.
- `backup --all` applies retention itself at the end (`--no-forget` skips it, and an interrupted run or one restricted with `--unit` never runs it). `relical-backup.service` no longer runs `forget --all` through `ExecStopPost=`, which made a stop or a reboot during the backup wait for a prune of every repository; its stop timeout is 15 minutes instead of infinity.
- `relical-check.service` passes `--read-data-subset 5%%`, since a bare `%` in a unit file is a specifier.
- `restic_bin`, `doveadm_bin` and `mysqldump_bin` must be clean absolute paths when given.
- Database names must start with a letter, a digit or `_`, and `mysqldump` gets `--` before the name.
- Repository URLs are redacted from `://` to the last `@` (`***`), whatever the user information holds.
- `restored-*` is among the default `file_excludes`.
- Mail restores pass `doveadm import` a plain `maildir:<path>` source, which Dovecot 2.4 (Plesk 18.0.81) requires, and put the new folder after `mail_folder_prefix` (`INBOX.` on Plesk, whose Dovecot refuses a top-level folder); a `--folder` given with that prefix is found in the restored copy without it.
- The version is set at build time by the Makefile (`VERSION`, default 0.2.0); a build without it reports `0.2.0-dev`.

### Plesk extension (`panels/plesk/`)

- Tested in the official `plesk/plesk` Docker image (Plesk 18.0.81.4, Ubuntu 24.04, Dovecot 2.4.5) by `panels/plesk/tests/docker/run.sh`; results in `panels/plesk/tests/docker/TEST-PLAN.md`.
- Root work runs through the package's `sbin/relical-root` and Plesk's `mod_wrapper`, not `plesk bin extension --exec`, which runs scripts as the panel user; root scripts started as the panel user (the install hooks included) hand themselves over to it.
- Saving the settings reloads systemd again (the steps were collected in a memory stream that `proc_open` could not pass to `systemctl`).
- Redirects no longer repeat the extension's base URL.
- `restored-*` is excluded from the files backup, as in the core.
- The backup and check services run at `Nice=19` and `IOSchedulingClass=idle`.
- A subdomain without mail service is not reported as skipped in the inventory.
- The configuration sets `mail_folder_prefix` to `INBOX.`; the status page names the restored mail folder without it.

### Removed

- Configuration version 1 (`domains` and `{domain}` repositories) is refused; there is no automatic migration.

## 0.1.0 (not released)

Phase 1: mail backup per domain with restic and restore of a mailbox or folder into a new IMAP folder through `doveadm import`; systemd units for a nightly backup and a weekly check.

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
- Restores clear set-user-id and set-group-id bits, leave out fifos and sockets and refuse device nodes.
- Restores refuse to start without room for the estimated size plus 10% plus 256 MiB.
- Staging left by a killed restore is removed after 24 hours, by the next restore or `backup --all`.

### Changed

- The systemd units run `/usr/local/lib/relical/relical`, at `Nice=19` and `IOSchedulingClass=idle`; the weekly check runs on Sunday at 03:00.
- `relical-check.service` passes `--read-data-subset 5%%`, since a bare `%` in a unit file is a specifier.
- `restic_bin`, `doveadm_bin` and `mysqldump_bin` must be clean absolute paths when given.
- Database names must start with a letter, a digit or `_`, and `mysqldump` gets `--` before the name.
- Repository URLs are redacted from `://` to the last `@` (`***`), whatever the user information holds.
- `restored-*` is among the default `file_excludes`.
- The version is set at build time by the Makefile (`VERSION`, default 0.2.0); a build without it reports `0.2.0-dev`.

### Removed

- Configuration version 1 (`domains` and `{domain}` repositories) is refused; there is no automatic migration.

## 0.1.0 (not released)

Phase 1: mail backup per domain with restic and restore of a mailbox or folder into a new IMAP folder through `doveadm import`; systemd units for a nightly backup and a weekly check.

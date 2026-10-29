# Relical

Backup for hosting servers (mail, website files, databases and the panel configuration), built on restic. A panel-neutral core (`core/`) with thin control-panel adapters (`panels/`, starting with Plesk). Part of the Noiz Déjà Vu backup range.

Status: phase 2 in development (mail, website files and databases are backed up per subscription, and the panel configuration into its own repository; mail can be restored; file, database and panel configuration restores are being added). Not ready for production.

## Build and test

    make tools        # pinned restic 0.19.1 and rest-server 0.14.0 into .tools/
    make test         # unit tests
    make integration  # against real restic and an append-only rest-server
    make e2e          # backup and restore through a real Dovecot in Docker
    make build        # dist/relical, static

`make e2e` runs a `debian:12` container, installs Dovecot inside it (so it needs network access), and runs the whole backup and restore cycle against it. Set `RELICAL_DOCKER_ARGS` to pass extra arguments to `docker run`, for example `RELICAL_DOCKER_ARGS="--network host" make e2e` on a host whose default container network has no outbound access.

## Commands

    relical [-config PATH] COMMAND [flags]

The configuration file defaults to `/etc/relical/config.json`. Every command prints JSON on stdout, except usage errors (exit code 2), which print plain text to stderr. A panel adapter can call `relical` and parse the result; restic is never run by an adapter. Errors that stop a command before it has results are printed as `{"error": "..."}`.

Relical backs up **subscriptions** (a hosting account, named by its main domain). Each subscription has its own restic repository and key. The list of subscriptions is the inventory (see Configuration).

| Command | What it does |
|---|---|
| `version` | Prints the version. |
| `inventory` | Prints the inventory after `inventory_command` has run and every entry has been validated. |
| `mailboxes --subscription S` | Lists the mailboxes of every mail domain of the subscription found on disk, with login, path, and owner uid and gid. |
| `backup --subscription S` or `--all` [`--unit mail\|files\|db\|panel-config`] | Backs up a subscription's units (or those of every subscription in the inventory) into that subscription's restic repository: mail, then files, then each database, each only when it is among the subscription's `features`. With `--all`, the panel configuration (`panel-config`, into the `_server` repository) runs last when the inventory's `server` lists it. `--subscription _server` backs up the panel configuration alone, as does `--all --unit panel-config`. `--unit` restricts the run to one unit. Creates the repository and its key on first use. |
| `forget --subscription S` or `--all` | Applies the retention policy to each unit's snapshots separately and prunes. `--all` includes the `_server` repository when the inventory's `server` lists `panel-config`; it uses the default `retention`. |
| `check --subscription S` or `--all` [`--read-data-subset 5%`] | Checks repository integrity, optionally reading a share of the stored data. `--all` includes `_server` as `forget` does. |
| `snapshots --subscription S` [`--unit U`] | Lists the subscription's snapshots, or only those of one unit. `--subscription _server` names the panel configuration's repository. |
| `restore` [`--subscription S`] [`--unit mail`] `--domain D --mailbox M` [`--snapshot ID`] [`--folder F`] | Restores a mailbox, or one IMAP folder, into a new folder (see Restores). Without `--subscription`, the subscription is the one whose mail domains include `D`; with it, `D` must be one of that subscription's mail domains. `--snapshot` takes `latest` (the default) or an id of 8 to 64 hex characters. |
| `adopt --subscription S` | Records the inventory's id as the owner of an existing repository that has a key but no owner file, after confirming that the repository opens (see Repository owners). Needed once per such repository when the inventory comes from `inventory_command`. |
| `status` | Prints the last recorded result per subscription, unit and action, and each repository's stored size from the last `backup --all` (see State). |

Units are `mail`, `files`, `db` and `panel-config`. This version backs up all four, and restores `mail` only; any unit other than `mail` on `restore` is reported as not supported.

`backup`, `forget` and `check` need exactly one of `--subscription` or `--all`, and take an exclusive lock so two runs never overlap; a second run fails at once with "another relical run holds the lock". `--all` runs the subscriptions one at a time, in inventory order. Each unit gives its own result and snapshot:

- `mail`: one snapshot tagged `mail` per subscription per run, covering the mailboxes of all its mail domains. Dovecot's index and cache files and each Maildir's `tmp/` are left out.
- `files`: one snapshot tagged `files` of the subscription's `files` paths. restic stays on each path's file system (`--one-file-system`) and skips directories marked with a `CACHEDIR.TAG` (`--exclude-caches`); each `file_excludes` pattern is applied relative to each path.
- `db`: one snapshot per database, tagged `db` and `db:<name>`, holding the single file `/<name>.sql`. `mysqldump --defaults-file=<mysql.defaults_file> --single-transaction --quick --routines --triggers --events --no-tablespaces <name>` is streamed into restic (`backup --stdin-from-command`), so the dump is never written to disk. A failing dump fails that database only, and restic saves no snapshot for it. Before any dump, `mysql.defaults_file` must exist and be a regular file, not a symlink, with mode 0600 or stricter and, when Relical runs as root, owned by root; otherwise every database of the subscription fails with that error. Databases of a type other than `mysql` are reported as OK with the note "skipped: type X not supported".
- `panel-config` (subscription `_server`): Relical runs the inventory's `server.config_command` with `{output}` replaced by `<state_dir>/staging/panel-config-<UTC time>.<config_ext>` (the staging directory is mode 0700), backs that file up into the `_server` repository tagged `panel-config`, and deletes it, also when the command or restic fails; a file left by a killed run is deleted at the start of the next. The command runs with `PATH` as its only environment variable, plus `PLESK_BACKUP_PASSWORD` set to the `_server` repository's key when `server.encrypt_with_repo_key` is on (the default), so the passwords inside Plesk's backup are encrypted with that key too. It runs in its own process group and gets 30 minutes; on a timeout the whole group is killed and the unit fails. A non-zero exit, or no file written, fails the unit and nothing is backed up. **Relical never restores the panel configuration into Plesk**: a restore (being added) only writes the tar back to a root-only file, and the administrator restores from it by hand with `plesk bin pleskrestore`, giving the same `PLESK_BACKUP_PASSWORD` (the `_server` key in `keys_dir`).

A unit that is not among the subscription's `features` is not run, even if mail domains, files or databases are listed; with `--unit`, it is reported as OK with a note. A subscription with none of the features is reported as OK with a note, and nothing is run for it. If restic saved a snapshot but could not read some files (exit code 3), `mail` and `files` are still OK, with a note. After a `backup --all`, Relical runs `restic stats --mode raw-data` on each repository that received a snapshot and records its stored size; a failed `stats` is a note on that result, never a failure, and the previous size is kept.

Each run is recorded in `state.json` in the state directory, which `status` reads. `forget` makes one call per repository with `--group-by tags`, so the mail, files and each database's snapshots are kept by the retention policy independently of each other. `backup`, `forget` and `check` wait up to 30 minutes for a repository lock held by another restic process, and `forget` and `check` first remove stale locks left by a restic that was killed. A subscription whose repository does not exist yet (it has no key, because nothing has been backed up for it, such as a `db`-only subscription without databases) is OK for `forget` and `check`, with the note "no repository yet (nothing has been backed up)"; a key readable by others or a repository recorded as another id's still fails.

Exit codes: `0` success; `1` an error, or at least one subscription's job failed (the JSON still lists every subscription's result); `2` a usage error: an unknown command, an unexpected argument, a flag the command does not take, an unknown `--unit`, neither or both of `--subscription` and `--all` on `backup`, `forget` or `check`, a missing `--subscription` on `mailboxes` or `snapshots`, or a missing `--domain` or `--mailbox` on `restore`. A subscription that is not in the inventory, or an inventory that fails validation, prints `{"error": ...}` and exits `1`.

### Sample output

`relical backup --subscription example.test`:

    [
      {
        "subscription": "example.test",
        "unit": "mail",
        "action": "backup",
        "time": "2026-10-15T01:00:41.220517304Z",
        "ok": true,
        "snapshot": "b9e9540af49ca5d668c11272f8542eba4fc2faf5bc0f8b4a4f3dd0aece0111c0",
        "mailboxes": 2,
        "files_new": 3,
        "data_added": 3746,
        "seconds": 0.666874786
      }
    ]

If restic saved a snapshot but could not read some files, `ok` is still `true` and a `note` field explains. A failed job has `"ok": false` and an `error` field instead.

Result fields: `subscription`; `unit` (`mail`, `files`, `db`, `panel-config`; absent for `forget`, `check` and `stats`, which concern the whole repository); `database` (for `db`); `action` (`backup`, `forget`, `check`, `stats`); `time`; `ok`; and when they apply `snapshot`, `mailboxes`, `files_new`, `data_added` (bytes added to the repository after compression), `stored_bytes` (`stats`: the repository's size), `seconds`, `note` and `error`.

`relical backup --all` for a subscription with all three units ends with a `stats` result per repository (shortened):

    [
      {"subscription": "example.test", "unit": "mail", "action": "backup", "ok": true, "snapshot": "b9e9...", "mailboxes": 2},
      {"subscription": "example.test", "unit": "files", "action": "backup", "ok": true, "snapshot": "4f1c..."},
      {"subscription": "example.test", "unit": "db", "database": "example_wp", "action": "backup", "ok": true, "snapshot": "7a02..."},
      {"subscription": "example.test", "action": "stats", "ok": true, "stored_bytes": 52428800}
    ]

`relical snapshots --subscription example.test`:

    [
      {
        "id": "b9e9540af49ca5d668c11272f8542eba4fc2faf5bc0f8b4a4f3dd0aece0111c0",
        "time": "2026-10-15T01:00:42.181532906Z",
        "paths": [
          "/var/qmail/mailnames/example.test/alice/Maildir",
          "/var/qmail/mailnames/shop.example.test/bob/Maildir"
        ],
        "hostname": "host.example.test",
        "tags": [
          "mail"
        ]
      }
    ]

`relical mailboxes --subscription example.test` (one entry shown):

    [
      {
        "domain": "example.test",
        "mailbox": "alice",
        "login": "alice@example.test",
        "path": "/var/qmail/mailnames/example.test/alice/Maildir",
        "uid": 10001,
        "gid": 10001
      }
    ]

`relical forget --subscription example.test` and `relical check --subscription example.test`:

    [
      {
        "subscription": "example.test",
        "action": "check",
        "time": "2026-10-15T05:00:12.903471882Z",
        "ok": true
      }
    ]

`relical status` prints `state.json`: subscription, then unit (`mail`, `files`, `db:<name>` per database, or `repository` for `forget`, `check` and `stats`), then action, then the last result (shortened):

    {
      "example.test": {
        "mail": {
          "backup": {"subscription": "example.test", "unit": "mail", "action": "backup", "time": "2026-10-15T01:00:41Z", "ok": true, "snapshot": "b9e9...", "mailboxes": 2}
        },
        "files": {
          "backup": {"subscription": "example.test", "unit": "files", "action": "backup", "time": "2026-10-15T01:02:10Z", "ok": true, "snapshot": "4f1c..."}
        },
        "db:example_wp": {
          "backup": {"subscription": "example.test", "unit": "db", "database": "example_wp", "action": "backup", "time": "2026-10-15T01:02:14Z", "ok": true, "snapshot": "7a02..."}
        },
        "repository": {
          "stats": {"subscription": "example.test", "action": "stats", "time": "2026-10-15T01:02:20Z", "ok": true, "stored_bytes": 52428800},
          "check": {"subscription": "example.test", "action": "check", "time": "2026-10-15T05:00:12Z", "ok": true}
        }
      }
    }

`relical restore --subscription example.test --domain example.test --mailbox alice`:

    {
      "subscription": "example.test",
      "unit": "mail",
      "domain": "example.test",
      "mailbox": "alice",
      "snapshot": "b9e9540af49ca5d668c11272f8542eba4fc2faf5bc0f8b4a4f3dd0aece0111c0",
      "restored_to": "Restored 2026-10-15 b9e9540a",
      "messages": 3
    }

`relical inventory` (one subscription; `file_excludes` shows the defaults that apply when none are given):

    {
      "version": 2,
      "subscriptions": [
        {
          "id": "17",
          "name": "example.test",
          "renamed_from": "",
          "sys_user": "exampleuser",
          "uid": 10001,
          "gid": 1004,
          "home": "/var/www/vhosts/example.test",
          "features": ["mail", "files", "db"],
          "mail_domains": ["example.test", "shop.example.test"],
          "files": ["/var/www/vhosts/example.test"],
          "file_excludes": ["logs/**", "statistics/logs/**", "tmp/**", ".cagefs/**"],
          "databases": [{"type": "mysql", "name": "example_wp"}],
          "destination": "",
          "retention": null
        }
      ],
      "server": {
        "features": ["panel-config"],
        "config_command": ["/usr/sbin/plesk", "bin", "pleskbackup", "--server", "-exclude-mail", "-exclude-files", "-exclude-databases", "-exclude-logs", "-include-server-settings", "-output-file", "{output}"],
        "config_ext": "tar",
        "encrypt_with_repo_key": true
      },
      "renamed": [],
      "skipped": []
    }

## Restores

A restore never overwrites mail. It restores the snapshot into a staging directory and imports it with `doveadm import` into a new IMAP folder named `Restored <snapshot date> <short id>` (with `--folder`, only that folder is imported). The user then copies what they need back. Restoring the same snapshot twice is refused while the first "Restored" folder still exists; delete or rename that folder first. A restore fails, and removes any partial folder it created, if the mailbox is not in the snapshot, if `doveadm` reports that it could not read the restored copy, or if the folder does not exist afterwards. Restores need `doveadm` and run as root.

## Configuration

Copy `packaging/config.example.json` to `/etc/relical/config.json` and set mode `0600`; Relical refuses a configuration readable by others. Unknown keys are rejected. The main settings:

- `version`: must be `2`. A version 1 configuration (with `domains` and `{domain}` repositories) is refused; there is no automatic migration.
- `roots`: the directories inventory paths must lie under: `files` (default `["/var/www/vhosts"]`) and `mail` (default `["/var/qmail/mailnames"]`).
- `mailbox_pattern`: where a mail domain's mailboxes live, with `{domain}` and `{mailbox}` once each, under a mail root (default `/var/qmail/mailnames/{domain}/{mailbox}/Maildir`, the Plesk layout).
- `destinations`: named restic repositories. `repository` must contain `{subscription}` once, so each subscription gets its own repository (the panel configuration uses the name `_server`); `env` holds the variables restic needs to reach it. For an `sftp:` repository, `sftp_args` (one line) is passed to restic as `-o sftp.args=<value>`, for example `-p 23 -i /etc/relical/ssh/id_ed25519`. restic splits the value on spaces and cannot quote, so it must not contain `"` and no path in it may contain a space; commas are kept. When an `sftp://host:port/` repository URL carries a port, `sftp_args` must not set one as well (`-p` or `-o Port`). `{subscription}` must be a whole path component of the repository (after `/` or `:`, followed by `/` or the end). `default_destination` applies to every subscription that does not name its own `destination`.
- `prune` (required in each destination, `"local"` or `"remote"`): `local` runs `forget --prune` from this server. `remote` is for append-only destinations (for example rest-server with `--append-only`): the server cannot delete there, so `relical forget` does nothing but report that pruning runs on the destination host. Its note gives, per repository, the exact command the destination host must run, with that subscription's retention and `--group-by tags` (for example `restic forget --group-by tags --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune`); without `--group-by tags`, restic groups by host and paths, and the mail, files and database snapshots would not be kept as intended.
- `retention`: snapshots to keep (daily, weekly, monthly; default 14, 8, 12). A subscription's own `retention` overrides it.
- `limit_upload_kib` and `low_priority`: limit upload bandwidth and run restic under `nice` and `ionice`.
- `mysql.defaults_file`: the MySQL client options file (mode 0600, owned by root, not a symlink) used for database dumps. `mysqldump_bin` names the `mysqldump` binary (default `mysqldump`, found on `PATH`).
- `keys_dir`: a random restic password per subscription is created here on the first backup (`<subscription>.key`, mode 0600). Create the directory beforehand with mode 0700; `state_dir` is created automatically. Losing a key loses that subscription's backups, so keep a copy somewhere safe. The panel configuration's key is `_server.key`; with `encrypt_with_repo_key` it is also the password of the Plesk backups inside that repository. `state_dir` holds `staging/` (mode 0700), where the panel configuration is written for the moment it takes to back it up.
- Repository owners: on a subscription's first backup, its inventory `id` is written next to its key (`<subscription>.id`, mode 0600). Every later `backup`, `snapshots`, `restore`, `forget` and `check` compares the inventory's id with it and refuses on a mismatch, naming both ids. This stops a subscription that reuses a name (a deleted subscription's name taken by a new customer, or a rename onto an old name) from writing into, or restoring from, the previous owner's repository. To give the name a new repository, move the old `.key` and `.id` files and the repository itself aside; Relical never deletes them. The `_server` repository records the owner id `_server`. A subscription without an `id` (possible only in a static inventory) records no owner, so its repository has no protection against a reused name.

  A repository can have a key but no `.id` file: it was first backed up from a static inventory without ids, or by a build from before owner files. With a static inventory, the first subscription with an id to use it is recorded as its owner, as before. With `inventory_command`, Relical refuses such a repository (backup, snapshots, restore, forget and check), because it may be an earlier subscription's of the same name; when it is this subscription's, run `relical adopt --subscription S` once. `adopt` confirms that the repository opens with the key, then records the inventory's id; it never creates a key or a repository and never replaces a recorded owner.

### The inventory

The inventory says which subscriptions are covered. It is given in one of two ways, never both:

- statically, in the configuration, as `subscriptions` (and optionally `server`), as in `packaging/config.example.json`;
- or by a panel adapter, as `inventory_command`: a command (first element an absolute path) that Relical runs as root before every command that needs the inventory, and whose standard output is the inventory JSON:

      "inventory_command": ["/usr/sbin/plesk", "bin", "extension", "--exec", "relical", "inventory.php"]

  The output must be `{"version": 2, "subscriptions": [...]}`, optionally with `server` (unless the configuration has one) and `skipped`; every subscription must have an `id`. The command runs with `PATH` as its only environment variable, in its own process group. It gets 60 seconds and at most 16 MiB of output; on a timeout the whole process group is killed. A non-zero exit fails the run with its exit code and standard error.

Each subscription has:

- `name`: the subscription's main domain (lower case), which names its repository and key; `_server` is reserved. `id` is the panel's stable id, which ties the repository to the subscription (see Repository owners); required from `inventory_command`, optional in a static inventory.
- `renamed_from`: the subscription's previous name, when the panel renamed it (two subscriptions cannot give the same one). Backups then go to a new repository under the new name; the old repository is left as it is, and `relical inventory` lists the rename under `renamed`.
- `features`: which units are backed up: `mail`, `files`, `db`.
- `mail_domains`: the domains whose mailboxes belong to the subscription. A mail domain belongs to one subscription only, and may not be another subscription's name.
- `sys_user`, `uid`, `gid`, `home`: the system user and webspace root, which file and database restores write into. They may be left empty (uid and gid 0) only when `features` contains neither `files` nor `db`. `home` must be `<files root>/<name>`; no two subscriptions' homes may be equal or nested. `uid` and `gid` must be those of `sys_user` on the system: they are not checked against it here, and file and database restores will check them before writing.
- `files`: the backed-up directories (usually `[home]`), each equal to `home` or under it; `file_excludes`: patterns relative to each of them (default `logs/**`, `statistics/logs/**`, `tmp/**`, `.cagefs/**`).
- `databases`: `{"type": "mysql", "name": "..."}` entries. A database (type and name) may be listed by one subscription only, and MySQL's and the panel's own databases are refused: `mysql`, `information_schema`, `performance_schema`, `sys`, `psa`, `apsc`, `horde`, `roundcubemail`, `atmail`, `sitebuilder5` and any name starting with `phpmyadmin` (compared without regard to case). Dumps run with the administrator's credentials, so either mistake would put another customer's, or every customer's, data into one subscription's repository.
- `destination` and `retention`: overrides of the defaults, or empty and `null`.

`server` describes the panel's own configuration backup: `features` (`["panel-config"]`), `config_command` (first element an absolute path, `{output}` exactly once, where the command must write the file), `config_ext` (the file's extension, 1-8 lower-case letters or digits) and `encrypt_with_repo_key` (default `true`; see the `panel-config` unit under Commands). `skipped`, from an adapter, lists subscriptions it left out (names, or objects with `name` and `reason`); it is informational.

## Scheduling

`packaging/systemd` holds a nightly backup and a weekly check as systemd units and timers. Both timers use the server's local time (the backup starts between 01:00 and 01:30 daily, because of `RandomizedDelaySec=30min`; the check starts Sunday 05:00); override them with `systemctl edit relical-backup.timer`. The backup unit runs `relical forget --all` through `ExecStopPost=`, so retention runs after every backup run, even one where a backup failed. The weekly check fails on Relical's own lock if it starts while a backup is still running, and is then not retried until the next week.

Licensed GPLv3.

## Support this project

Relical is free software from Noiz. If it is useful to you, the main way to support its development is a donation in Monero (XMR):

```text
44BtMn9izxH8mK2yFbSdY6Di7TNobkLbnHdZ6gZQjukCME5vsNhtPRtH4TcVkDHKHLhSpAJbsjv8gCdYuSZVMpXgMkUC1hV
```

You can also help by reporting bugs, sending fixes or starring the repository.

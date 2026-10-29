# Relical

Backup for hosting servers (mail, website files, databases and the panel configuration), built on restic. A panel-neutral core (`core/`) with thin control-panel adapters (`panels/`, starting with Plesk). Part of the Noiz Déjà Vu backup range.

Status: phase 2 in development (mail, website files and databases are backed up per subscription, and the panel configuration into its own repository; each can be restored next to the live data). Phase 2 in development: pilot pending.

## Build and test

    make tools        # pinned restic 0.19.1 and rest-server 0.14.0 into .tools/
    make test         # unit tests
    make integration  # against real restic and an append-only rest-server
    make e2e          # backup and restore through a real Dovecot and MariaDB in Docker
    make build        # dist/relical, static

`make e2e` runs a `debian:12` container, installs Dovecot and MariaDB inside it (so it needs network access), and runs the whole backup and restore cycle against it. Set `RELICAL_DOCKER_ARGS` to pass extra arguments to `docker run`, for example `RELICAL_DOCKER_ARGS="--network host" make e2e` on a host whose default container network has no outbound access.

## Commands

    relical [-config PATH] COMMAND [flags]

The configuration file defaults to `/etc/relical/config.json`. Every command prints JSON on stdout, except usage errors (exit code 2), which print plain text to stderr. A panel adapter calls `relical` and parses the result. **Panel adapters never run restic**: every repository operation goes through the core, which holds the keys. Errors that stop a command before it has results are printed as `{"error": "..."}`.

Relical backs up **subscriptions** (a hosting account, named by its main domain). Each subscription has its own restic repository and key. The list of subscriptions is the inventory (see Configuration).

| Command | What it does |
|---|---|
| `version` | Prints the version. |
| `inventory` | Prints the inventory after `inventory_command` has run and every entry has been validated. |
| `mailboxes --subscription S` | Lists the mailboxes of every mail domain of the subscription found on disk, with login, path, and owner uid and gid. |
| `backup --subscription S` or `--all` [`--unit mail\|files\|db\|panel-config`] [`--no-forget`] | Backs up a subscription's units (or those of every subscription in the inventory) into that subscription's restic repository: mail, then files, then each database, each only when it is among the subscription's `features`. With `--all`, the panel configuration (`panel-config`, into the `_server` repository) runs last when the inventory's `server` lists it. `--subscription _server` backs up the panel configuration alone, as does `--all --unit panel-config`. `--unit` restricts the run to one unit. Creates the repository and its key on first use. `--all` without `--unit` then applies retention to every repository, as `forget --all` does, unless the run was interrupted or `--no-forget` is given. |
| `forget --subscription S` or `--all` | Applies the retention policy to each unit's snapshots separately and prunes. `--all` includes the `_server` repository when the inventory's `server` lists `panel-config`; it uses the default `retention`. |
| `check --subscription S` or `--all` [`--read-data-subset 5%`] | Checks repository integrity, optionally reading a share of the stored data. `--all` includes `_server` as `forget` does. |
| `snapshots --subscription S` [`--unit U`] | Lists the subscription's snapshots, or only those of one unit. `--subscription _server` names the panel configuration's repository. |
| `restore` [`--subscription S`] [`--unit mail`] `--domain D --mailbox M` [`--snapshot ID`] [`--folder F`] | Restores a mailbox, or one IMAP folder, into a new folder (see Restores). Without `--subscription`, the subscription is the one whose mail domains include `D`; with it, `D` must be one of that subscription's mail domains. `--snapshot` takes `latest` (the default) or an id of 8 to 64 hex characters. |
| `restore --subscription S --unit files` [`--snapshot ID`] [`--path P`] | Restores the webspace, or the path `P` under it, into `<home>/restored-<snapshot date>-<short id>/` (see Restores). `P` is relative to `home`, for example `httpdocs` or `httpdocs/wp-content`. |
| `restore --subscription S --unit db --database D` [`--snapshot ID`] | Writes database `D`'s dump to `<home>/restored-<snapshot date>-<short id>/D.sql`; it is not imported. |
| `restore --subscription _server --unit panel-config` [`--snapshot ID`] | Writes the panel configuration file to `<state_dir>/restore/panel-config-<snapshot date>-<short id>.<ext>` (the backed-up file's extension, from `config_ext`; `tar` when it has none), readable by root only. |
| `ls --subscription S --snapshot ID` [`--path P`] | Lists one directory level of a snapshot, at most 5,000 entries (`"truncated": true` when there are more). `P` is relative to `home` for a `files` snapshot and to `/` for a `db` or `mail` one (a `db` snapshot holds `/<name>.sql`); for `_server` it lists the panel configuration file and takes no `--path`. `ID` must be an id (8 to 64 hex characters), not `latest`. A path that is not in the snapshot is an error. |
| `adopt --subscription S` | Records the inventory's id as the owner of an existing repository that has a key but no owner file, after confirming that the repository opens (see Repository owners). Needed once per such repository when the inventory comes from `inventory_command`. |
| `status` | Prints the last recorded result per subscription, unit and action, and each repository's stored size from the last `backup --all` (see State, report and log). |
| `report` | Prints the nightly summary from the state file: counts, failures, stored bytes, a row per subscription and unit, the inventory's renames and warnings (see State, report and log). Never runs restic. |

Units are `mail`, `files`, `db` and `panel-config`. This version backs up and restores all four. `restore` without `--unit` restores mail.

`backup`, `forget` and `check` need exactly one of `--subscription` or `--all`. They and `adopt` take an exclusive lock so two runs never overlap; a second run fails at once with "another relical run holds the lock". `--all` runs the subscriptions one at a time, in inventory order. Each unit gives its own result and snapshot:

- `mail`: one snapshot tagged `mail` per subscription per run, covering the mailboxes of all its mail domains. Dovecot's index and cache files and each Maildir's `tmp/` are left out. A subscription whose mail domains have no mailbox (panels switch mail on by default, so many website-only subscriptions have mail domains without one) is OK with the note "no mailboxes", and restic is not run.
- `files`: one snapshot tagged `files` of the subscription's `files` paths. restic stays on each path's file system (`--one-file-system`) and skips directories marked with a `CACHEDIR.TAG` (`--exclude-caches`); each `file_excludes` pattern is applied relative to each path.
- `db`: one snapshot per database, tagged `db` and `db:<name>`, holding the single file `/<name>.sql`. `mysqldump --defaults-file=<mysql.defaults_file> --single-transaction --quick --routines --triggers --events --no-tablespaces -- <name>` is streamed into restic (`backup --stdin-from-command`), so the dump is never written to disk. A failing dump fails that database only, and restic saves no snapshot for it. Before any dump, `mysql.defaults_file` must exist and be a regular file, not a symlink, with mode 0600 or stricter and, when Relical runs as root, owned by root; otherwise every database of the subscription fails with that error. Databases of a type other than `mysql` are reported as OK with the note "skipped: type X not supported".
- `panel-config` (subscription `_server`): Relical runs the inventory's `server.config_command` with `{output}` replaced by `<state_dir>/staging/panel-config-<UTC time>.<config_ext>` (the staging directory must be mode 0700 and, when Relical runs as root, owned by root), backs that file up into the `_server` repository tagged `panel-config`, and deletes it, also when the command or restic fails; a file left by a killed run is deleted by the next command that takes the run lock (`backup`, `forget`, `check` or `adopt`). The command runs with `PATH` as its only environment variable, plus `PLESK_BACKUP_PASSWORD` set to the `_server` repository's key when `server.encrypt_with_repo_key` is on (the default), so the passwords inside Plesk's backup are encrypted with that key too. It runs in its own process group and gets 30 minutes; on a timeout or an interruption the whole group is killed and the unit fails. The group is also killed when the command exits, so no child it left behind can still write the file. A non-zero exit, or no file written, fails the unit and nothing is backed up. **Relical never restores the panel configuration into Plesk**: a restore only writes the tar back to a root-only file, and the administrator restores from it by hand with `plesk bin pleskrestore`, giving the same `PLESK_BACKUP_PASSWORD` (the `_server` key in `keys_dir`).

A unit that is not among the subscription's `features` is not run, even if mail domains, files or databases are listed; with `--unit`, it is reported as OK with a note. A subscription with none of the features is reported as OK with a note, and nothing is run for it. If restic saved a snapshot but could not read some files (exit code 3), `mail` and `files` are still OK, with a note. After a `backup --all`, Relical runs `restic stats --mode raw-data` on each repository that received a snapshot and records its stored size; a failed `stats` is a note on that result, never a failure, and the previous size is kept.

Each run is recorded in `state.json` in the state directory, which `status` reads. `backup`, `forget` and `check` record each subscription's results, and write them to the run log, as soon as that subscription is done, so a run that is killed or hangs still leaves what it did. `forget` makes one call per repository with `--group-by tags`, so the mail, files and each database's snapshots are kept by the retention policy independently of each other. `backup`, `forget` and `check` wait up to 30 minutes for a repository lock held by another restic process, and `forget` and `check` first remove stale locks left by a restic that was killed. A subscription whose repository does not exist yet (it has no key, because nothing has been backed up for it, such as a `db`-only subscription without databases) is OK for `forget` and `check`, with the note "no repository yet (nothing has been backed up)"; a key readable by others, a repository recorded as another id's, or an owner file (`<subscription>.id`) without its key (the repository may exist, and its key is lost) still fails. With `prune: remote`, `forget` only reports the command the destination host must run; the repository in that note has the whole user information of its URL (everything between `://` and the last `@`) replaced by `***`.

Exit codes: `0` success; `1` an error, or at least one subscription's job failed (the JSON still lists every subscription's result); `2` a usage error: an unknown command, an unexpected argument, a flag the command does not take, an unknown `--unit`, neither or both of `--subscription` and `--all` on `backup`, `forget` or `check`, a missing `--subscription` on `mailboxes`, `snapshots` or `adopt`, any flag on `report`, a missing `--subscription` or `--snapshot` on `ls`, or a `restore` without the flags its unit needs (`--domain` and `--mailbox` for mail, `--database` for db, `--subscription` for every unit but mail) or with one it does not take (`--folder` is for mail only, `--path` for files only). A subscription that is not in the inventory, or an inventory that cannot be read or whose `server` is invalid, prints `{"error": ...}` and exits `1`. A subscription whose own entry is invalid is left out instead (see The inventory): an `--all` run lists it first as a failed result and exits `1`, and backs up the others.

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
      "restored_to": "INBOX.Restored 2026-10-15 b9e9540a",
      "messages": 3
    }

`relical restore --subscription example.test --unit files --path httpdocs`:

    {
      "subscription": "example.test",
      "unit": "files",
      "snapshot": "4f0a3c2e9d1b7a6c5e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f",
      "path": "httpdocs",
      "restored_to": "/var/www/vhosts/example.test/restored-2026-10-15-4f0a3c2e",
      "files": 1824,
      "bytes": 73400320,
      "dropped": []
    }

`relical restore --subscription example.test --unit db --database example_wp`:

    {
      "subscription": "example.test",
      "unit": "db",
      "database": "example_wp",
      "snapshot": "7c1d9e0f2a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5",
      "restored_to": "/var/www/vhosts/example.test/restored-2026-10-15-7c1d9e0f/example_wp.sql",
      "bytes": 1048576,
      "note": "the dump is not imported; load it into a database with the mysql client or phpMyAdmin"
    }

`relical ls --subscription example.test --snapshot 4f0a3c2e --path httpdocs`:

    {
      "subscription": "example.test",
      "snapshot": "4f0a3c2e9d1b7a6c5e8f0a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f",
      "path": "/var/www/vhosts/example.test/httpdocs",
      "entries": [
        {"name": "index.php", "type": "file", "size": 405, "mtime": "2026-09-30T10:12:44Z"},
        {"name": "wp-content", "type": "dir", "size": 0, "mtime": "2026-10-14T21:03:10Z"}
      ],
      "truncated": false
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
          "file_excludes": ["logs/**", "statistics/logs/**", "tmp/**", ".cagefs/**", "restored-*"],
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

## State, report and log

`state.json` in the state directory holds the last result per subscription, unit (`mail`, `files`, `db:<name>`, `panel-config`, or `repository` for `forget`, `check` and `stats`) and action. `status` prints it as it is.

`report` summarises it for a panel or a nightly mail. It reads the state file and the inventory and never runs restic:

- `generated`: when the report was made (RFC 3339, UTC).
- `rows`: the latest `backup` of each subscription's unit, sorted by subscription and then unit, with `ok`, `time`, `error` and `stored_bytes`: the subscription repository's size from its last `stats`, repeated on each of its rows.
- `ok`, `failed`, `skipped_rows`: those rows counted. A row is skipped when it is OK with a note that starts with "skipped" (a database of an unsupported type); skipped rows are not counted as OK.
- `skipped`: the subscriptions the inventory left out, as `{"name", "reason"}`: those the panel adapter skipped (informational), and those whose entry is invalid, which also have a failed row with unit `inventory` (see The inventory).
- `subscriptions`: the subscriptions with at least one row, not counting `_server`.
- `stored_bytes`: every repository's size from its last `stats`, each counted once.
- `renamed`: the inventory's renames (`name`, `renamed_from`, `note`). A rename starts a new repository under the new name and leaves the old one in place, untouched; `status` does not show this, because it reads only the state file.
- `warnings`: a backup older than 48 hours, a `check` older than 10 days (or none at all for a subscription first backed up more than 10 days ago), a subscription in the state file that is no longer in the inventory, and an inventory that could not be loaded.

`report` exits 0 whenever the state file can be read; an inventory that fails to load (the `inventory_command` failing, for example) is a warning, so the report always renders. Only an unreadable state file is an error.

Sample `relical report` (one database failed, one of an unsupported type, one subscription removed from the panel, one renamed and not backed up for three days):

    {
      "generated": "2026-10-29T06:04:40Z",
      "subscriptions": 3,
      "ok": 5,
      "failed": 1,
      "skipped_rows": 1,
      "stored_bytes": 1684013056,
      "rows": [
        {"subscription": "_server", "unit": "panel-config", "ok": true, "time": "2026-10-29T01:25:00Z", "error": "", "stored_bytes": 20971520},
        {"subscription": "closed.test", "unit": "mail", "ok": true, "time": "2026-10-29T01:01:00Z", "error": "", "stored_bytes": 0},
        {"subscription": "example.test", "unit": "db:example_pg", "ok": true, "time": "2026-10-29T01:15:02Z", "error": "", "stored_bytes": 1610612736},
        {"subscription": "example.test", "unit": "db:example_wp", "ok": false, "time": "2026-10-29T01:15:02Z", "error": "mysqldump: exit status 2: Got error: 1045: Access denied", "stored_bytes": 1610612736},
        {"subscription": "example.test", "unit": "files", "ok": true, "time": "2026-10-29T01:14:40Z", "error": "", "stored_bytes": 1610612736},
        {"subscription": "example.test", "unit": "mail", "ok": true, "time": "2026-10-29T01:12:04Z", "error": "", "stored_bytes": 1610612736},
        {"subscription": "shop.test", "unit": "mail", "ok": true, "time": "2026-10-26T01:05:00Z", "error": "", "stored_bytes": 52428800}
      ],
      "renamed": [
        {"name": "shop.test", "renamed_from": "oldshop.test", "note": "renamed from oldshop.test: backups now go to a new repository for shop.test; the repository for oldshop.test is kept as it is and is no longer backed up, pruned or checked"}
      ],
      "skipped": [
        {"name": "parked.test", "reason": "no hosting and no mail"}
      ],
      "warnings": [
        "closed.test: in the state file but no longer in the inventory; its repository is kept",
        "example.test: the last check (2026-10-12T03:10:00Z) is older than 10 days",
        "shop.test mail: the last backup (2026-10-26T01:05:00Z) is older than 48 hours"
      ]
    }

`backup`, `forget`, `check`, `adopt` and `restore` also append to the run log, `<state_dir>/relical.log` (mode 0600): one JSON line when the command starts (`"event": "start"`, with its arguments and process id), for an `--all` run one line with the subscriptions the inventory left out (`"event": "skipped"`, the inventory's `skipped` list) when there are any, one per result (`"event": "result"`, the result as the command prints it, or `"error"` when the command stopped before it had results) and one when it ends (`"event": "end"`, with the exit code). Every line has `time` (UTC) and `command`. The user information of repository URLs (everything between `://` and the last `@` of the word), and the values of destination `env` variables whose names contain `PASSWORD`, `SECRET`, `KEY`, `TOKEN` or `CREDENTIAL`, are replaced by `***`. When the log would grow past 10 MiB it is renamed to `relical.log.1`, replacing the previous one, and a new log is started. A log that cannot be written is a warning on stderr, never a failure.

## Restores

A restore never overwrites mail. It restores the snapshot into a staging directory and imports it with `doveadm import` into a new IMAP folder named `Restored <snapshot date> <short id>` (with `--folder`, only that folder is imported). The user then copies what they need back. Restoring the same snapshot twice is refused while the first "Restored" folder still exists; delete or rename that folder first. Before anything is restored, Relical estimates the mailbox's size (the file sizes `restic ls --recursive` lists under it) and refuses with "not enough free space for this restore in DIR (need X, free Y)" unless the staging file system (`<state_dir>/restore`) and the mailbox's file system each have that size plus 10% plus the reserve free; when they are the same file system it must hold both copies. A restore fails, and removes any partial folder it created, if the mailbox is not in the snapshot, if `doveadm` reports that it could not read the restored copy, or if the folder does not exist afterwards. Restores need `doveadm` and run as root.

File and database restores never write into the live webspace either. Before anything runs, Relical checks that `sys_user` exists with the inventory's `uid` and `gid`, that `home` is `<files root>/<name>`, is a directory and not a symlink, and is owned by `uid`; a `--path` must be relative and clean, at most 4096 bytes, without `..`, a leading `-` or control characters, and must exist in the snapshot (checked with `restic ls`, because `restic restore` succeeds having restored nothing for a path it does not have). Relical then estimates the restore's size (`restic stats --mode restore-size` for a whole snapshot, the file sizes `restic ls --recursive` lists for a path or a dump) and refuses with "not enough free space for this restore in DIR (need X, free Y)" unless the webspace file system has that size plus 10% plus 256 MiB free.

The finished `restored-<snapshot date>-<short id>/` folder is built in full in a root-only staging directory, `<files root>/.relical/` (mode 0700, owned by root; created when missing, and a restore is refused if it is a symlink or has another owner or mode). It sits next to the homes, so it is on the same file system and inside none of them. There the folder and everything in it are handed to the subscription's user (owners changed with `lchown`, so a symlink is never followed), and then the folder is moved into `home` with a single `renameat2(RENAME_NOREPLACE)` relative to the opened `home`: it appears complete or not at all, never replaces an entry (a folder of that name created meanwhile makes the restore fail with "already exists") and never follows a symlink. Only when that rename is not possible (`home` on another file system than its files root, or a kernel older than 3.15) does Relical create the folder in `home` and fill it there, after checking that what it opened is the empty, root-owned, mode 0700 directory it just made; anything else (the user having swapped it for another directory) stops the restore without touching it. Then:

- `files`: the snapshot is restored with `restic restore` into the staging directory, keeping its layout under `home`: `--path httpdocs` gives `restored-…/httpdocs/`. Set-user-id and set-group-id bits are cleared from files and directories, device nodes, fifos and sockets are left out (a Plesk chroot webspace holds some under `dev/`), and the result lists them as `dropped` (paths relative to `restored_to`, at most 100 and then `... and N more`). On the copy path (another file system), modes and owners are kept and times are not.
- `db`: the dump (`/<name>.sql` in a snapshot tagged `db:<name>`) is streamed with `restic dump` into a temporary file (with a random suffix) in the staging folder, and renamed to `<name>.sql` (mode 0600, owned by the user) once complete; a failed dump leaves nothing in `home`. The database does not have to be in the current inventory, so a database deleted since can still be restored: a `db:<name>` snapshot in the subscription's own repository is enough, because only a backup of that subscription's database can have written it. The name must still pass the inventory's database name rule. The dump is never imported.
- `panel-config`: the file is streamed with `restic dump` into a temporary file with a random suffix in `<state_dir>/restore`, then renamed, without replacing anything, to `panel-config-<snapshot date>-<short id>.<ext>` (mode 0600, root). Relical never runs `plesk bin pleskrestore`: the administrator restores from the file with it, giving the `_server` key as `PLESK_BACKUP_PASSWORD` when `encrypt_with_repo_key` is on, and deletes the file afterwards.

As for mail, a second restore of the same snapshot is refused while its `restored-…` folder (or panel configuration file) exists; delete or rename it first.

A restore that is killed (SIGKILL, out of memory, power loss) can leave its staging behind, never a partial folder in `home`. Every restore holds an exclusive `flock` on its own staging directory while it runs. Every restore, and every `backup --all` after it takes the run lock, removes staging older than 24 hours whose lock nobody holds (so a restore that runs for more than a day keeps its staging): everything in each `<files root>/.relical/`, and mail staging (`run-*`), staging from earlier builds (`files-*`) and temporary `.*.part` files in `<state_dir>/restore`.

## Configuration

Copy `packaging/config.example.json` to `/etc/relical/config.json` and set mode `0600`; Relical refuses a configuration readable by others. Unknown keys are rejected. The main settings:

- `version`: must be `2`. A version 1 configuration (with `domains` and `{domain}` repositories) is refused; there is no automatic migration.
- `server_id` (required): the host name every backup is recorded under (`restic backup --host`). It is only a label: `snapshots`, `ls`, restores and `forget` see every snapshot in a subscription's repository, whichever `server_id` made it, so changing it, or moving a subscription to another server that writes to the same repository, hides nothing and leaves nothing unpruned.
- `roots`: the directories inventory paths must lie under: `files` (default `["/var/www/vhosts"]`) and `mail` (default `["/var/qmail/mailnames"]`).
- `mailbox_pattern`: where a mail domain's mailboxes live, with `{domain}` and `{mailbox}` once each, under a mail root (default `/var/qmail/mailnames/{domain}/{mailbox}/Maildir`, the Plesk layout).
- `mail_folder_prefix`: the prefix of Dovecot's inbox namespace, put in front of the `Restored <date> <id>` folder a mail restore creates. Plesk's Dovecot needs `"INBOX."` (its folders are `INBOX.Sent` and the like); empty (the default) suits Dovecot's default namespace. A `--folder` given with this prefix (`INBOX.Sent`) is the same as without it (`Sent`).
- `destinations`: named restic repositories. `repository` must contain `{subscription}` once, so each subscription gets its own repository (the panel configuration uses the name `_server`); `env` holds the variables restic needs to reach it. For an `sftp:` repository, `sftp_args` (one line) is passed to restic as `-o sftp.args=<value>`, for example `-p 23 -i /etc/relical/ssh/id_ed25519`. restic splits the value on spaces and cannot quote, so it must not contain `"` and no path in it may contain a space; commas are kept. When an `sftp://host:port/` repository URL carries a port, `sftp_args` must not set one as well (`-p` or `-o Port`). `{subscription}` must be a whole path component of the repository (after `/` or `:`, followed by `/` or the end). `default_destination` applies to every subscription that does not name its own `destination`.
- `prune` (required in each destination, `"local"` or `"remote"`): `local` runs `forget --prune` from this server. `remote` is for append-only destinations (for example rest-server with `--append-only`): the server cannot delete there, so `relical forget` does nothing but report that pruning runs on the destination host. Its note gives, per repository, the exact command the destination host must run, with that subscription's retention and `--group-by tags` (for example `restic forget --group-by tags --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune`); without `--group-by tags`, restic groups by host and paths, and the mail, files and database snapshots would not be kept as intended.
- `retention`: snapshots to keep (daily, weekly, monthly; default 14, 8, 12). A subscription's own `retention` overrides it.
- `limit_upload_kib` and `low_priority`: limit upload bandwidth and run restic under `nice` and `ionice`.
- `mysql.defaults_file`: the MySQL client options file (mode 0600, owned by root, not a symlink) used for database dumps. `mysqldump_bin` names the `mysqldump` binary (default `/usr/bin/mysqldump`). `restic_bin`, `doveadm_bin` and `mysqldump_bin` must be clean absolute paths when given; left out, `restic` and `doveadm` are found on `PATH`.
- `keys_dir`: a random restic password per subscription is created here on the first backup (`<subscription>.key`, mode 0600). Create the directory beforehand with mode 0700; `state_dir` is created automatically. Losing a key loses that subscription's backups, so keep a copy somewhere safe. The panel configuration's key is `_server.key`; with `encrypt_with_repo_key` it is also the password of the Plesk backups inside that repository. `state_dir` holds `staging/` (mode 0700), where the panel configuration is written for the moment it takes to back it up.
- Repository owners: on a subscription's first backup, its inventory `id` is written next to its key (`<subscription>.id`, mode 0600). Every later `backup`, `snapshots`, `restore`, `forget` and `check` compares the inventory's id with it and refuses on a mismatch, naming both ids. This stops a subscription that reuses a name (a deleted subscription's name taken by a new customer, or a rename onto an old name) from writing into, or restoring from, the previous owner's repository. To give the name a new repository, move the old `.key` and `.id` files and the repository itself aside; Relical never deletes them. The `_server` repository records the owner id `_server`. A subscription without an `id` (possible only in a static inventory) records no owner, so its repository has no protection against a reused name.

  A repository can have a key but no `.id` file: it was first backed up from a static inventory without ids, or by a build from before owner files. With a static inventory, the first subscription with an id to use it is recorded as its owner, as before. With `inventory_command`, Relical refuses such a repository (backup, snapshots, restore, forget and check), because it may be an earlier subscription's of the same name; when it is this subscription's, run `relical adopt --subscription S` once. `adopt` confirms that the repository opens with the key, then records the inventory's id; it never creates a key or a repository and never replaces a recorded owner.

### Hetzner Storage Box

A Storage Box is reached over SFTP on port 23, with one sub-account per server so that no server can read or delete another's backups. Install the server's SSH key in the sub-account and pin the Storage Box's host key in a `known_hosts` file, then:

    "destinations": {
      "storagebox": {
        "repository": "sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/{subscription}",
        "env": {},
        "sftp_args": "-p 23 -i /etc/relical/ssh/id_ed25519 -o UserKnownHostsFile=/etc/relical/ssh/known_hosts -o StrictHostKeyChecking=yes -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=4 -o BatchMode=yes",
        "prune": "local"
      }
    }

`relical/{subscription}` is relative to the sub-account's home directory. `packaging/config.example.json` carries this destination as `storagebox`, beside an append-only rest-server one (`archive`); set `default_destination` to the one in use. `StrictHostKeyChecking=yes` refuses a host key that is not in `known_hosts`, rather than trusting it on first use. `ConnectTimeout`, `ServerAliveInterval` and `ServerAliveCountMax` stop a Storage Box that accepts the connection and never answers, or a session that stalls, from hanging the run; `BatchMode=yes` stops ssh from ever waiting for a password. Relical adds these four options (`-o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=4 -o BatchMode=yes`) to every `sftp:` destination whose `sftp_args` do not set them, so they apply even when left out.

In `--all` runs (`backup`, `forget` and `check`), a destination that three subscriptions in a row could not reach (restic or ssh reports a refused, reset or timed-out connection, or the SFTP session could not start) is given up on: the remaining subscriptions on it fail at once with "destination NAME unreachable; not tried", and restic is not run for them. Any subscription that reaches the destination resets the count. The nightly unit also stops a run that is still going after 20 hours (`RuntimeMaxSec=20h`).

A Storage Box cannot run rest-server, so there is no append-only mode: the server's key can delete snapshots, and `prune` is `local`. Protection against deletion by a compromised server comes from the Storage Box itself: its own snapshots (enable automatic snapshots in the Storage Box settings, kept for longer than an attacker is likely to go unnoticed) and the separation of per-server sub-accounts. Where a destination that refuses deletion is needed, use rest-server with `--append-only` and `prune: remote` instead.

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
- `sys_user`, `uid`, `gid`, `home`: the system user (lower case, digits, `_`, `-` and `.`, not starting with `-` or `.`, at most 32 characters) and webspace root, which file and database restores write into. They may be left empty (uid and gid 0) only when `features` contains neither `files` nor `db`. `home` must be `<files root>/<name>`; no two subscriptions' homes may be equal or nested. `uid` and `gid` must be those of `sys_user` on the system: they are not checked against it here; file and database restores check them before writing.
- `files`: the backed-up directories (usually `[home]`), each equal to `home` or under it; `file_excludes`: patterns relative to each of them (default `logs/**`, `statistics/logs/**`, `tmp/**`, `.cagefs/**`, `restored-*`). `restored-*` keeps restored copies, which are earlier backups, out of the next backups; a list given by the inventory replaces the default, so an adapter that sends its own should include it. The restore staging directory `<files root>/.relical/` is outside every home, so no files backup ever includes it.
- `databases`: `{"type": "mysql", "name": "..."}` entries. A name is 1 to 64 of `A-Z a-z 0-9 _ $ -` and starts with a letter, a digit or `_`, so that it can never be read as a `mysqldump` option. A database (type and name) may be listed by one subscription only, and MySQL's and the panel's own databases are refused: `mysql`, `information_schema`, `performance_schema`, `sys`, `psa`, `apsc`, `horde`, `roundcubemail`, `atmail`, `sitebuilder5` and any name starting with `phpmyadmin` (compared without regard to case). Dumps run with the administrator's credentials, so either mistake would put another customer's, or every customer's, data into one subscription's repository.
- `destination` and `retention`: overrides of the defaults, or empty and `null`.

A subscription that breaks any of these rules is left out of the inventory, with the reasons, and the others still load: it is listed under `skipped` with `"invalid": true` and a `reason` that starts with "invalid inventory entry", is not backed up, restored or listed, fails each `--all` run (exit `1`, without a state file entry), and has a failed row (unit `inventory`) in `report` until the entry is fixed. A conflict between two subscriptions (the same name, id, mail domain or database, a mail domain that is another subscription's name, equal or nested homes, or the same `renamed_from`) leaves both out. So one wrong entry, such as a database whose name is reserved, never stops the backups and restores of every other subscription. Only a `server` that fails validation, or output that is not the inventory's JSON shape, stops the command.

`server` describes the panel's own configuration backup: `features` (`["panel-config"]`), `config_command` (first element an absolute path, `{output}` exactly once, where the command must write the file), `config_ext` (the file's extension, 1-8 lower-case letters or digits) and `encrypt_with_repo_key` (default `true`; see the `panel-config` unit under Commands). `skipped`, from an adapter, lists subscriptions it left out (names, or objects with `name` and `reason`); it is informational, and `report` and the run log list it.

## Scheduling

`packaging/systemd` holds a nightly backup and a weekly check as systemd units and timers, which run `/usr/local/lib/relical/relical` with the default configuration `/etc/relical/config.json`. Both timers use the server's local time: the backup starts between 01:00 and 01:30 daily (`RandomizedDelaySec=30min`), the check on Sunday at 03:00, which reads back 5% of each repository's data. A run missed while the server was off starts at the next boot (`Persistent=true`). Override the times with `systemctl edit relical-backup.timer` or `relical-check.timer`. `relical backup --all` applies retention itself once every subscription has been backed up (its `forget` results are part of its output), also when some backups failed, so the unit runs nothing else. Stopping the backup (`systemctl stop relical-backup`, a reboot, or `RuntimeMaxSec=20h` running out) sends SIGTERM: Relical stops restic (which gets 60 seconds to remove its lock), records the results it has, skips retention and exits; `TimeoutStopSec=15min` bounds that. Retention then runs again at the end of the next complete run. Both services run with `Nice=19` and `IOSchedulingClass=idle`. The weekly check fails on Relical's own lock if it starts while a backup is still running, and is then not retried until the next week.

Licensed GPLv3.

## Support this project

Relical is free software from Noiz. If it is useful to you, the main way to support its development is a donation in Monero (XMR):

```text
44BtMn9izxH8mK2yFbSdY6Di7TNobkLbnHdZ6gZQjukCME5vsNhtPRtH4TcVkDHKHLhSpAJbsjv8gCdYuSZVMpXgMkUC1hV
```

You can also help by reporting bugs, sending fixes or starring the repository.

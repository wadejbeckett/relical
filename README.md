# Relical

Mail backup and archiving for hosting servers, built on restic. A panel-neutral core (`core/`) with thin control-panel adapters (`panels/`, starting with Plesk). Part of the Noiz Déjà Vu backup range.

Status: phase 2 in development (mail backup and restore per subscription; files, databases and the panel configuration are being added). Not ready for production.

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
| `backup --subscription S` or `--all` [`--unit mail`] | Backs up the mailboxes of every mail domain of a subscription (or of every subscription in the inventory) into that subscription's restic repository. Creates the repository and its key on first use. |
| `forget --subscription S` or `--all` | Applies the retention policy and prunes. |
| `check --subscription S` or `--all` [`--read-data-subset 5%`] | Checks repository integrity, optionally reading a share of the stored data. |
| `snapshots --subscription S` [`--unit U`] | Lists the subscription's snapshots, or only those of one unit. `--subscription _server` names the panel configuration's repository. |
| `restore` [`--subscription S`] [`--unit mail`] `--domain D --mailbox M` [`--snapshot ID`] [`--folder F`] | Restores a mailbox, or one IMAP folder, into a new folder (see Restores). Without `--subscription`, the subscription is the one whose mail domains include `D`; with it, `D` must be one of that subscription's mail domains. `--snapshot` takes `latest` (the default) or an id of 8 to 64 hex characters. |
| `status` | Prints the last recorded `backup`, `forget` and `check` result per subscription. |

Units are `mail`, `files`, `db` and `panel-config`. This version backs up and restores `mail` only; `--unit files`, `db` or `panel-config` on `backup` or `restore` is reported as not supported.

`backup`, `forget` and `check` need exactly one of `--subscription` or `--all`, and take an exclusive lock so two runs never overlap; a second run fails at once with "another relical run holds the lock". `--all` runs the subscriptions one at a time. Mail snapshots are tagged `mail`; there is one per subscription per run, covering the mailboxes of all its mail domains. A subscription whose features do not include `mail` is reported as OK with a note, and nothing is run for it. Each run is recorded in `state.json` in the state directory, which `status` reads. `backup`, `forget` and `check` wait up to 30 minutes for a repository lock held by another restic process, and `forget` and `check` first remove stale locks left by a restic that was killed.

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

`relical status`:

    {
      "example.test": {
        "backup": {
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
        },
        "check": {
          "subscription": "example.test",
          "action": "check",
          "time": "2026-10-15T05:00:12.903471882Z",
          "ok": true
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
        "config_ext": "tar"
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
- `destinations`: named restic repositories. `repository` must contain `{subscription}` once, so each subscription gets its own repository (the panel configuration uses the name `_server`); `env` holds the variables restic needs to reach it. For an `sftp:` repository, `sftp_args` (one line) is passed to restic as `-o sftp.args=<value>`, for example `-p 23 -i /etc/relical/ssh/id_ed25519`. `default_destination` applies to every subscription that does not name its own `destination`.
- `prune` (required in each destination, `"local"` or `"remote"`): `local` runs `forget --prune` from this server. `remote` is for append-only destinations (for example rest-server with `--append-only`): the server cannot delete there, so `relical forget` does nothing but report that pruning runs on the destination host.
- `retention`: snapshots to keep (daily, weekly, monthly; default 14, 8, 12). A subscription's own `retention` overrides it.
- `limit_upload_kib` and `low_priority`: limit upload bandwidth and run restic under `nice` and `ionice`.
- `mysql.defaults_file`: the MySQL client options file (mode 0600) used for database dumps.
- `keys_dir`: a random restic password per subscription is created here on the first backup (`<subscription>.key`, mode 0600). Create the directory beforehand with mode 0700; `state_dir` is created automatically. Losing a key loses that subscription's backups, so keep a copy somewhere safe.

### The inventory

The inventory says which subscriptions are covered. It is given in one of two ways, never both:

- statically, in the configuration, as `subscriptions` (and optionally `server`), as in `packaging/config.example.json`;
- or by a panel adapter, as `inventory_command`: a command (first element an absolute path) that Relical runs as root before every command that needs the inventory, and whose standard output is the inventory JSON:

      "inventory_command": ["/usr/sbin/plesk", "bin", "extension", "--exec", "relical", "inventory.php"]

  The output must be `{"version": 2, "subscriptions": [...]}`, optionally with `server` (unless the configuration has one) and `skipped`. The command gets 60 seconds and at most 16 MiB of output; a non-zero exit fails the run with its exit code and standard error.

Each subscription has:

- `name`: the subscription's main domain (lower case), which names its repository and key; `_server` is reserved. `id` is the panel's stable id, if it has one.
- `renamed_from`: the subscription's previous name, when the panel renamed it. Backups then go to a new repository under the new name; the old repository is left as it is, and `relical inventory` lists the rename under `renamed`.
- `features`: which units are backed up: `mail`, `files`, `db`.
- `mail_domains`: the domains whose mailboxes belong to the subscription. A mail domain belongs to one subscription only.
- `sys_user`, `uid`, `gid`, `home`: the system user and webspace root, which file and database restores write into. They may be left empty (uid and gid 0) only when `features` contains neither `files` nor `db`.
- `files`: the backed-up directories (usually `[home]`), each a clean absolute path under a files root; `file_excludes`: patterns relative to each of them (default `logs/**`, `statistics/logs/**`, `tmp/**`, `.cagefs/**`).
- `databases`: `{"type": "mysql", "name": "..."}` entries.
- `destination` and `retention`: overrides of the defaults, or empty and `null`.

`server` describes the panel's own configuration backup: `features` (`["panel-config"]`), `config_command` (first element an absolute path, `{output}` exactly once) and `config_ext`. `skipped`, from an adapter, lists subscriptions it left out (names, or objects with `name` and `reason`); it is informational.

## Scheduling

`packaging/systemd` holds a nightly backup and a weekly check as systemd units and timers. Both timers use the server's local time (the backup starts between 01:00 and 01:30 daily, because of `RandomizedDelaySec=30min`; the check starts Sunday 05:00); override them with `systemctl edit relical-backup.timer`. The backup unit runs `relical forget --all` through `ExecStopPost=`, so retention runs after every backup run, even one where a backup failed. The weekly check fails on Relical's own lock if it starts while a backup is still running, and is then not retried until the next week.

Licensed GPLv3.

## Support this project

Relical is free software from Noiz. If it is useful to you, the main way to support its development is a donation in Monero (XMR):

```text
44BtMn9izxH8mK2yFbSdY6Di7TNobkLbnHdZ6gZQjukCME5vsNhtPRtH4TcVkDHKHLhSpAJbsjv8gCdYuSZVMpXgMkUC1hV
```

You can also help by reporting bugs, sending fixes or starring the repository.

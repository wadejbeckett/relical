# Relical

Mail backup and archiving for hosting servers, built on restic. A panel-neutral core (`core/`) with thin control-panel adapters (`panels/`, starting with Plesk). Part of the Noiz Déjà Vu backup range.

Status: phase 1 prototype (mailbox backup and restore). Not ready for production.

## Build and test

    make tools        # pinned restic 0.19.1 and rest-server 0.14.0 into .tools/
    make test         # unit tests
    make integration  # against real restic and an append-only rest-server
    make e2e          # backup and restore through a real Dovecot in Docker
    make build        # dist/relical, static

`make e2e` builds a Debian 12 container with Dovecot and runs the whole backup and restore cycle against it. Set `RELICAL_DOCKER_ARGS` to pass extra arguments to `docker run`, for example `RELICAL_DOCKER_ARGS="--network host" make e2e` on a host whose containers have no network access.

## Commands

    relical [-config PATH] COMMAND [flags]

The configuration file defaults to `/etc/relical/config.json`. Every command prints JSON on stdout, so a panel adapter can call `relical` and parse the result; restic is never run by an adapter. Errors that stop a command before it has results are printed as `{"error": "..."}`.

| Command | What it does |
|---|---|
| `version` | Prints the version. |
| `mailboxes --domain D` | Lists the domain's mailboxes found on disk, with login, path, and owner uid and gid. |
| `backup --domain D` or `--all` | Backs up every mailbox of a domain (or of every domain in the configuration) into that domain's restic repository. Creates the repository and its key on first use. |
| `forget --domain D` or `--all` | Applies the retention policy and prunes. |
| `check --domain D` or `--all` [`--read-data-subset 5%`] | Checks repository integrity, optionally reading a share of the stored data. |
| `snapshots --domain D` | Lists the domain's snapshots. |
| `restore --domain D --mailbox M` [`--snapshot ID`] [`--folder F`] | Restores a mailbox, or one IMAP folder, into a new folder (see Restores). `--snapshot` takes `latest` (the default) or an id of 8 to 64 hex characters. |
| `status` | Prints the last recorded `backup`, `forget` and `check` result per domain. |

`backup`, `forget` and `check` need exactly one of `--domain` or `--all`, and take an exclusive lock so two runs never overlap; a second run fails at once with "another relical run holds the lock". Backups of a mailbox are tagged `backup`. Each run is recorded in `state.json` in the state directory, which `status` reads. `backup`, `forget` and `check` wait up to 30 minutes for a repository lock held by another restic process, and `forget` and `check` first remove stale locks left by a restic that was killed.

Exit codes: `0` success; `1` an error, or at least one domain's job failed (the JSON still lists every domain's result); `2` a usage error, such as an unknown command or a missing `--domain`/`--all`.

### Sample output

`relical backup --domain example.test`:

    [
      {
        "domain": "example.test",
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

`relical snapshots --domain example.test`:

    [
      {
        "id": "b9e9540af49ca5d668c11272f8542eba4fc2faf5bc0f8b4a4f3dd0aece0111c0",
        "time": "2026-10-15T01:00:42.181532906Z",
        "paths": [
          "/var/qmail/mailnames/example.test/alice/Maildir",
          "/var/qmail/mailnames/example.test/bob/Maildir"
        ],
        "hostname": "mail.example.test",
        "tags": [
          "backup"
        ]
      }
    ]

`relical mailboxes --domain example.test` (one entry shown):

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

`relical forget --domain example.test` and `relical check --domain example.test`:

    [
      {
        "domain": "example.test",
        "action": "check",
        "time": "2026-10-15T05:00:12.903471882Z",
        "ok": true
      }
    ]

`relical status`:

    {
      "example.test": {
        "backup": {
          "domain": "example.test",
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
          "domain": "example.test",
          "action": "check",
          "time": "2026-10-15T05:00:12.903471882Z",
          "ok": true
        }
      }
    }

`relical restore --domain example.test --mailbox alice`:

    {
      "domain": "example.test",
      "mailbox": "alice",
      "snapshot": "b9e9540af49ca5d668c11272f8542eba4fc2faf5bc0f8b4a4f3dd0aece0111c0",
      "restored_to": "Restored 2026-10-15 b9e9540a",
      "messages": 3
    }

## Restores

A restore never overwrites mail. It restores the snapshot into a staging directory and imports it with `doveadm import` into a new IMAP folder named `Restored <snapshot date> <short id>` (with `--folder`, only that folder is imported). The user then copies what they need back. Restoring the same snapshot twice is refused while the first "Restored" folder still exists; delete or rename that folder first. A restore fails, and removes any partial folder it created, if the mailbox is not in the snapshot, if `doveadm` reports that it could not read the restored copy, or if the folder does not exist afterwards. Restores need `doveadm` and run as root.

## Configuration

Copy `packaging/config.example.json` to `/etc/relical/config.json` and set mode `0600`; Relical refuses a configuration readable by others. Unknown keys are rejected. The main settings:

- `domains`: the domains that `--all` covers and that `backup` accepts.
- `mailbox_pattern`: where a domain's mailboxes live, with `{domain}` and `{mailbox}` once each (the example is the Plesk layout).
- `destinations`: named restic repositories. `repository` must contain `{domain}`, so each domain gets its own repository; `env` holds the variables restic needs to reach it. `default_destination` applies to every domain, and `domain_destinations` maps a domain to another destination.
- `prune`: `local` runs `forget --prune` from this server. `remote` is for append-only destinations (for example rest-server with `--append-only`): the server cannot delete there, so `relical forget` does nothing but report that pruning runs on the destination host.
- `retention`: snapshots to keep (daily, weekly, monthly; default 14, 8, 12).
- `limit_upload_kib` and `low_priority`: limit upload bandwidth and run restic under `nice` and `ionice`.
- `keys_dir`: a random restic password per domain is created here on the first backup (`<domain>.key`, mode 0600, directory mode 0700). Losing a key loses that domain's backups, so keep a copy somewhere safe.

## Scheduling

`packaging/systemd` holds a nightly backup and a weekly check as systemd units and timers. Both timers use the server's local time (01:00 daily for the backup, Sunday 05:00 for the check); override them with `systemctl edit relical-backup.timer`. The backup unit runs `relical forget --all` through `ExecStopPost=`, so retention runs after every backup run, even one where a backup failed. The weekly check fails on Relical's own lock if it starts while a backup is still running, and is then not retried until the next week.

Licensed GPLv3.

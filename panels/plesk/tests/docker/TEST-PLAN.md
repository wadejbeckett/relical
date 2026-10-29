# Docker Plesk test plan

What the test in the `plesk/plesk` image (plan Task 10) must confirm, beyond
what PHPUnit covers. Each line names the assumption and where it lives.

## Run of 29 October 2026

`run.sh` passed end to end against `plesk/plesk:latest` (Plesk Obsidian
18.0.81.4 on Ubuntu 24.04, Dovecot 2.4.5, MariaDB 10.11, the image's trial
licence: one domain, one customer) with `atmoz/sftp:alpine` on port 2222 as
the Storage Box. Repeat it with:

    make plesk-ext && panels/plesk/tests/docker/run.sh    # KEEP=1 keeps the containers

What it needed on this workstation:

- systemd in the container: on a cgroup v2 host the image's documented
  command (`-v /sys/fs/cgroup:/sys/fs/cgroup:ro`) exits at once (status
  255); `--cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw` boots it, and
  systemctl works, so no "no systemd" mode was needed.
- `openssh-client` installed in the container with apt: the image has no
  `ssh`, `sftp`, `ssh-keyscan` or `ssh-keygen`.
- The Plesk UI pages (`/cp/...`, `/smb/...`) redirect to the EULA until it
  is accepted; this test does not accept it, so the "Backups" button and the
  Permissions tab were not seen. The extension's own pages are not gated and
  were tested with sessions from `plesk login <user>`.

Defects found and fixed (each with a commit and, where a unit test can pin
it, a test): root scripts did not run as root (`extension --exec` and the
install hooks run as psaadm: now `sbin/relical-root`); saving the settings
never reloaded systemd; redirects repeated the base URL; mail restores
failed on Dovecot 2.4 and on Plesk's `INBOX.` namespace (core); a
subdomain without mail was reported as skipped; whole-webspace restores
failed on a chroot webspace's device nodes (core, Part A); `restored-*`
copies were backed up by the extension's excludes; the services did not
run at idle priority.

Legend: [x] checked here (the result says what was and was not covered),
[~] not or only partly testable here, with the reason.

## Plesk SDK bootstrap

- [x] `pm_Context` in lifecycle scripts: `post-install.php` and
      `pre-uninstall.php` run on install, upgrade and removal with
      `pm_Context::init('relical')` already called by the script, and
      `pm_Settings::get()` works in post-install (it reads the backup time).
      Also confirm that calling `init()` when Plesk has already set the
      context is harmless. (Header of every `plib/scripts/*.php`.)
      **Result (2026-10-29):** Confirmed, with a change. Plesk sets the module context for every script (`pm_Context::getModuleId()` is `relical` under `--exec` without `init()`), and calling `init('relical')` again is harmless. `pm_Settings::get()` works in post-install. But Plesk runs post-install and pre-uninstall as the panel user `psaadm`, not root, so the first install failed with "post-install must run as root". Fixed: the package ships `sbin/relical-root` and the hooks hand themselves to it (see the next item).
- [x] The same for `plesk bin extension --exec relical <script>` run as root
      from the web UI: `write-config.php`, `install-timers.php`, `cli.php`,
      `inventory.php`, `hostkey.php`, `probe.php`, `overview.php`,
      `log-tail.php`.
      **Result (2026-10-29):** Refuted, fixed. `plesk bin extension --exec` runs every script as `psaadm` (`posix_geteuid()` 999), even when root starts it, through `sh -c '/opt/psa/admin/bin/php' -dauto_prepend_file=sdk.php <script> 2>&1` (stderr merged into stdout). Root work now goes through `sbin/relical-root`, which Plesk installs under `/opt/psa/admin/sbin/modules/relical/` with a `mod_wrapper` link in `/opt/psa/admin/bin/modules/relical/` (the path `pm_ApiCli::callSbin` uses; checked: it runs as uid 0 from the web and from the CLI, with arguments, environment and stdin). relical-root starts `/opt/psa/admin/bin/php -dauto_prepend_file=sdk.php <script>` as root, where the SDK works (`pm_Settings`, `pm_Domain`, `pm_ApiRpc`, `getVarDir()`). Every listed script ran as root this way, and also through `plesk bin extension --exec` (the script hands itself to relical-root: `ScriptIO::requireRoot`; write-config.php and inventory.php now do too). The core's `inventory_command` is now `relical-root inventory.php`. Side note: `pm_Domain::getPlanItems()` in a CLI script logs "Call to a member function getParam() on null" twice in panel.log (Plesk's own noise) but returns the right items.

## Install, upgrade and removal

- [x] A fresh install and an upgrade print every step; both binaries are
      staged as `.relical.new` and `.restic.new` and renamed into place.
      **Result (2026-10-29):** Confirmed for the steps and staging: run as root through relical-root, post-install prints every step, `stage .relical.new`/`.restic.new`, then the renames; the installed binaries match `SHA256SUMS` (run.sh). `plesk bin extension -i` itself prints only "The extension was successfully installed"; the step list shows in panel.log only on failure. A re-run as `post-install.php upgrade 0.2.0` finishes ("Relical: done").
- [x] Invalid saved settings stop post-install before anything changes.
      **Result (2026-10-29):** Confirmed: `sftp_port` set to 0 makes post-install stop with "Storage Box port must be between 1 and 65535" before any step; config.json is unchanged (run.sh step 8). The message does not use the "Nothing was changed" wording, because the settings are checked before the plan runs.
- [x] A failed install names the completed steps, the steps not done and
      `plesk bin extension --exec relical post-install.php`, and running
      that command finishes the install.
      **Result (2026-10-29):** Confirmed on the first install (missing `ssh-keygen`): "installation failed: none of /usr/bin/ssh-keygen, /bin/ssh-keygen exists" after the completed steps; running post-install again finished the install. The `plesk/plesk` image ships without the OpenSSH client; the README now lists it as a requirement.
- [x] Removal with an extra file in `/usr/local/lib/relical` keeps the
      directory, names the file and still exits 0 (Plesk removes the
      extension).
      **Result (2026-10-29):** Confirmed: with `local-note.txt` in `/usr/local/lib/relical`, `plesk bin extension -u relical` exits 0, removes the binaries and units, keeps the directory with the file, and keeps `/etc/relical` and `/var/lib/relical`. Also seen: when pre-uninstall fails (the old build, as psaadm), Plesk removes the extension anyway and leaves the timers and binaries behind.

## Administrator screens

- [x] The web process (Plesk's panel user) runs root scripts through the
      utility `Paths::extensionCommand()` resolves (PRODUCT_ROOT_D's
      `bin/extension`, as Cloudflare DNS Sync's UI does on 18.0.81, else
      `/usr/sbin/plesk bin extension`) with `proc_open` in argv form, and
      the script runs as root. Note which form was used.
      **Result (2026-10-29):** Refuted, fixed (see Plesk SDK bootstrap): `<PRODUCT_ROOT_D>/bin/extension --exec` runs as the panel user. The screens now run `/opt/psa/admin/bin/modules/relical/relical-root <script> [args]` with `proc_open` in argv form (Paths::rootCommand, PRODUCT_ROOT_D `/opt/psa`); the scripts run as root (the settings page shows the root-only public key, overview, host key, probe and restores work).
- [x] `pm_Form_Simple` forms: the settings form shows the errors added with
      `addError()` after `isValid()`; a valid POST returns
      `{"redirect": ...}` and the status messages appear after it; a POST
      without Plesk's forgery token is refused; `hidden` and `radio`
      elements render and submit.
      **Result (2026-10-29):** Confirmed over HTTP with an admin session: an invalid port comes back as `{"formMessages":{"sftp_port":[...]}}`; a valid POST returns `{"redirect":"/modules/relical/"}` and the next page shows the status messages; a POST without `forgery_protection_token` is refused ("Session token has expired", HTTP 500); `hidden` and the `scope` radio render and submit. A save also exposed a defect: install-timers.php passed a `php://memory` stream to `proc_open`, so systemctl never ran and the old backup time stayed in force. Fixed (Process::run pipes such streams) and pinned.
- [x] An invalid GET `backup` or `adopt` redirects back with its message
      (`_redirect`); GET `probe` only shows the "Test connection" form.
      **Result (2026-10-29):** Refuted, fixed: `_redirect()` prepended the base URL, so the refusal went to `/modules/relical/index.php/modules/relical/`. Now `prependBase => false` (pinned by ControllerRedirectTest); an invalid GET `backup` redirects to `/modules/relical/` with its message. GET `probe` was not opened separately.
- [~] "Adopt repository" appears on a status row whose last error names
      `relical adopt`, and the POST records the owner so the next backup
      of that subscription runs.
      **Result (2026-10-29):** Not tested here: making the core refuse with "relical adopt" needs a repository without an owner file; the core's own tests cover adopt.
- [x] "Back up now" and "Run everything now" start a run that outlives the
      request (`... < /dev/null > /dev/null 2>&1 &`) and show up in the
      status table; a second click while it runs gets the "already in
      progress" message (`running.php` reads the core's flock from
      `/proc/locks`).
      **Result (2026-10-29):** Confirmed for "Run everything now": the POST starts `cli.php backup --all` as root detached from the request (seen in the process list after the response), and a second POST while it runs shows "A backup run is already in progress". "Back up now" for one subscription uses the same path and was not clicked separately.
- [x] GET `connection` runs nothing (no `ssh-keyscan` in the process
      list); its "Check connection" POST scans and comes back with the
      fingerprint in the URL; a fingerprint typed into the URL is shown but
      pins nothing unless the host really offers that key.
      **Result (2026-10-29):** Confirmed: GET `connection` shows only the form (no ssh-keyscan seen in the process list); its POST answers `{"redirect": ".../connection?fingerprint=SHA256:...&pinned=1"}`. The typed-fingerprint case was not tried over HTTP; HostKeyTest pins it.
- [x] "Check connection" against the SFTP container on a port other than
      22: `ssh-keyscan -p <port> -t ed25519 -T 10 <host>` prints
      `[host]:port ssh-ed25519 ...`, the fingerprint matches
      `ssh-keygen -l`, "Trust this host key" writes
      `/etc/relical/ssh/known_hosts` 0600, and a changed host key between
      scan and trust is refused.
      **Result (2026-10-29):** Confirmed against atmoz/sftp on port 2222 (`hostkey.php scan` and `trust`): the fingerprint equals `ssh-keygen -l` of the server's key, the pin is `[sftp.example.test]:2222 ssh-ed25519 ...` in `known_hosts` 0600 root, and a different fingerprint is refused with nothing written.
- [x] "Test connection" (a POST; `sftp -b -` running `pwd`, then
      `-ls "<path>"`) succeeds against the SFTP container once the public
      key is authorised, and fails with sftp's reason when it is not. A
      missing path still counts as connected and adds "does not exist yet"
      (sftp prints `Can't ls: "<path>" not found`). On a real Storage Box,
      confirm `pwd` and `ls` are accepted in sftp batch mode. (Task 8
      review finding 6: the plan's restic probe of `relical/_probe` became
      this sftp probe, which writes nothing.)
      **Result (2026-10-29):** Confirmed: before the key is authorised the probe reports sftp's reason ("Permission denied (publickey,password,keyboard-interactive)"); after, "Connected to u000000-sub1@sftp.example.test:relical (port 2222)". A missing path still counts as connected: "The path relical/missing does not exist yet; the first backup creates it." A real Storage Box's batch-mode `pwd`/`ls` was not tested here (no Storage Box).
- [x] Saving a new backup time rewrites `relical-backup.timer` and
      `systemctl list-timers` shows the new time.
      **Result (2026-10-29):** Confirmed after the memory-stream fix: saving 02:15 rewrites both timers and `systemctl show relical-backup.timer` gives `OnCalendar=*-*-* 02:15:00`; the check timer moves to Sunday 14:15.
- [x] The status table before any configuration shows the core's error, and
      the report block says the report is not available until the core has
      the `report` command (core Task 5).
      **Result (2026-10-29):** Confirmed: "No status yet: open /etc/relical/config.json: no such file or directory"; the report block says the report could not be made, with the same reason (the core has `report` now).
- [x] The log screen before the first run says there is no log yet.
      **Result (2026-10-29):** Confirmed: "There is no log yet: /var/lib/relical/relical.log is written by the first backup run."
- [~] Timing on a server with a few hundred subscriptions: how long
      `relical status` and `relical report` take inside `overview.php`
      (report runs `inventory_command`). The limits are 6 s and 12 s, the
      page waits 21 s; if report needs longer, the core should use the
      inventory of the last run instead (Task 9 review finding 6).
      **Result (2026-10-29):** Not testable here: the image's licence allows one domain and one customer (`lim_dom 1`, `lim_cl 1`). With one subscription `overview.php` answers well within its limits.

## Customer restore screens

- [~] Button visibility by coverage: with scope "plan items", the "Backups"
      button shows on a subscription whose plan (or add-on plan) carries
      `relical-mail` or `relical-web`, and not on one without; with scope
      "every subscription" it shows everywhere. Also on a subdomain's card
      (`site_id`), never on an alias. Confirm the parameter names Plesk
      passes with `contextParams` (`dom_id`, `site_id`, `alias_id`) and that
      `pm_Domain::getProperty('webspace_id')` gives the subscription of a
      subdomain (0 or its own id for a main domain).
      **Result (2026-10-29):** Partly. The plan items are `urn:ext:relical:plan-item-sdk:relical-mail` and `...:relical-web` in Plesk (registered on the first plan operation that names them, for example `plesk bin service_plan --add-custom-plan-item`), and `pm_Domain::getPlanItems()` returns the short keys `relical-mail` and `relical-web`, so keys with `-` work. Coverage by plan item works end to end (mail only gives a mail-only inventory and refuses the database screen; no item gives the not-included page). `contextParams` names: `dom_id` and `site_id` open the restore screens, `alias_id` is refused. `webspace_id` is 0 for the main domain and the subscription's id for a subdomain. The button itself could not be seen: every Plesk UI page redirects to the EULA, which this test does not accept; the extension's own pages are not gated.
- [x] A customer who opens `/modules/relical/index.php/restore/index?dom_id=<id>`
      for another customer's subscription (or `site_id` of another's site)
      gets "Permission denied" and no root script runs (check the process
      list or `plesk bin extension --exec` logging). An uncovered
      subscription of the customer's own shows the "not included in the
      plan" page. `_server` is never offered.
      **Result (2026-10-29):** Confirmed with customer cust1: while the subscription was the administrator's, the restore index (`dom_id`) and the subdomain (`site_id`) answered with a redirect whose page says "Permission denied", and no extension script ran. After `--change-owner` the customer's own page opens. The uncovered page says the plan does not include Relical. `_server` is never listed and `restore-run.php` refuses it.
- [x] Permission `relical_restore` (`plib/hooks/Permissions.php`): it
      shows on the Permissions tab of service plans and subscriptions, on
      by default; switched off, the Backups button disappears and the
      restore URL answers "Permission denied". Confirm that
      `pm_Client::hasPermission('relical_restore', $domain)` is the right
      name for an extension's permission (not a prefixed form).
      **Result (2026-10-29):** Confirmed: the permission is stored as `ext_permission_relical_relical_restore` (`plesk bin subscription_settings --update <sub> -ext_permission_relical_relical_restore false`); `pm_Client::hasPermission('relical_restore', $domain)` is the right name (true by default, false after switching it off); switched off, the customer's restore URL answers "Permission denied" while the administrator's still opens. The Permissions tab itself was not seen (EULA, as above).
- [x] Additional users: what `pm_Session::getClient()` returns for an
      additional user with a role (the user or the customer account);
      that a user limited to subscription A cannot open subscription B's
      restore page; and whether a role can withhold `relical_restore`.
      Record the behaviour here (Task 9 review finding 7).
      **Result (2026-10-29):** Recorded: for an additional user (role with mail management only, limited to example.test), `pm_Session::getClient()` returns the owning customer account (same id and login) with `isClient()` false, and `hasAccessToDomain()` and `hasPermission('relical_restore')` both false, so the restore screens refuse additional users. `plesk bin role` has no option for extension permissions; whether a role can grant it in the UI was not checked (EULA).
- [x] A customer cannot open the administrator screens
      (`IndexController::init` refuses non-admins).
      **Result (2026-10-29):** Confirmed: the customer's `index/index` redirects with "Permission denied", before and after owning the subscription.
- [x] Restore points: the index lists mail, files and database restore
      points with local dates; a subscription with no backup yet says so in
      plain words (no Storage Box host or repository in the message).
      **Result (2026-10-29):** Confirmed for the listing with dates ("1 restore point, the latest from 2026-10-29 06:42", files and database rows with date and reference). The no-backup-yet wording was not seen (the subscription was backed up before the screens were opened).
- [x] Mail end to end: the mailbox dropdown lists exactly the
      subscription's mailboxes (`cli.php mailboxes`); a restore of a whole
      mailbox and one of `INBOX` only each create a new folder
      `Restored <date> <id>` with the messages, and the status page says so;
      a second restore of the same point reports that the folder exists.
      **Result (2026-10-29):** Confirmed through restore-run.php as the screens start it, after a core fix: Dovecot 2.4.5 refused the `maildir:<copy>:INDEX=MEMORY` source, and Plesk's namespace (prefix `INBOX.`) refused a top-level `Restored ...` folder. Now a whole mailbox goes into `INBOX.Restored <date> <id>` (shown as "Restored <date> <id>" on the status page), INBOX only into `INBOX.Restored <date> <id>.INBOX`, and the same point again reports that the folder exists. The customer's mailbox dropdown lists exactly `alice@example.test`, and the restore point dropdown the three mail points.
- [x] Files end to end: browsing descends one level per click with a
      breadcrumb; "This folder" restores only that path, "Everything" the
      whole home directory, into `restored-<date>-<id>/` (0700, owned by the
      subscription's system user, not served by the website); File Manager
      shows it; a second restore of the same point is refused with the
      "already exists" message.
      **Result (2026-10-29):** Confirmed for the copies: `httpdocs` and the whole home directory restore into `restored-<date>-<id>/` (0700, the subscription's user), the same point again is refused with "already exists", and on a chroot webspace `dev/null`, `dev/random` and `dev/urandom` are left out and listed as `dropped` (the core used to refuse them). The files page lists the home directory without `logs/`, `tmp/` or `restored-*`. Breadcrumb clicks and File Manager were not checked.
- [x] Database end to end: the dropdown lists one entry per database
      restore point (from the `db:<name>` tags, including a database dropped
      since), and the restore writes `restored-<date>-<id>/<db>.sql` (0600)
      that phpMyAdmin imports into a new database.
      **Result (2026-10-29):** Confirmed for the file: `restored-<date>-<id>/example_wp.sql` (0600, the subscription's user) loads into a scratch database with the mysql client. The customer's dropdown lists one `example_wp, <date> (<id>)` entry per database restore point. phpMyAdmin was not used.
- [x] Every restore form is a `pm_Form_Simple` POST; it returns
      `{"redirect": <status URL>}`, the restore outlives the request
      (`restore-run.php` detached), and a forged POST with a mailbox,
      restore point or database not in the lists is refused without
      starting anything.
      **Result (2026-10-29):** Confirmed for the files form: the POST returns `{"redirect": "/modules/relical/index.php/restore/status?..."}` and restore-run.php runs as root after the request ended. A customer POST with a mailbox or restore point not in the lists comes back as a field error (`notInArray`) and starts nothing (no record written).
- [x] Status page: the `<meta http-equiv="refresh">` in the page body
      reloads it in Plesk's layout (Chrome and Firefox) until the record
      says done or failed; "Reload now" works without it. A record of
      another subscription (its id typed into the URL) reads as "not found".
      **Result (2026-10-29):** Confirmed in Chromium (Playwright): the `<meta http-equiv="refresh" content="5;url=...">` sits in the body inside Plesk's layout and the page reloads while the restore runs (SFTP paused to keep it running); a finished record has no refresh. Firefox was not tried.
- [x] Server-wide limit: with two restores of other subscriptions running,
      a third is refused with "The server is busy with other restores"
      (the `slot-1.lock`/`slot-2.lock` flocks in `restores/`); a customer
      with five `restored-*` copies in the home directory cannot start a
      files or database restore, an administrator can. Disk space itself
      is the core's check (free space on the staging and home file
      systems, and staging on the webspace's file system), being done on
      `main`: confirm a restore larger than the free space is refused
      before anything is written (Task 9 review finding 1).
      **Result (2026-10-29):** Confirmed: with `slot-1.lock` and `slot-2.lock` held, restore-run.php answers "The server is busy with other restores. Try again later."; with five `restored-*` folders a customer's files POST is refused ("There are already 5 restored copies ...") while the administrator's starts. The disk-space check is the core's (refused before staging in its tests); not exercised here.
- [~] Storage Box connections: reloading the index page in a loop opens
      at most one SFTP session per subscription at a time (others get
      "being read for another page") and none at all for five minutes
      after a successful read (`<subscription>.snapshots.cache`); a
      restore clears the cache; `cache-clear.php` clears it at once
      (Task 9 review finding 2).
      **Result (2026-10-29):** Partly: after an index load the root-only `example.test.snapshots.cache` and `example.test.read.lock` exist in `restores/`, and a new snapshot appears only after `cache-clear.php` (`{"removed":1}`). Session counting on the SFTP side was not done.
- [x] Rate limit: starting a second restore of the same subscription while
      one runs shows "Another restore of this subscription is still
      running"; two near-simultaneous POSTs leave one running and one
      failed record (the flock in `restore-run.php`).
      **Result (2026-10-29):** Confirmed: a second restore-run.php for the same subscription while one runs records "Another restore of this subscription is still running. Start this one when it has finished."
- [x] `pm_Context::getVarDir()` in `restore-run.php`, `restore-result.php`
      and `restore-running.php` (run through `--exec`) is the extension's
      var dir; `restores/` is created 0700 root and records are 0600.
      **Result (2026-10-29):** Confirmed: `getVarDir()` is `/opt/psa/var/modules/relical/` (owned by psaadm), `restores/` is 0700 root and records are 0600 root.
- [x] A restore killed mid-way (kill `restore-run.php`) shows "stopped
      before it finished" on the status page.
      **Result (2026-10-29):** Confirmed: killing restore-run.php and the core mid-way gives "The restore stopped before it finished."
- [x] `$this->render('restore/_style.phtml')` finds the partial from every
      restore view.
      **Result (2026-10-29):** Confirmed for the index, files, status and not-covered pages (styles rendered, no error in panel.log).

## Pre-pilot fixes (branch `ext-fixes`, 29 October 2026): to re-verify

Changes from the whole-branch review that PHPUnit pins but the Docker run
has not yet seen. Each line is open until the next `run.sh` run.

- [ ] Daily restore limit: a customer's fourth files or database restore of
      one subscription within 24 hours is refused with "This subscription
      has had 3 website or database restores in the last 24 hours", both on
      the form (`restore-running.php` returns `recent_restores`) and in
      `restore-run.php` as root; mail restores and an administrator's
      (`restore-run.php <id> --admin restore ...`) are not counted or held.
- [~] Plesk disk quota: a restore is not checked against the
      subscription's Plesk disk quota (whole-branch review finding 4). Not
      done for the pilot; the daily limit and the five-copy cap bound how
      much a customer can add. Revisit with `pm_Domain` limits.
- [ ] Failure notice: both services carry `OnFailure=relical-notify@%n.service`;
      `systemctl start relical-notify@relical-backup.service` appends
      "relical-notify: relical-backup.service failed" to the run log, writes
      `/var/lib/relical/last-failure.json` (0600 root) and the settings page
      shows "Last failed run" and "Last successful backup" (`run.sh` step 5).
      Mail to the administrator: confirm API-RPC `<server><get><admin/>`
      answers `admin/admin_email` on 18.0.81 (else the `pm_Client`
      fallback), and whether the image's MTA delivers (`"mailed"` in the
      journal); `run.sh` reports it but does not require it.
- [ ] Bounded runs: `TimeoutStartSec=20h` (not `RuntimeMaxSec=`, which
      systemd ignores for oneshot units) and `TimeoutStopSec=15min`;
      `systemctl stop relical-backup` during a run returns within the
      timeout and starts no forget (core finding 5).
- [ ] `relical-root` starts PHP with `env -i PATH=... LANG=C.UTF-8`: every
      root script, the install hooks and the core's `inventory.php` still
      work with the empty environment (does Plesk's SDK need HOME or
      anything else?), and the panel user's `PHP_INI_SCAN_DIR`/`PHPRC` no
      longer reach PHP (`run.sh` step 3, with a positive control).
- [ ] Reinstall: after `plesk bin extension -u relical` and a fresh install
      (settings empty, old `config.json` kept), the settings page says
      backups still go to the old destination (from `overview.php`'s
      `config_destination`) until the settings are saved.
- [ ] "Run everything now" goes through `run-all.php`, which runs
      `systemctl start --no-block relical-backup.service` (the journal shows
      the unit), so the run has the timer's priority, retention, cgroup and
      `OnFailure=`; without systemd it falls back to a detached
      `backup --all` (not testable in this image, which runs systemd).

# Docker Plesk test plan

What the test in the `plesk/plesk` image (plan Task 10) must confirm, beyond
what PHPUnit covers. Each line names the assumption and where it lives.

## Plesk SDK bootstrap

- [ ] `pm_Context` in lifecycle scripts: `post-install.php` and
      `pre-uninstall.php` run on install, upgrade and removal with
      `pm_Context::init('relical')` already called by the script, and
      `pm_Settings::get()` works in post-install (it reads the backup time).
      Also confirm that calling `init()` when Plesk has already set the
      context is harmless. (Header of every `plib/scripts/*.php`.)
- [ ] The same for `plesk bin extension --exec relical <script>` run as root
      from the web UI: `write-config.php`, `install-timers.php`, `cli.php`,
      `inventory.php`, `hostkey.php`, `probe.php`, `show-pubkey.php`,
      `log-tail.php`.

## Install, upgrade and removal

- [ ] A fresh install and an upgrade print every step; both binaries are
      staged as `.relical.new` and `.restic.new` and renamed into place.
- [ ] Invalid saved settings stop post-install before anything changes.
- [ ] A failed install names the completed steps, the steps not done and
      `plesk bin extension --exec relical post-install.php`, and running
      that command finishes the install.
- [ ] Removal with an extra file in `/usr/local/lib/relical` keeps the
      directory, names the file and still exits 0 (Plesk removes the
      extension).

## Administrator screens

- [ ] The web process (Plesk's panel user) runs root scripts through the
      utility `Paths::extensionCommand()` resolves (PRODUCT_ROOT_D's
      `bin/extension`, as Cloudflare DNS Sync's UI does on 18.0.81, else
      `/usr/sbin/plesk bin extension`) with `proc_open` in argv form, and
      the script runs as root. Note which form was used.
- [ ] `pm_Form_Simple` forms: the settings form shows the errors added with
      `addError()` after `isValid()`; a valid POST returns
      `{"redirect": ...}` and the status messages appear after it; a POST
      without Plesk's forgery token is refused; `hidden` and `radio`
      elements render and submit.
- [ ] GET `probe` and an invalid GET `backup` redirect back with their
      message (`_redirect`).
- [ ] "Back up now" and "Run everything now" start a run that outlives the
      request (`... < /dev/null > /dev/null 2>&1 &`) and show up in the
      status table; a second click while it runs gets the "already in
      progress" message (`running.php` reads the core's flock from
      `/proc/locks`).
- [ ] "Check connection" against the SFTP container on a port other than
      22: `ssh-keyscan -p <port> -t ed25519 -T 10 <host>` prints
      `[host]:port ssh-ed25519 ...`, the fingerprint matches
      `ssh-keygen -l`, "Trust this host key" writes
      `/etc/relical/ssh/known_hosts` 0600, and a changed host key between
      scan and trust is refused.
- [ ] "Test connection" (`sftp -b -` running `pwd`) succeeds against the SFTP
      container once the public key is authorised, and fails with sftp's
      reason when it is not. On a real Storage Box, confirm `pwd` is
      accepted in sftp batch mode.
- [ ] Saving a new backup time rewrites `relical-backup.timer` and
      `systemctl list-timers` shows the new time.
- [ ] The status table before any configuration shows the core's error, and
      the report block says the report is not available until the core has
      the `report` command (core Task 5).
- [ ] The log screen before the first run says there is no log yet.

## Customer restore screens

- [ ] Button visibility by coverage: with scope "plan items", the "Backups"
      button shows on a subscription whose plan (or add-on plan) carries
      `relical-mail` or `relical-web`, and not on one without; with scope
      "every subscription" it shows everywhere. Also on a subdomain's card
      (`site_id`), never on an alias. Confirm the parameter names Plesk
      passes with `contextParams` (`dom_id`, `site_id`, `alias_id`) and that
      `pm_Domain::getProperty('webspace_id')` gives the subscription of a
      subdomain (0 or its own id for a main domain).
- [ ] A customer who opens `/modules/relical/index.php/restore/index?dom_id=<id>`
      for another customer's subscription (or `site_id` of another's site)
      gets "Permission denied" and no root script runs (check the process
      list or `plesk bin extension --exec` logging). An uncovered
      subscription of the customer's own shows the "not included in the
      plan" page. `_server` is never offered.
- [ ] A customer cannot open the administrator screens
      (`IndexController::init` refuses non-admins).
- [ ] Restore points: the index lists mail, files and database restore
      points with local dates; a subscription with no backup yet says so in
      plain words (no Storage Box host or repository in the message).
- [ ] Mail end to end: the mailbox dropdown lists exactly the
      subscription's mailboxes (`cli.php mailboxes`); a restore of a whole
      mailbox and one of `INBOX` only each create a new folder
      `Restored <date> <id>` with the messages, and the status page says so;
      a second restore of the same point reports that the folder exists.
- [ ] Files end to end: browsing descends one level per click with a
      breadcrumb; "This folder" restores only that path, "Everything" the
      whole home directory, into `restored-<date>-<id>/` (0700, owned by the
      subscription's system user, not served by the website); File Manager
      shows it; a second restore of the same point is refused with the
      "already exists" message.
- [ ] Database end to end: the dropdown lists one entry per database
      restore point (from the `db:<name>` tags, including a database dropped
      since), and the restore writes `restored-<date>-<id>/<db>.sql` (0600)
      that phpMyAdmin imports into a new database.
- [ ] Every restore form is a `pm_Form_Simple` POST; it returns
      `{"redirect": <status URL>}`, the restore outlives the request
      (`restore-run.php` detached), and a forged POST with a mailbox,
      restore point or database not in the lists is refused without
      starting anything.
- [ ] Status page: the `<meta http-equiv="refresh">` in the page body
      reloads it in Plesk's layout (Chrome and Firefox) until the record
      says done or failed; "Reload now" works without it. A record of
      another subscription (its id typed into the URL) reads as "not found".
- [ ] Rate limit: starting a second restore of the same subscription while
      one runs shows "Another restore of this subscription is still
      running"; two near-simultaneous POSTs leave one running and one
      failed record (the flock in `restore-run.php`).
- [ ] `pm_Context::getVarDir()` in `restore-run.php`, `restore-result.php`
      and `restore-running.php` (run through `--exec`) is the extension's
      var dir; `restores/` is created 0700 root and records are 0600.
- [ ] A restore killed mid-way (kill `restore-run.php`) shows "stopped
      before it finished" on the status page.
- [ ] `$this->render('restore/_style.phtml')` finds the partial from every
      restore view.

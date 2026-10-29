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

- [ ] The web process (Plesk's panel user) can run
      `/usr/sbin/plesk bin extension --exec relical <script>` through
      `proc_open` and the script runs as root, as Cloudflare DNS Sync's UI
      does through `exec`.
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

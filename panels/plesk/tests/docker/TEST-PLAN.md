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

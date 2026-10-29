#!/usr/bin/env bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq && apt-get install -y -qq dovecot-imapd mariadb-server >/dev/null
fail() { echo "FAIL: $*"; exit 1; }

useradd -r -u 2000 -U -d /nonexistent -s /usr/sbin/nologin popuser
M=/var/qmail/mailnames/example.test/alice/Maildir
mkdir -p $M/{cur,new,tmp} $M/.Sent/{cur,new,tmp}
printf 'From: a@example.test\nSubject: one\n\nbody\n'  > "$M/cur/1000.M1.e2e:2,S"
printf 'From: a@example.test\nSubject: two\n\nbody\n'  > "$M/cur/1001.M2.e2e:2,S"
printf 'From: a@example.test\nSubject: sent\n\nbody\n' > "$M/.Sent/cur/1002.M3.e2e:2,S"
echo cache > $M/dovecot.index.cache
echo partial > $M/tmp/inflight
chown -R popuser:popuser /var/qmail/mailnames

cat > /etc/dovecot/conf.d/99-relical-e2e.conf <<'EOC'
mail_location = maildir:/var/qmail/mailnames/%d/%n/Maildir
first_valid_uid = 2000
# doveadm needs no network listener; keep the test Dovecot off the network
# (it matters when the container shares the host network namespace).
service imap-login {
  inet_listener imap {
    port = 0
  }
  inet_listener imaps {
    port = 0
  }
}
passdb {
  driver = passwd-file
  args = /etc/dovecot/e2e-users
}
userdb {
  driver = passwd-file
  args = /etc/dovecot/e2e-users
}
EOC
echo 'alice@example.test:{PLAIN}e2e:2000:2000::/var/qmail/mailnames/example.test/alice::' > /etc/dovecot/e2e-users
dovecot

# A webspace owned by its system user, with a log that the default
# file_excludes keep out of the backup.
useradd -u 10001 -U -d /var/www/vhosts/example.test -s /usr/sbin/nologin exampleuser
W=/var/www/vhosts/example.test
mkdir -p $W/httpdocs $W/logs
echo '<h1>example</h1>' > $W/httpdocs/index.html
echo 'GET / 200' > $W/logs/access_log
chown -R exampleuser:exampleuser $W
chmod 710 $W

# MariaDB with one database; relical dumps it as root over the socket.
install -d -o mysql -g mysql /run/mysqld
mariadbd --user=mysql --skip-networking >/tmp/mariadb.log 2>&1 &
for _ in $(seq 60); do mysqladmin ping >/dev/null 2>&1 && break; sleep 1; done
mysqladmin ping >/dev/null || { cat /tmp/mariadb.log; fail "MariaDB did not start"; }
mysql -e "CREATE DATABASE example_wp; CREATE TABLE example_wp.posts (id INT PRIMARY KEY, title VARCHAR(64)); INSERT INTO example_wp.posts VALUES (1, 'hello e2e'), (2, 'second');"
install -d -m 755 /etc/relical
printf '[client]\nuser=root\nsocket=/run/mysqld/mysqld.sock\n' > /etc/relical/mysql.cnf
chmod 600 /etc/relical/mysql.cnf

# Stand-in for plesk bin pleskbackup: writes a tar where -output-file says.
cat > /usr/local/bin/fake-pleskbackup <<'EOS'
#!/bin/sh
while [ $# -gt 0 ]; do [ "$1" = -output-file ] && out="$2"; shift; done
[ -n "$PLESK_BACKUP_PASSWORD" ] || { echo "no PLESK_BACKUP_PASSWORD" >&2; exit 1; }
tar -cf "$out" -C /etc hostname os-release
EOS
chmod 755 /usr/local/bin/fake-pleskbackup

install -d -m 700 /etc/relical/keys
cat > /etc/relical/config.json <<'EOC'
{
  "version": 2,
  "server_id": "e2e.example.test",
  "restic_bin": "/work/restic",
  "state_dir": "/var/lib/relical",
  "keys_dir": "/etc/relical/keys",
  "mysqldump_bin": "/usr/bin/mysqldump",
  "mailbox_pattern": "/var/qmail/mailnames/{domain}/{mailbox}/Maildir",
  "mysql": {"defaults_file": "/etc/relical/mysql.cnf"},
  "default_destination": "local",
  "destinations": {"local": {"repository": "/srv/repo/{subscription}", "prune": "local"}},
  "subscriptions": [
    {
      "id": "1",
      "name": "example.test",
      "sys_user": "exampleuser",
      "uid": 10001,
      "gid": 10001,
      "home": "/var/www/vhosts/example.test",
      "features": ["mail", "files", "db"],
      "mail_domains": ["example.test"],
      "files": ["/var/www/vhosts/example.test"],
      "databases": [{"type": "mysql", "name": "example_wp"}]
    }
  ],
  "server": {
    "features": ["panel-config"],
    "config_command": ["/usr/local/bin/fake-pleskbackup", "-output-file", "{output}"],
    "config_ext": "tar"
  }
}
EOC
chmod 600 /etc/relical/config.json
R="/work/relical -config /etc/relical/config.json"

$R backup --all | tee /tmp/backup.json
if grep -q '"ok": false' /tmp/backup.json; then fail "backup --all"; fi
for u in mail files db panel-config; do grep -q "\"unit\": \"$u\"" /tmp/backup.json || fail "no $u result"; done
$R inventory | grep -q '"name": "example.test"'
# Dumps and the panel configuration never stay on disk unencrypted.
if find /var/lib/relical /tmp -name '*.sql' | grep .; then fail "a dump is on disk"; fi
if find /var/lib/relical/staging -type f | grep .; then fail "panel configuration left in staging"; fi

snapshot_of() { $R snapshots --subscription "$1" --unit "$2" | sed -n 's/^ *"id": "\([0-9a-f]*\)",*$/\1/p' | head -n 1; }
FILES_SNAP=$(snapshot_of example.test files)
[ -n "$FILES_SNAP" ] || fail "no files snapshot"
$R ls --subscription example.test --snapshot "$FILES_SNAP" | tee /tmp/ls.json
grep -q '"name": "httpdocs"' /tmp/ls.json || fail "ls misses httpdocs"
if grep -q '"name": "index.html"' /tmp/ls.json; then fail "ls went deeper than one level"; fi
# logs/** excludes the log directory with its contents.
if $R ls --subscription example.test --snapshot "$FILES_SNAP" --path logs > /tmp/ls-logs.json; then fail "the excluded logs were backed up"; fi
grep -q 'not in snapshot' /tmp/ls-logs.json || fail "ls of a missing path"
$R ls --subscription example.test --snapshot "$FILES_SNAP" --path httpdocs | grep -q '"name": "index.html"' || fail "ls --path httpdocs"

# Lose a file and a table, then restore both next to the live data.
rm $W/httpdocs/index.html
mysql -e "DROP TABLE example_wp.posts"
$R restore --subscription example.test --unit files --path httpdocs | tee /tmp/restore-files.json
RF=$(sed -n 's/^ *"restored_to": "\(.*\)",*$/\1/p' /tmp/restore-files.json)
case "$RF" in $W/restored-*) ;; *) fail "files restored to '$RF'";; esac
grep -qx '<h1>example</h1>' "$RF/httpdocs/index.html" || fail "restored file content"
[ "$(stat -c %U:%a "$RF")" = exampleuser:700 ] || fail "restored folder is $(stat -c %U:%a "$RF")"
[ "$(stat -c %U "$RF/httpdocs/index.html")" = exampleuser ] || fail "restored file owner"
[ ! -e "$RF/logs" ] || fail "logs restored"
[ ! -e $W/httpdocs/index.html ] || fail "the live httpdocs was written to"
if $R restore --subscription example.test --unit files --path httpdocs > /tmp/again-files.json; then
  fail "second files restore of the same snapshot was not refused"
fi
grep -q 'already exists' /tmp/again-files.json

$R restore --subscription example.test --unit db --database example_wp | tee /tmp/restore-db.json
RD=$(sed -n 's/^ *"restored_to": "\(.*\)",*$/\1/p' /tmp/restore-db.json)
case "$RD" in $W/restored-*/example_wp.sql) ;; *) fail "dump restored to '$RD'";; esac
[ "$(stat -c %U:%a "$RD")" = exampleuser:600 ] || fail "dump is $(stat -c %U:%a "$RD")"
grep -q 'CREATE TABLE `posts`' "$RD" || fail "dump lacks the table"
grep -q 'hello e2e' "$RD" || fail "dump lacks the rows"
if mysql -e "SELECT 1 FROM example_wp.posts" 2>/dev/null; then fail "the dump was imported"; fi
# The dump is usable: load it into a scratch database.
mysql -e "CREATE DATABASE scratch" && mysql scratch < "$RD"
[ "$(mysql -N -e 'SELECT title FROM scratch.posts WHERE id=1')" = 'hello e2e' ] || fail "dump does not load"

# The restored folders are built in root-only staging next to the homes.
[ "$(stat -c %U:%a /var/www/vhosts/.relical)" = root:700 ] || fail ".relical is $(stat -c %U:%a /var/www/vhosts/.relical)"
[ -z "$(ls -A /var/www/vhosts/.relical)" ] || fail "restore staging left behind"

# Set-id bits are dropped and device nodes left out (listed as dropped). restored-* folders are
# not backed up again.
mkdir -p $W/setid/dir $W/devs
cp /bin/true $W/setid/suid && chmod 4755 $W/setid/suid && chmod 2755 $W/setid/dir
mknod $W/devs/null c 1 3
chown -R exampleuser: $W/setid $W/devs
$R backup --subscription example.test --unit files | tee /tmp/backup-setid.json
grep -q '"ok": true' /tmp/backup-setid.json || fail "files backup with set-id files"
SETID_SNAP=$(sed -n 's/^ *"snapshot": "\([0-9a-f]*\)",*$/\1/p' /tmp/backup-setid.json)
[ -n "$SETID_SNAP" ] || fail "no snapshot id from the files backup"
if $R ls --subscription example.test --snapshot "$SETID_SNAP" --path "${RF#$W/}" > /tmp/ls-restored.json; then fail "a restored-* folder was backed up"; fi
$R restore --subscription example.test --unit files --snapshot "$SETID_SNAP" --path devs | tee /tmp/restore-dev.json
grep -q '"devs/null"' /tmp/restore-dev.json || fail "the dropped device node is not listed"
RDEV=$(sed -n 's/^ *"restored_to": "\(.*\)",*$/\1/p' /tmp/restore-dev.json)
[ -d "$RDEV/devs" ] && [ ! -e "$RDEV/devs/null" ] || fail "a device node was restored"
rm -rf "$RDEV"
[ -z "$(ls -A /var/www/vhosts/.relical)" ] || fail "restore with a device node left staging behind"
$R restore --subscription example.test --unit files --snapshot "$SETID_SNAP" --path setid | tee /tmp/restore-setid.json
RS=$(sed -n 's/^ *"restored_to": "\(.*\)",*$/\1/p' /tmp/restore-setid.json)
[ "$(stat -c %a "$RS/setid/suid")" = 755 ] || fail "set-user-id kept: $(stat -c %a "$RS/setid/suid")"
[ "$(stat -c %a "$RS/setid/dir")" = 755 ] || fail "set-group-id kept on a directory: $(stat -c %a "$RS/setid/dir")"
[ "$(stat -c %U "$RS/setid/suid")" = exampleuser ] || fail "set-id file owner"
rm -rf $W/setid $W/devs

$R restore --subscription _server --unit panel-config | tee /tmp/restore-panel.json
grep -q 'pleskrestore' /tmp/restore-panel.json
RP=$(sed -n 's/^ *"restored_to": "\(.*\)",*$/\1/p' /tmp/restore-panel.json)
case "$RP" in /var/lib/relical/restore/panel-config-*.tar) ;; *) fail "panel config restored to '$RP'";; esac
[ "$(stat -c %U:%a "$RP")" = root:600 ] || fail "panel config is $(stat -c %U:%a "$RP")"
tar -tf "$RP" | grep -qx hostname || fail "panel config tar content"
PANEL_SNAP=$(snapshot_of _server panel-config)
$R ls --subscription _server --snapshot "$PANEL_SNAP" | grep -q '"name": "panel-config-' || fail "ls _server"

export RESTIC_REPOSITORY=/srv/repo/example.test RESTIC_PASSWORD_FILE=/etc/relical/keys/example.test.key RESTIC_CACHE_DIR=/var/lib/relical/cache
if /work/restic ls latest --tag mail | grep -E 'dovecot\.index\.cache|/tmp/inflight'; then
  echo "FAIL: an excluded file was backed up"; exit 1
fi

rm "$M/cur/1001.M2.e2e:2,S"
$R restore --subscription example.test --domain example.test --mailbox alice | tee /tmp/restore.json
grep -q '"messages": 3' /tmp/restore.json
doveadm mailbox list -u alice@example.test | tee /tmp/list.txt
grep -q '^Restored ' /tmp/list.txt
doveadm fetch -u alice@example.test hdr.subject mailbox 'Restored*' | tee /tmp/fetch.txt
grep -q 'two' /tmp/fetch.txt

if $R restore --domain example.test --mailbox alice > /tmp/again.json; then
  echo "FAIL: second restore of the same snapshot was not refused"; exit 1
fi
grep -q 'already exists' /tmp/again.json

# Folder restore. The first snapshot's "Restored" folder now exists, so take a
# second snapshot (it still holds the Sent message), then lose that message.
$R backup --subscription example.test | tee /tmp/backup2.json
grep -q '"ok": true' /tmp/backup2.json
rm "$M/.Sent/cur/1002.M3.e2e:2,S"
$R restore --domain example.test --mailbox alice --folder Sent | tee /tmp/restore-folder.json
grep -q '"messages": 1' /tmp/restore-folder.json
restored_to() { sed -n 's/^ *"restored_to": "\(.*\)",*$/\1/p' "$1"; }
T1=$(restored_to /tmp/restore.json)
T2=$(restored_to /tmp/restore-folder.json)
if [ -z "$T2" ] || [ "$T2" = "$T1" ]; then
  echo "FAIL: folder restore target is '$T2' (first restore: '$T1')"; exit 1
fi
doveadm mailbox list -u alice@example.test | tee /tmp/list2.txt
doveadm fetch -u alice@example.test 'mailbox hdr.subject' mailbox "$T2*" | tee /tmp/fetch-folder.txt
grep -q '^hdr.subject: sent$' /tmp/fetch-folder.txt
grep -q "^mailbox: $T2" /tmp/fetch-folder.txt
if grep -q -E '^hdr.subject: (one|two)$' /tmp/fetch-folder.txt; then
  echo "FAIL: folder restore of Sent imported INBOX messages"; exit 1
fi

$R forget --all | tee /tmp/forget.json
if grep -q '"ok": false' /tmp/forget.json; then fail "forget --all"; fi
$R check --all --read-data-subset 100% | tee /tmp/check.json
if grep -q '"ok": false' /tmp/check.json; then fail "check --all"; fi
grep -q '"subscription": "_server"' /tmp/check.json || fail "check --all skipped _server"

# The report reads the state the runs above left, and every run is in the log.
$R report | tee /tmp/report.json
grep -q '"failed": 0' /tmp/report.json || fail "report counts a failure"
grep -q '"unit": "panel-config"' /tmp/report.json || fail "report lacks the panel-config row"
[ "$(stat -c %a /var/lib/relical/relical.log)" = 600 ] || fail "relical.log is not 0600"
for c in backup restore forget check; do
  grep -q "\"event\":\"end\",\"command\":\"$c\"" /var/lib/relical/relical.log || fail "no end line for $c in relical.log"
done
echo "E2E PASS"

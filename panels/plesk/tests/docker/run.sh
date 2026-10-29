#!/usr/bin/env bash
#
# Docker Plesk test for the Relical extension (plan Task 10).
#
# Starts the official plesk/plesk image and an atmoz/sftp container that
# stands in for the Storage Box (user u000000-sub1, port 2222), installs
# dist/relical-<version>.zip, sets the destination through the settings
# form, pins the host key, backs up mail, files, a database and the Plesk
# configuration, deletes the data, restores it the way the customer screens
# do, and checks the HTTP side with admin and customer sessions made with
# `plesk login`. Everything uses example.test names; nothing leaves the
# workstation except the image's own apt request for openssh-client (the
# image ships without ssh, sftp and ssh-keygen).
#
# Usage:
#   make plesk-ext && panels/plesk/tests/docker/run.sh
#
# Environment:
#   ZIP=<path>   the package to install (default dist/relical-0.2.0.zip)
#   KEEP=1       leave the containers running afterwards (removed otherwise)
#
# Re-running is safe: containers and the network from an earlier run are
# removed first. Exit 0 means every check passed; the first failed check
# stops the run with "FAIL: ...".
#
# systemd: the image boots /sbin/init. On a cgroup v2 host its documented
# command (-v /sys/fs/cgroup:/sys/fs/cgroup:ro) exits at once, so the
# container gets the host's cgroup namespace and a writable
# /sys/fs/cgroup (systemd then manages only its own docker-<id>.scope).
#
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/../../../.." && pwd)
ZIP=${ZIP:-$ROOT/dist/relical-0.2.0.zip}
NET=relical-test
P=relical-plesk
SFTP=relical-sftp
WORK=$(mktemp -d)
COOKIES="$WORK/cookies"
mkdir -p "$COOKIES"

cleanup() {
    local code=$?
    if [ "${KEEP:-0}" != 1 ]; then
        docker rm -f "$P" "$SFTP" >/dev/null 2>&1 || true
        docker network rm "$NET" >/dev/null 2>&1 || true
    else
        echo "KEEP=1: containers $P and $SFTP are still running (admin login: docker exec $P plesk login admin)"
    fi
    rm -rf "$WORK"
    exit $code
}
trap cleanup EXIT

say() { printf '\n== %s\n' "$*"; }
ok() { printf 'ok   %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
# px runs a bash snippet as root in the Plesk container.
px() { docker exec "$P" bash -c "$1"; }
# The root entry point as the panel user runs it (Plesk's mod_wrapper link).
RR=/opt/psa/admin/bin/modules/relical/relical-root
asPanel() { docker exec "$P" su -s /bin/sh psaadm -c "$RR $*"; }
newId() { printf '%s-%s' "$(date -u +%Y%m%d%H%M%S)" "$(openssl rand -hex 12)"; }

[ -f "$ZIP" ] || fail "$ZIP is missing; run make plesk-ext first"

# ---------------------------------------------------------------------------
say "1. containers"
docker rm -f "$P" "$SFTP" >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true
docker network create "$NET" >/dev/null
docker run -d --name "$P" --network "$NET" --hostname plesk.example.test \
    -p 127.0.0.1:8880:8880 -p 127.0.0.1:8443:8443 \
    --tmpfs /run --tmpfs /run/lock --tmpfs /tmp:rw,exec,size=2g \
    --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
    plesk/plesk:latest >/dev/null
cat > "$WORK/port.sh" <<'EOS'
#!/bin/sh
sed -i 's/^#\{0,1\}Port .*/Port 2222/' /etc/ssh/sshd_config
grep -q '^Port 2222' /etc/ssh/sshd_config || echo 'Port 2222' >> /etc/ssh/sshd_config
EOS
chmod 755 "$WORK/port.sh"
docker run -d --name "$SFTP" --network "$NET" --network-alias sftp.example.test \
    -v "$WORK/port.sh:/etc/sftp.d/port.sh:ro" atmoz/sftp:alpine 'u000000-sub1::1001:1001:relical' >/dev/null
ok "started $P and $SFTP"

say "2. Plesk and test data"
timeout 900 bash -c "until [ \"\$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8880/login_up.php)\" = 200 ]; do sleep 5; done" \
    || fail "Plesk did not answer on 8880 within 15 minutes"
timeout 300 bash -c "until docker exec $P plesk bin subscription --list >/dev/null 2>&1; do sleep 5; done" || fail "plesk bin is not ready"
[ "$(px 'systemctl is-system-running' || true)" != offline ] || fail "systemd is not running in the container"
ok "Plesk $(px 'plesk version | sed -n "s/^ *Product version: //p"'), systemd $(px 'systemctl is-system-running' || true)"
px 'command -v ssh-keygen >/dev/null || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-client >/dev/null; }' \
    || fail "could not install openssh-client in the Plesk container"
IP=$(px "plesk bin ipmanage --ip_list | awk 'NR==2 {split(\$3, a, \":\"); split(a[2], b, \"/\"); print b[1]}'")
px "plesk bin subscription --create example.test -owner admin -service-plan 'Default Domain' -ip $IP -login exampleuser -passwd 'Ex4mple-Pass-1' >/dev/null"
# A chroot shell puts dev/null, dev/random and dev/urandom in the webspace.
px "plesk bin subscription --update example.test -shell /opt/psa/bin/chrootsh >/dev/null"
px "plesk bin subdomain --create sub -domain example.test >/dev/null"
px "plesk bin domalias --create alias.test -domain example.test >/dev/null"
px "plesk bin mail --create alice@example.test -passwd 'Al1ce-Pass-x' -mailbox true >/dev/null"
px "plesk bin database --create example_wp -domain example.test -type mysql >/dev/null"
px "plesk bin customer --create cust1 -name 'Customer One' -passwd 'Cust1-Pass-x9' -email cust1@example.test >/dev/null"
px 'M=/var/qmail/mailnames/example.test/alice/Maildir; t=$(mktemp)
    printf "From: bob@example.test\r\nTo: alice@example.test\r\nSubject: relical test message\r\nMessage-ID: <relical-test-1@example.test>\r\n\r\nHello from the Relical Docker test.\r\n" > $t
    f="$M/cur/1761700000.M1P1.plesk.example.test,S=$(stat -c %s $t):2,S"; mv $t "$f"; chown popuser:popuser "$f"; chmod 600 "$f"'
px 'mysql -uadmin -p"$(cat /etc/psa/.psa.shadow)" example_wp -e "CREATE TABLE posts (id INT PRIMARY KEY, title VARCHAR(100)); INSERT INTO posts VALUES (1, \"hello relical\")"'
px 'echo "relical docker test file" > /var/www/vhosts/example.test/httpdocs/relical-test.txt; chown exampleuser:psacln /var/www/vhosts/example.test/httpdocs/relical-test.txt'
[ "$(px 'find /var/www/vhosts/example.test/dev -type c | wc -l')" -ge 1 ] || fail "the chroot webspace has no device nodes"
DOM=$(px "plesk db -Ne \"select id from domains where name='example.test'\"")
SUB=$(px "plesk db -Ne \"select id from domains where name='sub.example.test'\"")
[ "$(px "plesk db -Ne \"select webspace_id from domains where id=$DOM\"")" = 0 ] || fail "webspace_id of a main domain is not 0"
[ "$(px "plesk db -Ne \"select webspace_id from domains where id=$SUB\"")" = "$DOM" ] || fail "webspace_id of the subdomain is not its subscription"
ok "subscription example.test (id $DOM, chroot shell), subdomain (id $SUB), alias, mailbox, database, customer cust1"

# ---------------------------------------------------------------------------
say "3. install"
docker cp "$ZIP" "$P:/root/relical.zip"
px 'plesk bin extension -i /root/relical.zip' | tee "$WORK/install.txt"
px 'test -x /opt/psa/admin/sbin/modules/relical/relical-root && test -L /opt/psa/admin/bin/modules/relical/relical-root' \
    || fail "Plesk did not install sbin/relical-root and its mod_wrapper link"
# Plesk registers hook plan items on the first plan operation that names them.
for item in relical-mail relical-web; do
    px "plesk bin service_plan --add-custom-plan-item 'Default Domain' -custom-plan-item-name urn:ext:relical:plan-item-sdk:$item >/dev/null" \
        || fail "plan item $item cannot be added to a plan"
    px "plesk bin service_plan --show-custom-plan-items 'Default Domain'" | grep -q "urn:ext:relical:plan-item-sdk:$item\$" \
        || fail "plan item $item is not on the plan"
done
ok "plan items urn:ext:relical:plan-item-sdk:relical-mail and relical-web"
px 'cd /opt/psa/admin/plib/modules/relical/bin && sed "s#  #  /usr/local/lib/relical/#" SHA256SUMS | sha256sum --quiet -c -' \
    || fail "installed binaries do not match the package's SHA256SUMS"
[ "$(px 'stat -c %a:%U /usr/local/lib/relical/relical /usr/local/lib/relical/restic | sort -u')" = 755:root ] || fail "binary modes"
[ "$(px 'stat -c %a /etc/relical /etc/relical/keys /etc/relical/ssh /var/lib/relical | sort -u')" = 700 ] || fail "directory modes"
[ "$(px 'stat -c %a:%U /etc/relical/mysql.cnf /etc/relical/ssh/id_ed25519')" = "$(printf '600:root\n600:root')" ] || fail "mysql.cnf or the SSH key is not 0600 root"
px 'test ! -e /etc/relical/config.json' || fail "config.json was written before the destination was saved"
px 'systemctl is-enabled relical-backup.timer relical-check.timer >/dev/null' || fail "timers are not enabled"
px 'grep -q "^Nice=19" /etc/systemd/system/relical-backup.service && grep -q "^IOSchedulingClass=idle" /etc/systemd/system/relical-check.service' || fail "services are not at idle priority"
px 'grep -q "^OnFailure=relical-notify@%n.service" /etc/systemd/system/relical-backup.service && grep -q "^OnFailure=relical-notify@%n.service" /etc/systemd/system/relical-check.service' || fail "services do not name relical-notify@ in OnFailure="
px 'grep -q "^ExecStart=/opt/psa/admin/sbin/modules/relical/relical-root notify.php %i$" "/etc/systemd/system/relical-notify@.service"' || fail "relical-notify@.service does not run notify.php through relical-root"
px 'systemd-analyze verify /etc/systemd/system/relical-backup.service /etc/systemd/system/relical-check.service "/etc/systemd/system/relical-notify@.service" 2>&1 | grep relical' && fail "systemd-analyze verify complains about a Relical unit"
ok "binaries match SHA256SUMS; /etc/relical, keys, ssh, mysql.cnf modes; timers enabled; no config.json yet; OnFailure= and relical-notify@ in place"
# Plesk runs the hooks and `extension --exec` as psaadm; the scripts hand themselves to relical-root.
[ "$(px 'plesk bin extension --exec relical running.php')" = '{"running":false}' ] || fail "running.php through extension --exec"
ok "plesk bin extension --exec relical <script> reaches root"

# ---------------------------------------------------------------------------
say "4. settings, host key, probe"
login() { # login <plesk user> <cookie name>
    local secret
    secret=$(px "plesk login $1" | head -n 1 | sed 's/.*secret=//')
    rm -f "$COOKIES/$2"
    curl -sk -c "$COOKIES/$2" -b "$COOKIES/$2" -o /dev/null "https://127.0.0.1:8443/login?secret=$secret"
}
get() { # get <cookie> <path> <out file>; prints the HTTP code
    curl -sk -b "$COOKIES/$1" -c "$COOKIES/$1" -o "$3" -w '%{http_code}' "https://127.0.0.1:8443$2"
}
post() { # post <cookie> <path> <form page> <fields>; prints the JSON answer
    local token
    token=$(grep -o 'name="forgery_protection_token" value="[0-9a-f]*"' "$3" | head -n 1 | grep -o '[0-9a-f]\{32\}')
    curl -sk -b "$COOKIES/$1" -c "$COOKIES/$1" -H 'X-Requested-With: XMLHttpRequest' \
        --data "forgery_protection_token=$token&$4" "https://127.0.0.1:8443$2"
}
login admin admin
[ "$(get admin /modules/relical/index.php "$WORK/admin.html")" = 200 ] || fail "admin settings page"
grep -q "$(px 'cut -d" " -f2 /etc/relical/ssh/id_ed25519.pub')" "$WORK/admin.html" || fail "the settings page does not show the public key"
grep -q 'No status yet' "$WORK/admin.html" || fail "the status table before configuration does not show the core's error"
[ "$(get admin /modules/relical/index.php/index/log "$WORK/log.html")" = 200 ] && grep -q 'There is no log yet' "$WORK/log.html" || fail "log screen before the first run"
ok "settings page shows the key and the unconfigured status; log screen says there is no log yet"
FORM='scope=all&sftp_host=sftp.example.test&sftp_port=2222&sftp_user=u000000-sub1&sftp_path=relical&retention_daily=14&retention_weekly=8&retention_monthly=12&backup_time=02:15&limit_upload_kib=0&hidden='
curl -sk -b "$COOKIES/admin" -H 'X-Requested-With: XMLHttpRequest' --data "$FORM" https://127.0.0.1:8443/modules/relical/index.php/index/index \
    | grep -q 'Session token has expired' || fail "a POST without the forgery token was not refused"
post admin /modules/relical/index.php/index/index "$WORK/admin.html" "${FORM/sftp_port=2222/sftp_port=99999}" \
    | grep -q '"sftp_port":\["Storage Box port must be between 1 and 65535.' || fail "an invalid port did not come back as a field error"
post admin /modules/relical/index.php/index/index "$WORK/admin.html" "$FORM" | grep -q '"redirect":"\\/modules\\/relical\\/"' || fail "a valid save did not redirect"
[ "$(px 'stat -c %a:%U /etc/relical/config.json')" = 600:root ] || fail "config.json is not 0600 root"
px '/usr/local/lib/relical/relical -config /etc/relical/config.json inventory' > "$WORK/inventory.json" || fail "the core cannot read the inventory"
grep -q '"name": "example.test"' "$WORK/inventory.json" || fail "example.test is not in the inventory"
grep -q '"restored-\*"' "$WORK/inventory.json" || fail "restored-* is not excluded"
px 'systemctl show relical-backup.timer -p TimersCalendar' | grep -q '02:15:00' || fail "saving 02:15 did not reach systemd"
ok "forgery token required, field errors, save writes config.json 0600, inventory resolves, timer moved to 02:15"

px 'plesk bin extension --exec relical hostkey.php scan' > "$WORK/scan.json"
FP=$(sed -n 's/.*"fingerprint":"\([^"]*\)".*/\1/p' "$WORK/scan.json")
[ "$FP" = "$(docker exec "$SFTP" ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub | awk '{print $2}')" ] || fail "scanned fingerprint $FP differs from the server's"
if px 'plesk bin extension --exec relical hostkey.php trust SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA' >/dev/null 2>&1; then fail "a different fingerprint was trusted"; fi
px 'test ! -e /etc/relical/ssh/known_hosts' || fail "a refused trust wrote known_hosts"
px "plesk bin extension --exec relical hostkey.php trust $FP" | grep -q '"ok":true' || fail "trusting the scanned key"
[ "$(px 'stat -c %a:%U /etc/relical/ssh/known_hosts')" = 600:root ] || fail "known_hosts is not 0600 root"
px 'grep -q "^\[sftp.example.test\]:2222 ssh-ed25519 " /etc/relical/ssh/known_hosts' || fail "known_hosts line"
px 'plesk bin extension --exec relical probe.php' | grep -q 'Permission denied (publickey' || fail "probe without the authorised key did not give sftp's reason"
PUB=$(px 'cat /etc/relical/ssh/id_ed25519.pub')
docker exec "$SFTP" sh -c "mkdir -p /home/u000000-sub1/.ssh && echo '$PUB' > /home/u000000-sub1/.ssh/authorized_keys && chown -R 1001:1001 /home/u000000-sub1/.ssh && chmod 700 /home/u000000-sub1/.ssh && chmod 600 /home/u000000-sub1/.ssh/authorized_keys"
px 'plesk bin extension --exec relical probe.php' | grep -q '"ok":true' || fail "probe with the key"
ok "host key scan matches ssh-keygen -l, a changed key pins nothing, known_hosts 0600, probe fails then connects"

# ---------------------------------------------------------------------------
say "5. backup"
px 'plesk bin extension --exec relical cli.php backup --all' > "$WORK/backup.json" || { cat "$WORK/backup.json"; fail "backup --all"; }
for unit in mail files db panel-config; do
    grep -q "\"unit\": \"$unit\"" "$WORK/backup.json" || fail "no $unit result"
done
if grep -q '"ok": false' "$WORK/backup.json"; then cat "$WORK/backup.json"; fail "a unit failed"; fi
snap() { sed -n "/\"unit\": \"$1\"/,/\"snapshot\"/s/.*\"snapshot\": \"\([0-9a-f]*\)\".*/\1/p" "$2" | head -n 1 | cut -c1-8; }
MAIL1=$(snap mail "$WORK/backup.json"); FILES1=$(snap files "$WORK/backup.json"); DB1=$(snap db "$WORK/backup.json")
[ -z "$(px 'find /var/lib/relical /tmp /var/www/vhosts -name "*.sql" -o -name "*.tar" -path "/var/lib/relical/*"')" ] || fail "a dump or tar was left on disk"
px 'plesk bin extension --exec relical cli.php status' | grep -q '"panel-config"' || fail "status"
px 'plesk bin extension --exec relical cli.php report' > "$WORK/report.json"
grep -q '"failed": 0' "$WORK/report.json" && grep -q '"ok": 4' "$WORK/report.json" || fail "report"
ok "mail $MAIL1, files $FILES1, db $DB1 and panel-config backed up (real pleskbackup); no dump on disk; status and report"
px 'systemctl start relical-notify@relical-backup.service' || fail "relical-notify@relical-backup.service did not run"
px 'grep -q "relical-notify: relical-backup.service failed" /var/lib/relical/relical.log' || fail "the failure notice is not in the run log"
[ "$(px 'stat -c %a:%U /var/lib/relical/last-failure.json')" = 600:root ] || fail "last-failure.json is not 0600 root"
px 'journalctl -u relical-notify@relical-backup.service --no-pager -n 20' | tee "$WORK/notify.txt" | grep -q '"logged":true' || fail "notify.php did not log: $(cat "$WORK/notify.txt")"
grep -o '"mailed":[a-z]*,"to":"[^"]*"' "$WORK/notify.txt" || true   # mail delivery is reported, not required here
get admin /modules/relical/index.php "$WORK/admin-failed.html" >/dev/null
grep -q 'Last failed run' "$WORK/admin-failed.html" && grep -q 'Last successful backup' "$WORK/admin-failed.html" || fail "the settings page does not show the last failure and success"
ok "relical-notify@ logs the failure, keeps last-failure.json, the settings page shows it"

# ---------------------------------------------------------------------------
say "6. restores (as restore-run.php is started by the customer screens)"
px 'rm /var/www/vhosts/example.test/httpdocs/relical-test.txt'
px 'mysql -uadmin -p"$(cat /etc/psa/.psa.shadow)" example_wp -e "DROP TABLE posts"'
px 'doveadm expunge -u alice@example.test mailbox INBOX ALL'
W=/var/www/vhosts/example.test
asPanel "restore-run.php $(newId) restore --subscription example.test --unit files --snapshot $FILES1 --path httpdocs" > "$WORK/r-files.json"
RF=$(px "ls -d $W/restored-*-$FILES1")
[ "$(px "stat -c %a:%U $RF")" = 700:exampleuser ] || fail "restored folder is not 0700 exampleuser"
[ "$(px "cat $RF/httpdocs/relical-test.txt")" = 'relical docker test file' ] || fail "restored file content"
[ "$(px "stat -c %U $RF/httpdocs/relical-test.txt")" = exampleuser ] || fail "restored file owner"
ID=$(newId)
if asPanel "restore-run.php $ID restore --subscription example.test --unit files --snapshot $FILES1 --path httpdocs" >/dev/null 2>&1; then fail "a second restore of the same point was not refused"; fi
asPanel "restore-result.php $ID" | grep -q 'already exists' || fail "the refusal is not in the record"
ok "files: httpdocs restored into $(basename "$RF") (0700, exampleuser); the same point again is refused"

asPanel "restore-run.php $(newId) restore --subscription example.test --unit db --database example_wp --snapshot $DB1" > /dev/null
SQL=$(px "ls $W/restored-*-$DB1/example_wp.sql")
[ "$(px "stat -c %a:%U $SQL")" = 600:exampleuser ] || fail "dump is not 0600 exampleuser"
px "mysql -uadmin -p\"\$(cat /etc/psa/.psa.shadow)\" -e 'CREATE DATABASE scratch_wp' && mysql -uadmin -p\"\$(cat /etc/psa/.psa.shadow)\" scratch_wp < $SQL"
[ "$(px 'mysql -uadmin -p"$(cat /etc/psa/.psa.shadow)" -N scratch_wp -e "select title from posts where id=1"')" = 'hello relical' ] || fail "dump does not load"
px 'mysql -uadmin -p"$(cat /etc/psa/.psa.shadow)" -e "DROP DATABASE scratch_wp"'
ok "db: example_wp.sql (0600, exampleuser) loads into a scratch database"

asPanel "restore-run.php $(newId) restore --subscription example.test --unit mail --domain example.test --mailbox alice --snapshot $MAIL1" > "$WORK/r-mail.json"
grep -q "\"restored_to\":\"INBOX.Restored [0-9-]* $MAIL1\"" "$WORK/r-mail.json" || fail "mail restore: $(cat "$WORK/r-mail.json")"
px "doveadm fetch -u alice@example.test hdr.subject mailbox 'INBOX.Restored*' ALL" | grep -q 'relical test message' || fail "restored message not found"
ID=$(newId)
if asPanel "restore-run.php $ID restore --subscription example.test --unit mail --domain example.test --mailbox alice --snapshot $MAIL1 --folder INBOX" >/dev/null 2>&1; then fail "a second mail restore of the same point was not refused"; fi
asPanel "restore-result.php $ID" | grep -q 'already exists' || fail "mail refusal not recorded"
ok "mail: whole mailbox into INBOX.Restored <date> $MAIL1; the same point again is refused"

for args in "--subscription _server --unit panel-config" "--subscription _server --unit files"; do
    if asPanel "restore-run.php $(newId) restore $args" > "$WORK/server.json" 2>&1; then fail "restore-run accepted $args"; fi
    grep -q 'refused' "$WORK/server.json" || fail "_server refusal: $(cat "$WORK/server.json")"
done
ok "_server is refused by restore-run.php"

# A second backup, then the whole webspace (with the chroot's device nodes),
# and INBOX only from the new mail point.
px 'M=/var/qmail/mailnames/example.test/alice/Maildir; t=$(mktemp)
    printf "From: bob@example.test\r\nTo: alice@example.test\r\nSubject: second message\r\n\r\nA second message.\r\n" > $t
    f="$M/cur/1761700100.M2P1.plesk.example.test,S=$(stat -c %s $t):2,S"; mv $t "$f"; chown popuser:popuser "$f"; chmod 600 "$f"'
px 'plesk bin extension --exec relical cli.php backup --subscription example.test' > "$WORK/backup2.json"
FILES2=$(snap files "$WORK/backup2.json"); MAIL2=$(snap mail "$WORK/backup2.json")
asPanel "restore-run.php $(newId) restore --subscription example.test --unit files --snapshot $FILES2" > "$WORK/r-whole.json"
grep -q '"dropped":\["dev/null","dev/random","dev/urandom"\]' "$WORK/r-whole.json" || fail "device nodes not listed as dropped: $(cat "$WORK/r-whole.json")"
px "test -d $W/restored-*-$FILES2/httpdocs && test -z \"\$(find $W/restored-*-$FILES2 -type c)\"" || fail "whole-webspace restore"
px "test ! -e $W/restored-*-$FILES2/restored-*" || fail "a restored-* copy was backed up again"
asPanel "restore-run.php $(newId) restore --subscription example.test --unit mail --domain example.test --mailbox alice --snapshot $MAIL2 --folder INBOX" > "$WORK/r-inbox.json"
grep -q '"messages":0' "$WORK/r-inbox.json" && fail "INBOX restore imported nothing"
px "doveadm mailbox list -u alice@example.test" | grep -q "^INBOX.Restored [0-9-]* $MAIL2.INBOX\$" || fail "INBOX-only restore folder"
px "doveadm fetch -u alice@example.test hdr.subject mailbox 'INBOX.Restored * $MAIL2.INBOX' ALL" | grep -q 'second message' || fail "INBOX-only restore content"
ok "whole webspace $FILES2 lists dev/null, dev/random, dev/urandom as dropped; restored-* not backed up; INBOX only from $MAIL2"

# ---------------------------------------------------------------------------
say "7. HTTP: admin and customer screens"
[ "$(get admin "/modules/relical/index.php/restore/index?dom_id=$DOM" "$WORK/a-idx.html")" = 200 ] || fail "admin restore index"
grep -q 'Restore mail' "$WORK/a-idx.html" && grep -q "$FILES2" "$WORK/a-idx.html" || fail "admin restore index content"
[ "$(get admin "/modules/relical/index.php/restore/index?site_id=$SUB" "$WORK/a-sub.html")" = 200 ] || fail "restore index for the subdomain (site_id)"
[ "$(get admin "/modules/relical/index.php/restore/index?alias_id=1" "$WORK/a-alias.html")" = 302 ] || fail "an alias must not open the restore screens"
[ "$(get admin "/modules/relical/index.php/index/backup?subscription=BAD" "$WORK/a-bad.html")" = 302 ] || fail "an invalid GET backup"
curl -sk -b "$COOKIES/admin" -D - -o /dev/null "https://127.0.0.1:8443/modules/relical/index.php/index/backup?subscription=BAD" \
    | grep -qi '^location: https://127.0.0.1:8443/modules/relical/' || fail "invalid backup redirects elsewhere"
ok "admin: restore index for dom_id and site_id, alias refused, invalid backup redirects to the settings page"

login cust1 cust
denied() { # denied <path>: a 302/303 whose target says Permission denied
    curl -sk -L -b "$COOKIES/cust" -c "$COOKIES/cust" -o "$WORK/denied.html" "https://127.0.0.1:8443$1"
    grep -q 'Permission denied' "$WORK/denied.html"
}
denied "/modules/relical/index.php/restore/index?dom_id=$DOM" || fail "a customer opened another account's subscription"
denied "/modules/relical/index.php/index/index" || fail "a customer opened the administrator screens"
px "plesk bin subscription --change-owner example.test -owner cust1 >/dev/null"
[ "$(get cust "/modules/relical/index.php/restore/index?dom_id=$DOM" "$WORK/c-idx.html")" = 200 ] || fail "customer restore index"
grep -q 'Restore mail' "$WORK/c-idx.html" || fail "customer restore index content"
grep -q 'sftp.example.test\|u000000-sub1\|/etc/relical' "$WORK/c-idx.html" && fail "the customer page names the Storage Box or /etc/relical"
denied "/modules/relical/index.php/index/index" || fail "the owning customer opened the administrator screens"
px "plesk bin subscription_settings --update example.test -ext_permission_relical_relical_restore false >/dev/null"
denied "/modules/relical/index.php/restore/index?dom_id=$DOM" || fail "relical_restore switched off still lets the customer in"
px "plesk bin subscription_settings --update example.test -ext_permission_relical_relical_restore true >/dev/null"
ok "customer: foreign subscription and admin screens refused; own subscription opens; relical_restore off refuses"

# Coverage by plan item.
px "/opt/psa/admin/sbin/modules/relical/relical-root inventory.php" > /dev/null
px "plesk bin subscription --unlock-subscription example.test >/dev/null 2>&1 || true"
px 'printf "%s\n" "<?php" "pm_Context::init(\"relical\");" "pm_Settings::set(\$argv[1], \$argv[2]);" > /opt/psa/admin/plib/modules/relical/scripts/zz-test-set.php'
setting() { px "/opt/psa/admin/bin/php -dauto_prepend_file=sdk.php /opt/psa/admin/plib/modules/relical/scripts/zz-test-set.php $1 $2"; }
setting scope plan-items
px "plesk bin service_plan --remove-custom-plan-item 'Default Domain' -custom-plan-item-name urn:ext:relical:plan-item-sdk:relical-web >/dev/null && plesk bin subscription --sync-subscription example.test >/dev/null"
px "$RR inventory.php" | grep -q '"features":\["mail"\]' || fail "a plan with relical-mail only must give mail only"
[ "$(get cust "/modules/relical/index.php/restore/database?dom_id=$DOM" "$WORK/c-db.html")" = 302 ] || fail "database screen without relical-web"
px "plesk bin service_plan --remove-custom-plan-item 'Default Domain' -custom-plan-item-name urn:ext:relical:plan-item-sdk:relical-mail >/dev/null && plesk bin subscription --sync-subscription example.test >/dev/null"
get cust "/modules/relical/index.php/restore/index?dom_id=$DOM" "$WORK/c-nc.html" >/dev/null
grep -q 'not included in the plan' "$WORK/c-nc.html" || fail "uncovered subscription page"
setting scope all
px 'rm /opt/psa/admin/plib/modules/relical/scripts/zz-test-set.php'
ok "plan-items scope: relical-mail gives mail only, no item gives the not-included page"

# Admin run buttons.
get admin "/modules/relical/index.php/index/backup?all=1" "$WORK/run.html" >/dev/null
post admin "/modules/relical/index.php/index/backup?all=1" "$WORK/run.html" 'all=1&hidden=' >/dev/null
sleep 2
post admin "/modules/relical/index.php/index/backup?all=1" "$WORK/run.html" 'all=1&hidden=' >/dev/null
get admin /modules/relical/index.php "$WORK/after-run.html" >/dev/null
grep -q 'already in progress' "$WORK/after-run.html" || fail "a second Run everything now while one runs"
timeout 600 bash -c "while docker exec $P pgrep -f 'relical -config /etc/relical/config.json backup' >/dev/null; do sleep 3; done" || fail "the background run did not finish"
ok "Run everything now starts a detached run; a second click says a run is already in progress"

if px 'grep -i "fatal\|warning\|exception" /var/log/plesk/panel.log | grep -i relical | grep -v "Permission denied"' > "$WORK/panel-errors.txt"; then
    cat "$WORK/panel-errors.txt"; fail "panel.log has PHP errors from the extension"
fi
ok "no PHP fatal, warning or exception from the extension in panel.log"

# ---------------------------------------------------------------------------
say "8. install edge cases"
px 'printf "%s\n" "<?php" "pm_Context::init(\"relical\");" "pm_Settings::set(\"sftp_port\", \$argv[1]);" > /opt/psa/admin/plib/modules/relical/scripts/zz-test-set.php'
px '/opt/psa/admin/bin/php -dauto_prepend_file=sdk.php /opt/psa/admin/plib/modules/relical/scripts/zz-test-set.php 0'
before=$(px 'stat -c %Y /etc/relical/config.json')
if px 'plesk bin extension --exec relical post-install.php' > "$WORK/bad-install.txt" 2>&1; then fail "post-install accepted invalid settings"; fi
grep -q 'installation failed: Storage Box port must be between 1 and 65535' "$WORK/bad-install.txt" || fail "invalid settings: $(cat "$WORK/bad-install.txt")"
[ "$(px 'stat -c %Y /etc/relical/config.json')" = "$before" ] || fail "config.json changed although post-install stopped"
px '/opt/psa/admin/bin/php -dauto_prepend_file=sdk.php /opt/psa/admin/plib/modules/relical/scripts/zz-test-set.php 2222; rm /opt/psa/admin/plib/modules/relical/scripts/zz-test-set.php'
px 'plesk bin extension --exec relical post-install.php upgrade 0.2.0' | grep -q 'Relical: done' || fail "post-install upgrade run"
ok "invalid saved settings stop post-install before anything changes; a re-run as upgrade finishes"

px 'touch /usr/local/lib/relical/local-note.txt'
px 'plesk bin extension -u relical' > "$WORK/uninstall.txt" 2>&1 || { cat "$WORK/uninstall.txt"; fail "uninstall"; }
px 'test -e /usr/local/lib/relical/local-note.txt && test ! -e /usr/local/lib/relical/relical && test ! -e /etc/systemd/system/relical-backup.timer' \
    || fail "removal did not keep the foreign file or left Relical's files"
px 'test -d /etc/relical/keys && test -d /var/lib/relical' || fail "removal deleted the keys or state"
ok "removal keeps a foreign file in /usr/local/lib/relical, removes the units and binaries, keeps keys and state"

say "PASS"

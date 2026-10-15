#!/usr/bin/env bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq && apt-get install -y -qq dovecot-imapd >/dev/null

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

install -d -m 700 /etc/relical/keys
cat > /etc/relical/config.json <<'EOC'
{
  "server_id": "e2e.example.test",
  "restic_bin": "/work/restic",
  "state_dir": "/var/lib/relical",
  "keys_dir": "/etc/relical/keys",
  "mailbox_pattern": "/var/qmail/mailnames/{domain}/{mailbox}/Maildir",
  "domains": ["example.test"],
  "default_destination": "local",
  "destinations": {"local": {"repository": "/srv/repo/{domain}", "prune": "local"}}
}
EOC
chmod 600 /etc/relical/config.json
R="/work/relical -config /etc/relical/config.json"

$R backup --domain example.test | tee /tmp/backup.json
grep -q '"ok": true' /tmp/backup.json

export RESTIC_REPOSITORY=/srv/repo/example.test RESTIC_PASSWORD_FILE=/etc/relical/keys/example.test.key RESTIC_CACHE_DIR=/var/lib/relical/cache
if /work/restic ls latest | grep -E 'dovecot\.index\.cache|/tmp/inflight'; then
  echo "FAIL: an excluded file was backed up"; exit 1
fi

rm "$M/cur/1001.M2.e2e:2,S"
$R restore --domain example.test --mailbox alice | tee /tmp/restore.json
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
$R backup --domain example.test | tee /tmp/backup2.json
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

$R forget --domain example.test | grep -q '"ok": true'
$R check --domain example.test --read-data-subset 100% | grep -q '"ok": true'
echo "E2E PASS"

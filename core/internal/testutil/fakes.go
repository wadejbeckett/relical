// Package testutil provides fake restic and doveadm binaries for tests.
// They log their arguments and behave according to FAKE_* variables,
// which tests pass through the runner's own Env (the runner does not
// inherit the parent environment).
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

const fakeRestic = `#!/bin/sh
echo "$*" >> "$FAKE_LOG"
env | grep -E '^RESTIC_' | sort >> "$FAKE_LOG.env"
[ -n "$RESTIC_PASSWORD" ] && echo "LEAKED RESTIC_PASSWORD" >> "$FAKE_LOG"
while [ "${1#--}" != "$1" ]; do shift 2; done
case "$1" in
  cat) exit "${FAKE_CAT_EXIT:-0}" ;;
  backup) cat "$FAKE_BACKUP_OUT"; exit "${FAKE_BACKUP_EXIT:-0}" ;;
  snapshots) cat "$FAKE_SNAPSHOTS_OUT"; exit 0 ;;
  restore)
    shift
    while [ $# -gt 0 ]; do
      case "$1" in --target) t="$2"; shift ;; --include) i="$2"; shift ;; esac
      shift
    done
    mkdir -p "$t$i/cur" "$t$i/.Sent/cur"
    printf 'Subject: one\n\nx\n' > "$t$i/cur/1.host:2,S"
    printf 'Subject: sent\n\nx\n' > "$t$i/.Sent/cur/2.host:2,S"
    exit "${FAKE_RESTORE_EXIT:-0}" ;;
  forget|check|init) exit "${FAKE_OTHER_EXIT:-0}" ;;
esac
exit 0
`

const fakeDoveadm = `#!/bin/sh
echo "$*" >> "$FAKE_LOG"
case "$*" in
  "mailbox list"*) cat "$FAKE_LIST" 2>/dev/null; exit 0 ;;
  "import"*) for a in "$@"; do case "$a" in maildir:*) p="${a#maildir:}"; p="${p%%:*}"; ls -R "$p" >> "$FAKE_LOG";; esac; done
             [ -n "$FAKE_IMPORT_SLEEP" ] && sleep "$FAKE_IMPORT_SLEEP"
             exit "${FAKE_IMPORT_EXIT:-0}" ;;
  "-f flow mailbox status"*) # real Dovecot 2.3 prints the mailbox name, then key=value pairs (no mailbox= key)
                              echo "Restored 2026-10-15 fedcba98.INBOX messages=1"; echo "Restored 2026-10-15 fedcba98.Sent messages=1"; exit 0 ;;
esac
exit 0
`

func write(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// FakeRestic returns the fake binary, the FAKE_* env to put into
// Runner.Env, and a function that reads the argument log.
func FakeRestic(t *testing.T) (string, map[string]string, func() string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "log")
	env := map[string]string{
		"FAKE_LOG":           log,
		"FAKE_BACKUP_OUT":    filepath.Join(dir, "backup.out"),
		"FAKE_SNAPSHOTS_OUT": filepath.Join(dir, "snapshots.out"),
	}
	os.WriteFile(env["FAKE_BACKUP_OUT"], []byte(
		`{"message_type":"status","percent_done":0.5}`+"\n"+
			`{"message_type":"summary","files_new":3,"data_added":2048,"data_added_packed":1024,"total_duration":1.5,"snapshot_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`+"\n"), 0o600)
	os.WriteFile(env["FAKE_SNAPSHOTS_OUT"], []byte(
		`[{"id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","time":"2026-10-14T02:00:00Z","paths":["/m"],"hostname":"h","tags":["backup"]},`+
			`{"id":"fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210","time":"2026-10-15T02:00:00Z","paths":["/m"],"hostname":"h","tags":["backup"]}]`), 0o600)
	read := func() string { b, _ := os.ReadFile(log); return string(b) }
	return write(t, "restic", fakeRestic), env, read
}

// FakeDoveadm returns a fake doveadm that logs to env["FAKE_LOG"].
func FakeDoveadm(t *testing.T) string {
	t.Helper()
	return write(t, "doveadm", fakeDoveadm)
}

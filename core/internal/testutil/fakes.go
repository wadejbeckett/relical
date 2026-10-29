// Package testutil provides fake restic, doveadm, mysqldump and inventory
// commands for tests.
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
# Skip the global flags. Every global flag the runner passes takes a value
# (-o, --limit-upload, --retry-lock); a boolean one would need its own case.
while [ "${1#-}" != "$1" ]; do shift 2; done
if [ -n "$FAKE_HANG" ]; then
  # Stand-in for a long restic run; restic removes its own lock on SIGTERM.
  trap 'kill $! 2>/dev/null; echo "got SIGTERM" >> "$FAKE_LOG"; exit 130' TERM
  sleep 30 >/dev/null 2>&1 &
  wait
  exit 0
fi
case "$1" in
  cat) exit "${FAKE_CAT_EXIT:-0}" ;;
  backup)
    case " $* " in
      *" --stdin-from-command "*)
        # Like restic 0.19: run the command after --, store its stdout, and
        # fail without a snapshot when it exits non-zero.
        while [ $# -gt 0 ] && [ "$1" != "--" ]; do shift; done
        shift
        "$@" >> "$FAKE_LOG.stdin"
        rc=$?
        # Mid-run: no dump of this test may exist as a file. FAKE_NO_SQL_UNDER
        # lists the directories to search; the fake mysqldump writes the
        # test's FAKE_TOKEN into every dump.
        for d in $FAKE_NO_SQL_UNDER; do
          find "$d" -xdev -name '*.sql' -type f -exec grep -lF "$FAKE_TOKEN" {} + 2>/dev/null | sed 's/^/SQL FILE ON DISK: /' >> "$FAKE_LOG"
        done
        if [ "$rc" -ne 0 ]; then
          echo "Fatal: unable to save snapshot: command failed: exit status $rc" >&2
          exit 1
        fi
        cat "$FAKE_BACKUP_OUT"; exit 0 ;;
    esac
    # Log each regular file given as a path, with its first line, so that
    # tests can see a file existed while it was backed up.
    for a in "$@"; do [ -f "$a" ] && printf '%s: %s\n' "$a" "$(head -n 1 "$a")" >> "$FAKE_LOG.seen"; done
    cat "$FAKE_BACKUP_OUT"; exit "${FAKE_BACKUP_EXIT:-0}" ;;
  stats)
    if [ -n "$FAKE_STATS_EXIT" ]; then echo "stats failed" >&2; exit "$FAKE_STATS_EXIT"; fi
    echo "{\"total_size\":${FAKE_STATS_SIZE:-4096},\"total_uncompressed_size\":8192,\"snapshots_count\":2}"
    exit 0 ;;
  snapshots) cat "$FAKE_SNAPSHOTS_OUT"; exit 0 ;;
  restore)
    shift
    while [ $# -gt 0 ]; do
      case "$1" in --target) t="$2"; shift ;; --include) i="$2"; shift ;; esac
      shift
    done
    [ -n "$FAKE_RESTORE_EMPTY" ] && exit 0
    # FAKE_RESTORE_MKDIR: a directory that appears while restic runs (the
    # customer creating the restore's target folder meanwhile).
    [ -n "$FAKE_RESTORE_MKDIR" ] && mkdir -p "$FAKE_RESTORE_MKDIR"
    if [ "$FAKE_RESTORE_KIND" = files ]; then
      # A webspace subtree: two files, one in a subdirectory.
      mkdir -p "$t$i/sub"
      printf 'hello\n' > "$t$i/index.html"
      printf 'deep\n' > "$t$i/sub/deep.txt"
      chmod 640 "$t$i/index.html"
      exit "${FAKE_RESTORE_EXIT:-0}"
    fi
    mkdir -p "$t$i/cur" "$t$i/.Sent/cur"
    printf 'Subject: one\n\nx\n' > "$t$i/cur/1.host:2,S"
    printf 'Subject: sent\n\nx\n' > "$t$i/.Sent/cur/2.host:2,S"
    exit "${FAKE_RESTORE_EXIT:-0}" ;;
  ls)
    # Like restic 0.19 ls --json: the snapshot, then one node per line. By
    # default the path is a directory with one file in it; FAKE_LS_OUT
    # replaces the nodes, FAKE_LS_EMPTY gives none (a path not in the
    # snapshot).
    for a in "$@"; do p="$a"; done
    echo '{"time":"2026-10-15T02:00:00Z","paths":["/x"],"id":"fedcba98","message_type":"snapshot","struct_type":"snapshot"}'
    [ -n "$FAKE_LS_EMPTY" ] && exit 0
    if [ -n "$FAKE_LS_OUT" ]; then cat "$FAKE_LS_OUT"; exit 0; fi
    echo "{\"name\":\"$(basename "$p")\",\"type\":\"dir\",\"path\":\"$p\",\"mtime\":\"2026-10-14T01:00:00Z\",\"message_type\":\"node\",\"struct_type\":\"node\"}"
    echo "{\"name\":\"index.html\",\"type\":\"file\",\"path\":\"${p%/}/index.html\",\"size\":6,\"mtime\":\"2026-10-14T01:00:00Z\",\"message_type\":\"node\",\"struct_type\":\"node\"}"
    exit 0 ;;
  dump)
    # Prints a small dump of the requested file; with FAKE_DUMP_EXIT, a
    # partial one and then an error.
    for a in "$@"; do p="$a"; done
    printf -- '-- dump of %s\nCREATE TABLE t (id int);\n' "$p"
    if [ -n "$FAKE_DUMP_EXIT" ]; then echo "Fatal: cannot dump file: connection lost" >&2; exit "$FAKE_DUMP_EXIT"; fi
    exit 0 ;;
  unlock) exit "${FAKE_UNLOCK_EXIT:-0}" ;;
  forget|check|init) exit "${FAKE_OTHER_EXIT:-0}" ;;
esac
exit 0
`

// fakeMysqldump logs its arguments to $FAKE_LOG.mysqldump, prints a small
// dump of the database named last (with FAKE_TOKEN in it), and exits 2 when that database is
// listed in FAKE_MYSQLDUMP_FAIL (space-separated).
const fakeMysqldump = `#!/bin/sh
echo "$*" >> "$FAKE_LOG.mysqldump"
for a in "$@"; do db="$a"; done
case " $FAKE_MYSQLDUMP_FAIL " in
  *" $db "*) echo "mysqldump: Got error: 1049: \"Unknown database '$db'\" when selecting the database" >&2; exit 2 ;;
esac
printf -- '-- fake dump of %s (%s)\nCREATE TABLE t (id int);\nINSERT INTO t VALUES (1);\n' "$db" "$FAKE_TOKEN"
`

const fakeDoveadm = `#!/bin/sh
echo "$*" >> "$FAKE_LOG"
case "$*" in
  "mailbox list"*) cat "$FAKE_LIST" 2>/dev/null; exit 0 ;;
  "import"*) for a in "$@"; do case "$a" in maildir:*) p="${a#maildir:}"; p="${p%%:*}"; ls -R "$p" >> "$FAKE_LOG";; esac; done
             if [ -n "$FAKE_IMPORT_SLEEP" ]; then
               trap 'kill $! 2>/dev/null; echo "import got SIGTERM" >> "$FAKE_LOG"; exit 130' TERM
               sleep "$FAKE_IMPORT_SLEEP" >/dev/null 2>&1 &
               wait
             fi
             [ -n "$FAKE_IMPORT_STDERR_ERROR" ] && echo "doveadm(alice@example.test)<1><x>: Error: opendir($p) failed: Permission denied" >&2
             [ -n "$FAKE_IMPORT_STDERR_OTHER" ] && echo "doveadm(alice@example.test)<1><x>: Error: Mailbox INBOX: Deleting corrupted cache: File too small" >&2
             exit "${FAKE_IMPORT_EXIT:-0}" ;;
  "mailbox delete"*) [ -n "$FAKE_DELETE_EXIT" ] && echo "doveadm(alice@example.test): Error: Can't delete mailbox: Mailbox doesn't exist" >&2
                     exit "${FAKE_DELETE_EXIT:-0}" ;;
  "-f flow mailbox status"*)
    # Real Dovecot 2.3 prints the mailbox name, then key=value pairs (no mailbox= key).
    [ -n "$FAKE_STATUS_EMPTY" ] && exit 0
    for a in "$@"; do t="${a%\*}"; done
    echo "$t.INBOX messages=1"; echo "$t.Sent messages=1"
    [ -n "$FAKE_STATUS_LONG" ] && head -c 70000 /dev/zero | tr '\0' x && echo
    exit 0 ;;
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
		"FAKE_TOKEN":         "relical-test-" + dir, // unique per test
		"FAKE_LOG":           log,
		"FAKE_BACKUP_OUT":    filepath.Join(dir, "backup.out"),
		"FAKE_SNAPSHOTS_OUT": filepath.Join(dir, "snapshots.out"),
	}
	os.WriteFile(env["FAKE_BACKUP_OUT"], []byte(
		`{"message_type":"status","percent_done":0.5}`+"\n"+
			`{"message_type":"summary","files_new":3,"data_added":2048,"data_added_packed":1024,"total_duration":1.5,"snapshot_id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}`+"\n"), 0o600)
	os.WriteFile(env["FAKE_SNAPSHOTS_OUT"], []byte(
		`[{"id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","time":"2026-10-14T02:00:00Z","paths":["/m"],"hostname":"h","tags":["mail"]},`+
			`{"id":"fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210","time":"2026-10-15T02:00:00Z","paths":["/m"],"hostname":"h","tags":["mail"]}]`), 0o600)
	read := func() string { b, _ := os.ReadFile(log); return string(b) }
	return write(t, "restic", fakeRestic), env, read
}

// FakeMysqldump returns a fake mysqldump; see fakeMysqldump. Its argument
// log is env["FAKE_LOG"]+".mysqldump", and what restic stored from its
// stdout is env["FAKE_LOG"]+".stdin".
func FakeMysqldump(t *testing.T) string {
	t.Helper()
	return write(t, "mysqldump", fakeMysqldump)
}

// FakeDoveadm returns a fake doveadm that logs to env["FAKE_LOG"].
func FakeDoveadm(t *testing.T) string {
	t.Helper()
	return write(t, "doveadm", fakeDoveadm)
}

// FakeInventory returns an inventory_command that prints out and exits 0.
func FakeInventory(t *testing.T, out string) string {
	t.Helper()
	data := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(data, []byte(out), 0o600); err != nil {
		t.Fatal(err)
	}
	return write(t, "inventory", "#!/bin/sh\nexec cat '"+data+"'\n")
}

// FakeInventoryFailing returns an inventory_command that prints a partial
// inventory and an error on stderr, then exits 1.
func FakeInventoryFailing(t *testing.T) string {
	t.Helper()
	return write(t, "inventory", "#!/bin/sh\necho '{\"version\": 2, \"subscriptions\": ['\necho 'panel database unreachable' >&2\nexit 1\n")
}

// FakeCommand returns an executable shell script with the given body.
func FakeCommand(t *testing.T, name, body string) string {
	t.Helper()
	return write(t, name, "#!/bin/sh\n"+body+"\n")
}

// FakeInventoryScript returns an inventory_command with the given shell body.
func FakeInventoryScript(t *testing.T, body string) string {
	t.Helper()
	return write(t, "inventory", "#!/bin/sh\n"+body+"\n")
}

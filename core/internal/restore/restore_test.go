package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/locator"
	"github.com/wadejbeckett/relical/core/internal/testutil"
)

func setup(t *testing.T) (job.Env, map[string]string, func() string) {
	t.Helper()
	root := t.TempDir()
	mail := filepath.Join(root, "mail")
	os.MkdirAll(filepath.Join(mail, "example.test", "alice", "Maildir", "cur"), 0o700)
	keysDir := filepath.Join(root, "keys")
	os.Mkdir(keysDir, 0o700)
	os.WriteFile(filepath.Join(keysDir, "example.test.key"), []byte("k\n"), 0o600)
	bin, env, log := testutil.FakeRestic(t)
	cfg := &config.Config{
		ServerID: "h", ResticBin: bin, DoveadmBin: testutil.FakeDoveadm(t),
		StateDir: filepath.Join(root, "state"), KeysDir: keysDir,
		MailboxPattern:     mail + "/{domain}/{mailbox}/Maildir",
		DefaultDestination: "d", Destinations: map[string]config.Destination{"d": {Repository: "/repo/{subscription}", Prune: "local"}},
	}
	inv := inventory.Inventory{Version: 2, Subscriptions: []inventory.Subscription{
		{Name: "example.test", SysUser: "exampleuser", UID: 10001, GID: 1004, Home: "/var/www/vhosts/example.test",
			Features: []string{"mail"}, MailDomains: []string{"example.test"}},
		{Name: "other.example.test", SysUser: "otheruser", UID: 10002, GID: 1004, Home: "/var/www/vhosts/other.example.test",
			Features: []string{"mail"}, MailDomains: []string{"other.example.test"}},
	}}
	e := job.Env{Cfg: cfg, Inv: inv, Locator: locator.Pattern(cfg.MailboxPattern), Now: time.Now, ExtraEnv: env}
	if err := e.Prepare(); err != nil {
		t.Fatal(err)
	}
	return e, env, log
}

func TestRestoreLatestWholeMailbox(t *testing.T) {
	e, _, log := setup(t)
	res, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RestoredTo != "Restored 2026-10-15 fedcba98" || res.Messages != 2 {
		t.Errorf("%+v", res)
	}
	l := log()
	if !strings.Contains(l, "restore fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210 --target") {
		t.Errorf("restore args:\n%s", l)
	}
	if !strings.Contains(l, "import -s -u alice@example.test maildir:") || !strings.Contains(l, "/Maildir Restored 2026-10-15 fedcba98 all") || strings.Contains(l, "INDEX=") {
		t.Errorf("import args:\n%s", l)
	}
	if !strings.Contains(l, "1.host:2,S") {
		t.Errorf("staging was empty when doveadm ran:\n%s", l)
	}
	entries, _ := os.ReadDir(filepath.Join(e.Cfg.StateDir, "restore"))
	if len(entries) != 0 {
		t.Errorf("staging not cleaned up: %v", entries)
	}
}

func TestRestoreOneFolderByShortID(t *testing.T) {
	e, _, log := setup(t)
	res, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "01234567", Folder: "Sent"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RestoredTo != "Restored 2026-10-14 01234567" {
		t.Errorf("%+v", res)
	}
	if !strings.Contains(log(), "Restored 2026-10-14 01234567 mailbox Sent") {
		t.Errorf("%s", log())
	}
}

func TestRestoreRefusesExistingTarget(t *testing.T) {
	e, env, log := setup(t)
	list := filepath.Join(t.TempDir(), "list")
	os.WriteFile(list, []byte("INBOX\nRestored 2026-10-15 fedcba98\nRestored 2026-10-15 fedcba98/Sent\n"), 0o600)
	env["FAKE_LIST"] = list
	_, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(log(), "restore ") {
		t.Error("ran restic restore although the target exists")
	}
}

func TestRestoreCleansUpWhenImportFails(t *testing.T) {
	e, env, log := setup(t)
	env["FAKE_IMPORT_EXIT"] = "75"
	if _, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"}); err == nil {
		t.Fatal("import failure not reported")
	}
	if !strings.Contains(log(), "mailbox delete -r -s -u alice@example.test Restored 2026-10-15 fedcba98") {
		t.Errorf("partial folder not removed:\n%s", log())
	}
	entries, _ := os.ReadDir(filepath.Join(e.Cfg.StateDir, "restore"))
	if len(entries) != 0 {
		t.Errorf("staging left behind: %v", entries)
	}
}

func TestRestoreCleansUpWhenContextCancelledDuringImport(t *testing.T) {
	e, env, log := setup(t)
	env["FAKE_IMPORT_SLEEP"] = "2"
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"}); err == nil {
		t.Fatal("cancelled import not reported")
	}
	if !strings.Contains(log(), "mailbox delete -r -s -u alice@example.test") {
		t.Errorf("partial folder not removed after cancellation:\n%s", log())
	}
	if !strings.Contains(log(), "import got SIGTERM") {
		t.Errorf("doveadm import was not sent SIGTERM on cancellation:\n%s", log())
	}
}

func TestRestoreRejectsBadInput(t *testing.T) {
	e, _, _ := setup(t)
	for _, r := range []Request{
		{Domain: "example.test", Mailbox: "alice", Snapshot: "nothex!"},
		{Domain: "example.test", Mailbox: "alice", Snapshot: "abc"},
		{Domain: "example.test", Mailbox: "../alice", Snapshot: "latest"},
		{Domain: "example.test", Mailbox: "alice", Snapshot: "99999999"},
		{Domain: "example.test", Mailbox: "alice", Snapshot: "latest", Folder: "bad\nname"},
	} {
		if _, err := Run(context.Background(), e, r); err == nil {
			t.Errorf("%+v accepted", r)
		}
	}
}

func stagingEntries(e job.Env) []os.DirEntry {
	entries, _ := os.ReadDir(filepath.Join(e.Cfg.StateDir, "restore"))
	return entries
}

func TestRestoreFailsWhenDoveadmLogsErrorDespiteExitZero(t *testing.T) {
	e, env, _ := setup(t)
	env["FAKE_IMPORT_STDERR_ERROR"] = "1"
	_, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "Permission denied") || !strings.Contains(err.Error(), "import") {
		t.Fatalf("err = %v", err)
	}
	if n := stagingEntries(e); len(n) != 0 {
		t.Errorf("staging left behind: %v", n)
	}
}

func TestRestoreFailsWhenNoRestoredFolderExists(t *testing.T) {
	e, env, _ := setup(t)
	env["FAKE_STATUS_EMPTY"] = "1"
	_, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "no \"Restored 2026-10-15 fedcba98\" folder exists") {
		t.Fatalf("err = %v", err)
	}
	if n := stagingEntries(e); len(n) != 0 {
		t.Errorf("staging left behind: %v", n)
	}
}

func TestCleanupTreatsMissingMailboxAsClean(t *testing.T) {
	e, env, _ := setup(t)
	env["FAKE_IMPORT_EXIT"] = "75"
	env["FAKE_DELETE_EXIT"] = "68"
	_, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err == nil || strings.Contains(err.Error(), "also failed") {
		t.Fatalf("err = %v", err)
	}
	env["FAKE_DELETE_EXIT"] = "75"
	_, err = Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "also failed") {
		t.Fatalf("other delete failures must still be reported: %v", err)
	}
}

func TestRestoreIgnoresUnrelatedDoveadmErrorLines(t *testing.T) {
	e, env, _ := setup(t)
	env["FAKE_IMPORT_STDERR_OTHER"] = "1"
	res, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err != nil || res.Messages != 2 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestErrorLinesMentioning(t *testing.T) {
	stderr := []byte("doveadm(alice@example.test)<3380><abc>: Error: opendir(/srv/src) failed: Permission denied\nError: bare /srv/src\nInfo: Error: /srv/src mid-line\nError: other\nnoise /srv/src\n")
	got := errorLinesMentioning(stderr, "/srv/src")
	if len(got) != 3 || !strings.HasPrefix(got[0], "doveadm(") || got[1] != "Error: bare /srv/src" {
		t.Errorf("%q", got)
	}
}

func TestRestoreFailsWhenMailboxIsNotInSnapshot(t *testing.T) {
	e, env, log := setup(t)
	env["FAKE_RESTORE_EMPTY"] = "1"
	_, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err == nil || err.Error() != "mailbox alice@example.test is not in snapshot fedcba98" {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(log(), "import ") {
		t.Errorf("ran doveadm import with nothing restored:\n%s", log())
	}
}

func TestRestoreFailsClosedWhenFolderListUnreadable(t *testing.T) {
	e, env, log := setup(t)
	list := filepath.Join(t.TempDir(), "list")
	os.WriteFile(list, []byte(strings.Repeat("x", 70000)+"\nRestored 2026-10-15 fedcba98\n"), 0o600)
	env["FAKE_LIST"] = list
	if _, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"}); err == nil {
		t.Fatal("an unreadable folder list was treated as \"target does not exist\"")
	}
	if strings.Contains(log(), "restore ") {
		t.Error("ran restic restore although the folder list could not be read")
	}
}

func TestRestoreFailsClosedWhenStatusUnreadable(t *testing.T) {
	e, env, _ := setup(t)
	env["FAKE_STATUS_LONG"] = "1"
	if _, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"}); err == nil {
		t.Fatal("an unreadable mailbox status was accepted")
	}
}

func TestRestoreUsesTheSubscriptionRepository(t *testing.T) {
	e, env, log := setup(t)
	res, err := Run(context.Background(), e, Request{Subscription: "example.test", Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err != nil || res.Subscription != "example.test" || res.Unit != "mail" {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(log(), "snapshots --json --tag mail") {
		t.Errorf("%s", log())
	}
	b, _ := os.ReadFile(env["FAKE_LOG"] + ".env")
	if !strings.Contains(string(b), "RESTIC_REPOSITORY=/repo/example.test") {
		t.Errorf("%s", b)
	}
}

func TestRestoreInfersSubscriptionFromMailDomain(t *testing.T) {
	e, _, _ := setup(t)
	res, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err != nil || res.Subscription != "example.test" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRestoreRefusesDomainOfAnotherSubscription(t *testing.T) {
	e, _, log := setup(t)
	for _, req := range []Request{
		{Subscription: "other.example.test", Domain: "example.test", Mailbox: "alice", Snapshot: "latest"},
		{Subscription: "nope.example.test", Domain: "example.test", Mailbox: "alice", Snapshot: "latest"},
		{Domain: "nope.example.test", Mailbox: "alice", Snapshot: "latest"},
	} {
		if _, err := Run(context.Background(), e, req); err == nil {
			t.Errorf("%+v accepted", req)
		}
	}
	if log() != "" {
		t.Errorf("restic or doveadm ran:\n%s", log())
	}
}

func TestRestoreFromAnotherIdsRepositoryIsRefused(t *testing.T) {
	e, _, log := setup(t)
	os.WriteFile(filepath.Join(e.Cfg.KeysDir, "example.test.id"), []byte("17\n"), 0o600)
	e.Inv.Subscriptions[0].ID = "42"
	_, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), `id "17"`) || !strings.Contains(err.Error(), `id "42"`) {
		t.Fatalf("err = %v", err)
	}
	if log() != "" {
		t.Errorf("restic or doveadm ran:\n%s", log())
	}
}

func TestRestoreFolderTakesTheNamespacePrefix(t *testing.T) {
	e, _, log := setup(t)
	e.Cfg.MailFolderPrefix = "INBOX."
	res, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	if res.RestoredTo != "INBOX.Restored 2026-10-15 fedcba98" {
		t.Errorf("%+v", res)
	}
	if l := log(); !strings.Contains(l, "/Maildir INBOX.Restored 2026-10-15 fedcba98 all") {
		t.Errorf("import args:\n%s", l)
	}
}

func TestSourceFolderDropsTheLivePrefix(t *testing.T) {
	for _, c := range []struct{ folder, prefix, want string }{
		{"INBOX.Sent", "INBOX.", "Sent"},
		{"Sent", "INBOX.", "Sent"},
		{"INBOX", "INBOX.", "INBOX"},
		{"INBOX.", "INBOX.", "INBOX."},
		{"INBOX.Archive.2025", "INBOX.", "Archive.2025"},
		{"INBOX.Sent", "", "INBOX.Sent"},
	} {
		if got := sourceFolder(c.folder, c.prefix); got != c.want {
			t.Errorf("sourceFolder(%q, %q) = %q, want %q", c.folder, c.prefix, got, c.want)
		}
	}
}

// TestMailStageChownNeverLeavesTheStage swaps a mail folder that the walk
// has not reached yet for a symlink to a directory outside the stage, as
// the mail user could once a directory is theirs. No owner change may
// land outside the stage, and the stage root must be changed last.
func TestMailStageChownNeverLeavesTheStage(t *testing.T) {
	stage := filepath.Join(t.TempDir(), "run-1")
	m := filepath.Join(stage, "mail", "example.test", "alice", "Maildir")
	for _, d := range []string{"cur", ".Sent/cur", ".ZZZ/cur", ".AAA/cur"} {
		os.MkdirAll(filepath.Join(m, d), 0o700)
	}
	for _, f := range []string{"cur/1", ".Sent/cur/2", ".ZZZ/cur/3", ".AAA/cur/4"} {
		os.WriteFile(filepath.Join(m, f), []byte("x"), 0o600)
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "passwd"), []byte("root"), 0o644)

	var calls []string
	swapped := ""
	record := func(p string) {
		calls = append(calls, p)
		if swapped != "" {
			return
		}
		// Swap a folder the walk has not reached yet.
		for _, d := range []string{".ZZZ", ".AAA", ".Sent"} {
			if !strings.HasPrefix(p, filepath.Join(m, d)) {
				swapped = filepath.Join(m, d)
				os.Rename(swapped, swapped+".moved")
				os.Symlink(outside, swapped)
				return
			}
		}
	}
	fdPath := func(fd int) string { p, _ := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd)); return p }
	oldAt, oldF := fchownat, fchown
	fchownat = func(dirfd int, name string, uid, gid, flags int) error {
		if flags != unix_AT_SYMLINK_NOFOLLOW {
			t.Errorf("fchownat %s without AT_SYMLINK_NOFOLLOW", name)
		}
		record(filepath.Join(fdPath(dirfd), name))
		return nil
	}
	fchown = func(fd, uid, gid int) error { record(fdPath(fd)); return nil }
	t.Cleanup(func() { fchownat, fchown = oldAt, oldF })

	if err := chownStage(stage, 2000, 2000); err != nil {
		t.Fatal(err)
	}
	if swapped == "" {
		t.Fatal("no folder was swapped")
	}
	sawLink := false
	for _, c := range calls {
		if c != stage && !strings.HasPrefix(c, stage+"/") {
			t.Errorf("chown outside the stage: %s", c)
		}
		sawLink = sawLink || c == swapped
	}
	if !sawLink {
		t.Errorf("the symlink %s itself was not changed: %q", swapped, calls)
	}
	if calls[len(calls)-1] != stage {
		t.Errorf("the stage root was not changed last: %q", calls)
	}
	// Children before parents.
	seen := map[string]bool{}
	for _, c := range calls {
		for d := filepath.Dir(c); strings.HasPrefix(d, stage); d = filepath.Dir(d) {
			if seen[d] {
				t.Errorf("%s changed before its child %s", d, c)
			}
		}
		seen[c] = true
	}
}

// fakeFileSystems puts each directory on its own file system (dev) with
// free bytes free, or with plenty when it is not listed.
func fakeFileSystems(t *testing.T, devs map[string]uint64, free map[string]uint64) {
	t.Helper()
	oldStat, oldStatfs := stat, statfs
	devOf := func(p string) uint64 {
		for d, n := range devs {
			if p == d || strings.HasPrefix(p, d+"/") {
				return n
			}
		}
		return 99
	}
	stat = func(p string, st *syscall.Stat_t) error { st.Dev = devOf(p); return nil }
	statfs = func(p string, st *syscall.Statfs_t) error {
		st.Bsize, st.Frsize, st.Bavail = 1, 1, 1<<50
		for d, n := range devs {
			if devOf(p) == n {
				if f, ok := free[d]; ok {
					st.Bavail = f
				}
			}
		}
		return nil
	}
	t.Cleanup(func() { stat, statfs = oldStat, oldStatfs })
}

// TestMailRestoreRefusesWithoutFreeSpace: a mail restore needs room for
// the mailbox in the staging file system (<state_dir>/restore) and in the
// mailbox's, twice when they are one file system; without it, nothing is
// restored or imported.
func TestMailRestoreRefusesWithoutFreeSpace(t *testing.T) {
	const mib = 1 << 20
	for name, c := range map[string]struct {
		sameFS     bool
		stageFree  uint64
		mailFree   uint64
		wantRefuse string
	}{
		"staging full":                  {stageFree: 100 * mib, mailFree: 1 << 40, wantRefuse: "restore"},
		"mailbox file system full":      {stageFree: 1 << 40, mailFree: 100 * mib, wantRefuse: "mail"},
		"one file system, room for two": {sameFS: true, stageFree: MinSpaceReserve + 12},
		"one file system, room for one": {sameFS: true, stageFree: MinSpaceReserve + 7, wantRefuse: "restore"},
		"room on both":                  {stageFree: 1 << 40, mailFree: 1 << 40},
	} {
		t.Run(name, func(t *testing.T) {
			e, _, log := setup(t)
			stage := filepath.Join(e.Cfg.StateDir, "restore")
			mail := filepath.Dir(filepath.Dir(filepath.Dir(string(e.Locator))))
			devs := map[string]uint64{stage: 1, mail: 2}
			if c.sameFS {
				devs[mail] = 1
				c.mailFree = c.stageFree
			}
			fakeFileSystems(t, devs, map[string]uint64{stage: c.stageFree, mail: c.mailFree})
			// The fake ls lists one 6-byte file under the mailbox.
			_, err := Run(context.Background(), e, Request{Domain: "example.test", Mailbox: "alice", Snapshot: "latest"})
			l := log()
			if c.wantRefuse == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			want := "not enough free space for this restore in " + map[string]string{"restore": stage, "mail": filepath.Join(mail, "example.test", "alice", "Maildir")}[c.wantRefuse]
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want %q", err, want)
			}
			if !strings.Contains(l, "ls --json --recursive fedcba98") {
				t.Errorf("no size estimate:\n%s", l)
			}
			if strings.Contains(l, "restore fedcba98") || strings.Contains(l, "import ") {
				t.Errorf("restored or imported:\n%s", l)
			}
		})
	}
}

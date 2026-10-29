package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

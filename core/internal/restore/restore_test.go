package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
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
		MailboxPattern: mail + "/{domain}/{mailbox}/Maildir", Domains: []string{"example.test"},
		DefaultDestination: "d", Destinations: map[string]config.Destination{"d": {Repository: "/repo/{domain}", Prune: "local"}},
	}
	e := job.Env{Cfg: cfg, Locator: locator.Pattern(cfg.MailboxPattern), Now: time.Now, ExtraEnv: env}
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
	if !strings.Contains(l, "import -s -u alice@example.test maildir:") || !strings.Contains(l, ":INDEX=MEMORY Restored 2026-10-15 fedcba98 all") {
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

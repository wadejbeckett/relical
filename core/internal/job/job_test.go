package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/locator"
	"github.com/wadejbeckett/relical/core/internal/testutil"
)

func setup(t *testing.T, prune string) (Env, map[string]string, func() string) {
	t.Helper()
	root := t.TempDir()
	mail := filepath.Join(root, "mail")
	for _, b := range []string{"alice", "bob"} {
		os.MkdirAll(filepath.Join(mail, "example.test", b, "Maildir", "cur"), 0o700)
	}
	keysDir := filepath.Join(root, "keys")
	os.Mkdir(keysDir, 0o700)
	bin, env, log := testutil.FakeRestic(t)
	cfg := &config.Config{
		ServerID: "host.example.test", ResticBin: bin, StateDir: filepath.Join(root, "state"), KeysDir: keysDir,
		MailboxPattern: mail + "/{domain}/{mailbox}/Maildir", Domains: []string{"example.test"},
		DefaultDestination: "d",
		Destinations:       map[string]config.Destination{"d": {Repository: "/repo/{domain}", Prune: prune}},
		Retention:          config.Retention{Daily: 14, Weekly: 8, Monthly: 12},
	}
	e := Env{Cfg: cfg, Locator: locator.Pattern(cfg.MailboxPattern), Now: func() time.Time { return time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC) }, ExtraEnv: env}
	if err := e.Prepare(); err != nil {
		t.Fatal(err)
	}
	return e, env, log
}

func TestBackupInitsNewRepoAndExcludesTmp(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_CAT_EXIT"] = "10"
	r := e.Backup(context.Background(), "example.test")
	if !r.OK || r.Snapshot == "" || r.Mailboxes != 2 {
		t.Fatalf("%+v", r)
	}
	l := log()
	if !strings.Contains(l, "init --repository-version 2") {
		t.Errorf("no init:\n%s", l)
	}
	alice := filepath.Join(filepath.Dir(e.Cfg.StateDir), "mail/example.test/alice/Maildir")
	for _, w := range []string{"--tag backup", "--exclude " + alice + "/tmp/*", "--exclude " + alice + "/.*/tmp/*", "--exclude **/dovecot.index*"} {
		if !strings.Contains(l, w) {
			t.Errorf("missing %q in\n%s", w, l)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.KeysDir, "example.test.key")); err != nil {
		t.Error("key not created")
	}
}

func TestBackupWrongPasswordDoesNotInit(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_CAT_EXIT"] = "12"
	r := e.Backup(context.Background(), "example.test")
	if r.OK || !strings.Contains(r.Error, "exit 12") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(log(), "init") || strings.Contains(log(), "backup --json") {
		t.Fatalf("ran init or backup after a wrong password:\n%s", log())
	}
}

func TestBackupPartialIsOKWithNote(t *testing.T) {
	e, env, _ := setup(t, "local")
	env["FAKE_BACKUP_EXIT"] = "3"
	r := e.Backup(context.Background(), "example.test")
	if !r.OK || r.Snapshot == "" || r.Note == "" {
		t.Fatalf("%+v", r)
	}
}

func TestBackupDisabledDomainNeverCallsRestic(t *testing.T) {
	e, _, log := setup(t, "local")
	r := e.Backup(context.Background(), "other.example.test")
	if r.OK || log() != "" {
		t.Fatalf("%+v\n%s", r, log())
	}
}

func TestForgetRemoteSkips(t *testing.T) {
	e, _, log := setup(t, "remote")
	keysEnsure(t, e)
	r := e.Forget(context.Background(), "example.test")
	if !r.OK || !strings.Contains(r.Note, "remote") || log() != "" {
		t.Fatalf("%+v\n%s", r, log())
	}
}

func TestForgetLocalRunsPrune(t *testing.T) {
	e, _, log := setup(t, "local")
	keysEnsure(t, e)
	r := e.Forget(context.Background(), "example.test")
	if !r.OK || !strings.Contains(log(), "--keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune") {
		t.Fatalf("%+v\n%s", r, log())
	}
}

func TestCheckWithoutKeyFails(t *testing.T) {
	e, _, _ := setup(t, "local")
	if r := e.Check(context.Background(), "example.test", "5%"); r.OK {
		t.Fatalf("%+v", r)
	}
}

func TestLockIsExclusive(t *testing.T) {
	e, _, _ := setup(t, "local")
	unlock, err := Lock(e.Cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(e.Cfg.StateDir); err == nil {
		t.Fatal("second lock succeeded")
	}
	unlock()
	u2, err := Lock(e.Cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	u2()
}

func TestRecordMergesState(t *testing.T) {
	e, _, _ := setup(t, "local")
	e.Record([]Result{{Domain: "example.test", Action: "backup", OK: true, Snapshot: "a"}})
	e.Record([]Result{{Domain: "example.test", Action: "check", OK: true}})
	st, err := ReadState(e.Cfg.StateDir)
	if err != nil || st["example.test"]["backup"].Snapshot != "a" || !st["example.test"]["check"].OK {
		t.Fatalf("%+v %v", st, err)
	}
	fi, _ := os.Stat(filepath.Join(e.Cfg.StateDir, "state.json"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("state mode %v", fi.Mode().Perm())
	}
}

func keysEnsure(t *testing.T, e Env) {
	t.Helper()
	p := filepath.Join(e.Cfg.KeysDir, "example.test.key")
	if err := os.WriteFile(p, []byte("k\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

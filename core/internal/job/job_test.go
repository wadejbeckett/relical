package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
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
		MailboxPattern:     mail + "/{domain}/{mailbox}/Maildir",
		DefaultDestination: "d",
		Destinations: map[string]config.Destination{
			"d":     {Repository: "/repo/{subscription}", Prune: prune},
			"other": {Repository: "/other/{subscription}", Prune: prune},
		},
		Retention: config.Retention{Daily: 14, Weekly: 8, Monthly: 12},
	}
	inv := inventory.Inventory{Version: 2, Subscriptions: []inventory.Subscription{{
		Name: "example.test", SysUser: "exampleuser", UID: 10001, GID: 1004, Home: "/var/www/vhosts/example.test",
		Features: []string{"mail"}, MailDomains: []string{"example.test", "shop.example.test"},
	}}}
	e := Env{Cfg: cfg, Inv: inv, Locator: locator.Pattern(cfg.MailboxPattern), Now: func() time.Time { return time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC) }, ExtraEnv: env}
	if err := e.Prepare(); err != nil {
		t.Fatal(err)
	}
	return e, env, log
}

func TestBackupInitsNewRepoAndExcludesTmp(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_CAT_EXIT"] = "10"
	r := e.Backup(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || r.Snapshot == "" || r.Mailboxes != 2 {
		t.Fatalf("%+v", r)
	}
	l := log()
	if !strings.Contains(l, "init --repository-version 2") {
		t.Errorf("no init:\n%s", l)
	}
	alice := filepath.Join(filepath.Dir(e.Cfg.StateDir), "mail/example.test/alice/Maildir")
	for _, w := range []string{"--tag mail", "--exclude " + alice + "/tmp/*", "--exclude " + alice + "/.*/tmp/*", "--exclude **/dovecot.index*"} {
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
	r := e.Backup(context.Background(), mustSub(t, e, "example.test"))
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
	r := e.Backup(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || r.Snapshot == "" || r.Note == "" || !strings.Contains(r.Note, "exit 3") {
		t.Fatalf("%+v", r)
	}
}

func TestBackupPartialWithoutSummaryFails(t *testing.T) {
	e, env, _ := setup(t, "local")
	env["FAKE_BACKUP_EXIT"] = "3"
	if err := os.WriteFile(env["FAKE_BACKUP_OUT"], []byte(`{"message_type":"status"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.Backup(context.Background(), mustSub(t, e, "example.test"))
	if r.OK || r.Snapshot != "" || r.Error == "" {
		t.Fatalf("%+v", r)
	}
}

// repoEnv returns the RESTIC_* variables the fake restic saw.
func repoEnv(env map[string]string) string {
	b, _ := os.ReadFile(env["FAKE_LOG"] + ".env")
	return string(b)
}

func mustSub(t *testing.T, e Env, name string) inventory.Subscription {
	t.Helper()
	s, err := e.Subscription(name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSubscriptionNotInInventory(t *testing.T) {
	e, _, _ := setup(t, "local")
	if _, err := e.Subscription("other.example.test"); err == nil || !strings.Contains(err.Error(), "not in the inventory") {
		t.Fatalf("err = %v", err)
	}
	if s, err := e.Subscription("_server"); err != nil || s.Name != "_server" {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestBackupWithoutMailFeatureNeverCallsRestic(t *testing.T) {
	e, _, log := setup(t, "local")
	sub := mustSub(t, e, "example.test")
	sub.Features = []string{"files"}
	for _, r := range []Result{e.Backup(context.Background(), sub), e.Forget(context.Background(), sub), e.Check(context.Background(), sub, "")} {
		if !r.OK || r.Note == "" {
			t.Errorf("%+v", r)
		}
	}
	if log() != "" {
		t.Fatalf("restic ran:\n%s", log())
	}
}

func TestBackupCoversEveryMailDomain(t *testing.T) {
	e, env, log := setup(t, "local")
	shop := filepath.Join(filepath.Dir(e.Cfg.StateDir), "mail", "shop.example.test", "carol", "Maildir")
	os.MkdirAll(filepath.Join(shop, "cur"), 0o700)
	r := e.Backup(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || r.Mailboxes != 3 || r.Subscription != "example.test" || r.Unit != "mail" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(log(), shop) || !strings.Contains(repoEnv(env), "RESTIC_REPOSITORY=/repo/example.test") {
		t.Errorf("%s\n%s", log(), repoEnv(env))
	}
}

func TestBackupMissingMailDomainDirIsSkipped(t *testing.T) {
	e, _, _ := setup(t, "local")
	// shop.example.test has no mail directory in setup.
	if r := e.Backup(context.Background(), mustSub(t, e, "example.test")); !r.OK || r.Mailboxes != 2 {
		t.Fatalf("%+v", r)
	}
	sub := mustSub(t, e, "example.test")
	sub.MailDomains = []string{"shop.example.test"}
	if r := e.Backup(context.Background(), sub); r.OK || !strings.Contains(r.Error, "no mailboxes") {
		t.Fatalf("%+v", r)
	}
}

func TestDestinationOverrideAndRetention(t *testing.T) {
	e, env, log := setup(t, "local")
	keysEnsure(t, e)
	sub := mustSub(t, e, "example.test")
	sub.Destination = "other"
	sub.Retention = &config.Retention{Daily: 3}
	if name, d, err := e.DestinationFor(sub); err != nil || name != "other" || d.Repository != "/other/example.test" {
		t.Fatalf("%s %+v %v", name, d, err)
	}
	if r := e.Forget(context.Background(), sub); !r.OK {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(repoEnv(env), "RESTIC_REPOSITORY=/other/example.test") || !strings.Contains(log(), "forget --tag mail --group-by host,tags --keep-daily 3 --keep-weekly 0 --keep-monthly 0") {
		t.Errorf("%s", log())
	}
}

func TestForgetRemoteSkips(t *testing.T) {
	e, _, log := setup(t, "remote")
	keysEnsure(t, e)
	r := e.Forget(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || !strings.Contains(r.Note, "remote") || log() != "" {
		t.Fatalf("%+v\n%s", r, log())
	}
}

func TestForgetLocalRunsPrune(t *testing.T) {
	e, _, log := setup(t, "local")
	keysEnsure(t, e)
	r := e.Forget(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || !strings.Contains(log(), "--keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune") {
		t.Fatalf("%+v\n%s", r, log())
	}
}

func TestCheckWithoutKeyFails(t *testing.T) {
	e, _, _ := setup(t, "local")
	if r := e.Check(context.Background(), mustSub(t, e, "example.test"), "5%"); r.OK {
		t.Fatalf("%+v", r)
	}
}

// logLines returns the restic invocations in order, without global flags.
func logLines(log string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
		f := strings.Fields(l)
		for len(f) > 1 && strings.HasPrefix(f[0], "--") {
			f = f[2:]
		}
		out = append(out, strings.Join(f, " "))
	}
	return out
}

func TestForgetUnlocksStaleLocksFirst(t *testing.T) {
	e, _, log := setup(t, "local")
	keysEnsure(t, e)
	if r := e.Forget(context.Background(), mustSub(t, e, "example.test")); !r.OK {
		t.Fatalf("%+v", r)
	}
	l := logLines(log())
	if len(l) != 2 || l[0] != "unlock" || !strings.HasPrefix(l[1], "forget ") || !strings.HasSuffix(l[1], " --prune") {
		t.Fatalf("want unlock then forget --prune, got %q", l)
	}
}

func TestCheckUnlocksStaleLocksFirst(t *testing.T) {
	e, _, log := setup(t, "local")
	keysEnsure(t, e)
	if r := e.Check(context.Background(), mustSub(t, e, "example.test"), "5%"); !r.OK {
		t.Fatalf("%+v", r)
	}
	l := logLines(log())
	if len(l) != 2 || l[0] != "unlock" || l[1] != "check --read-data-subset 5%" {
		t.Fatalf("want unlock then check, got %q", l)
	}
}

func TestUnlockFailureFailsForgetAndCheck(t *testing.T) {
	e, env, log := setup(t, "local")
	keysEnsure(t, e)
	env["FAKE_UNLOCK_EXIT"] = "1"
	for _, r := range []Result{e.Forget(context.Background(), mustSub(t, e, "example.test")), e.Check(context.Background(), mustSub(t, e, "example.test"), "")} {
		if r.OK || !strings.Contains(r.Error, "restic unlock: exit 1") {
			t.Errorf("%+v", r)
		}
	}
	if strings.Contains(log(), "forget") || strings.Contains(log(), "check") {
		t.Errorf("ran forget or check after unlock failed:\n%s", log())
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
	e.Record([]Result{{Subscription: "example.test", Action: "backup", OK: true, Snapshot: "a"}})
	e.Record([]Result{{Subscription: "example.test", Action: "check", OK: true}})
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

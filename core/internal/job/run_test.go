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
	defaults := filepath.Join(root, "mysql.cnf")
	if err := os.WriteFile(defaults, []byte("[client]\nuser=admin\npassword=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		ServerID: "host.example.test", ResticBin: bin, StateDir: filepath.Join(root, "state"), KeysDir: keysDir,
		MysqldumpBin: testutil.FakeMysqldump(t), MySQL: config.MySQL{DefaultsFile: defaults},
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
		Files: []string{"/var/www/vhosts/example.test"}, FileExcludes: inventory.DefaultFileExcludes,
		Databases: []inventory.Database{{Type: "mysql", Name: "example_wp"}},
	}}}
	e := Env{Cfg: cfg, Inv: inv, Locator: locator.Pattern(cfg.MailboxPattern), Now: func() time.Time { return time.Date(2026, 10, 15, 2, 0, 0, 0, time.UTC) }, ExtraEnv: env}
	if err := e.Prepare(); err != nil {
		t.Fatal(err)
	}
	return e, env, log
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

func TestSftpArgsReachRestic(t *testing.T) {
	e, _, log := setup(t, "local")
	e.Cfg.Destinations["box"] = config.Destination{Repository: "sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/{subscription}", Prune: "local", SftpArgs: "-p 23 -o StrictHostKeyChecking=yes"}
	sub := mustSub(t, e, "example.test")
	sub.Destination = "box"
	if r := e.BackupMail(context.Background(), sub); !r.OK {
		t.Fatalf("%+v", r)
	}
	for _, l := range strings.Split(strings.TrimSpace(log()), "\n") {
		if !strings.HasPrefix(l, `-o "sftp.args=-p 23 -o StrictHostKeyChecking=yes" `) {
			t.Errorf("restic call without sftp.args: %s", l)
		}
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
	if !strings.Contains(repoEnv(env), "RESTIC_REPOSITORY=/other/example.test") || !strings.Contains(log(), "forget --group-by tags --keep-daily 3 --keep-weekly 0 --keep-monthly 0 --prune") {
		t.Errorf("%s", log())
	}
}

func TestForgetRemoteSkips(t *testing.T) {
	e, _, log := setup(t, "remote")
	keysEnsure(t, e)
	sub := mustSub(t, e, "example.test")
	r := e.Forget(context.Background(), sub)
	want := "for the repository /repo/example.test: restic forget --group-by tags --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune"
	if !r.OK || !strings.Contains(r.Note, "prune: remote") || !strings.Contains(r.Note, want) || log() != "" {
		t.Fatalf("%+v\n%s", r, log())
	}
	// A subscription's own retention is the one reported.
	sub.Retention = &config.Retention{Daily: 3}
	if r := e.Forget(context.Background(), sub); !strings.Contains(r.Note, "--keep-daily 3 --keep-weekly 0 --keep-monthly 0 --prune") {
		t.Errorf("%+v", r)
	}
}

func TestForgetRemoteNoteRedactsCredentials(t *testing.T) {
	e, _, _ := setup(t, "remote")
	keysEnsure(t, e)
	sub := mustSub(t, e, "example.test")
	for repo, want := range map[string]string{
		"rest:https://u:secret@backup.example.test:8000/{subscription}/": "rest:https://***@backup.example.test:8000/example.test/",
		"s3:https://key:secret@s3.example.test/bucket/{subscription}":    "s3:https://***@s3.example.test/bucket/example.test",
		"rest:https://u:se%zzcret@backup.example.test/{subscription}":    "rest:https://***@backup.example.test/example.test",
		// A password a URL parser reads as a port and a fragment or path.
		"rest:https://u:123#secret@backup.example.test/{subscription}": "rest:https://***@backup.example.test/example.test",
		"rest:https://u:123/secret@backup.example.test/{subscription}": "rest:https://***@backup.example.test/example.test",
		"rest:https://u:1?secret@backup.example.test/{subscription}":   "rest:https://***@backup.example.test/example.test",
		// A token as the user name.
		"rest:https://secrettoken@backup.example.test/{subscription}": "rest:https://***@backup.example.test/example.test",
		"rest:https://backup.example.test/{subscription}":             "rest:https://backup.example.test/example.test",
		"sftp:u000000-sub1@u000000-sub1.your-storagebox.example:/relical/{subscription}":       "sftp:u000000-sub1@u000000-sub1.your-storagebox.example:/relical/example.test",
	} {
		e.Cfg.Destinations["d"] = config.Destination{Repository: repo, Prune: "remote"}
		r := e.Forget(context.Background(), sub)
		if !r.OK || strings.Contains(r.Note, "secret") || strings.Contains(r.Note, "cret") || !strings.Contains(r.Note, want) {
			t.Errorf("%s: %+v", repo, r)
		}
		if err := e.Record([]Result{r}); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(filepath.Join(e.Cfg.StateDir, "state.json")); strings.Contains(string(b), "secret") {
			t.Errorf("%s: state holds the password", repo)
		}
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

func TestForgetWithoutRepositoryIsANote(t *testing.T) {
	e, _, log := setup(t, "local")
	// A db-only subscription without databases never gets a repository.
	sub := mustSub(t, e, "example.test")
	sub.Features, sub.Databases = []string{"db"}, nil
	if rs := e.BackupSubscription(context.Background(), sub, ""); len(rs) != 1 || !rs[0].OK {
		t.Fatalf("%+v", rs)
	}
	for _, r := range []Result{e.Forget(context.Background(), sub), e.Check(context.Background(), sub, "5%")} {
		if !r.OK || r.Note != NoRepositoryNote || r.Error != "" {
			t.Errorf("%+v", r)
		}
	}
	if log() != "" {
		t.Errorf("restic ran:\n%s", log())
	}
	// An owner file of another id still fails, even without a key.
	sub.ID = "42"
	os.WriteFile(filepath.Join(e.Cfg.KeysDir, "example.test.id"), []byte("17\n"), 0o600)
	if r := e.Forget(context.Background(), sub); r.OK || !strings.Contains(r.Error, `id "17"`) {
		t.Errorf("owner mismatch: %+v", r)
	}
	// A matching owner file without a key: the repository was created and
	// its key is lost, which fails.
	os.WriteFile(filepath.Join(e.Cfg.KeysDir, "example.test.id"), []byte("42\n"), 0o600)
	for _, r := range []Result{e.Forget(context.Background(), sub), e.Check(context.Background(), sub, "")} {
		if r.OK || !strings.Contains(r.Error, "key for example.test is missing but its owner file exists") {
			t.Errorf("lost key: %+v", r)
		}
	}
	os.Remove(filepath.Join(e.Cfg.KeysDir, "example.test.id"))
	// A key with the wrong mode fails.
	keysEnsure(t, e)
	os.Chmod(filepath.Join(e.Cfg.KeysDir, "example.test.key"), 0o644)
	for _, r := range []Result{e.Forget(context.Background(), sub), e.Check(context.Background(), sub, "")} {
		if r.OK || !strings.Contains(r.Error, "readable by others") {
			t.Errorf("wrong-mode key: %+v", r)
		}
	}
	// A keys directory that cannot be used fails too.
	os.Chmod(filepath.Join(e.Cfg.KeysDir, "example.test.key"), 0o600)
	os.Chmod(e.Cfg.KeysDir, 0o755)
	if r := e.Check(context.Background(), sub, ""); r.OK || !strings.Contains(r.Error, "mode 0700") {
		t.Errorf("open keys dir: %+v", r)
	}
	if log() != "" {
		t.Errorf("restic ran:\n%s", log())
	}
}

// logLines returns the restic invocations in order, without global flags.
func logLines(log string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(log), "\n") {
		f := strings.Fields(l)
		for len(f) > 1 && strings.HasPrefix(f[0], "-") {
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
	e.Record([]Result{
		{Subscription: "example.test", Unit: "mail", Action: "backup", OK: true, Snapshot: "a"},
		{Subscription: "example.test", Unit: "db", Database: "example_wp", Action: "backup", OK: true, Snapshot: "b"},
	})
	e.Record([]Result{
		{Subscription: "example.test", Action: "check", OK: true},
		{Subscription: "example.test", Unit: "files", Action: "backup", OK: true, Snapshot: "c"},
	})
	st, err := ReadState(e.Cfg.StateDir)
	if err != nil || st["example.test"]["mail"]["backup"].Snapshot != "a" || st["example.test"]["db:example_wp"]["backup"].Snapshot != "b" ||
		st["example.test"]["files"]["backup"].Snapshot != "c" || !st["example.test"]["repository"]["check"].OK {
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

func TestReadStateDropsOldShape(t *testing.T) {
	e, _, _ := setup(t, "local")
	old := `{"example.test": {"backup": {"subscription": "example.test", "action": "backup", "ok": true}}, "other.example.test": {"mail": {"backup": {"subscription": "other.example.test", "ok": true}}}}`
	if err := os.WriteFile(filepath.Join(e.Cfg.StateDir, "state.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := ReadState(e.Cfg.StateDir)
	if err != nil || len(st["example.test"]) != 0 || !st["other.example.test"]["mail"]["backup"].OK {
		t.Fatalf("%+v %v", st, err)
	}
	if err := e.Record([]Result{{Subscription: "example.test", Unit: "mail", Action: "backup", OK: true}}); err != nil {
		t.Fatal(err)
	}
}

func allFeatures(e Env) { e.Inv.Subscriptions[0].Features = []string{"mail", "files", "db"} }

// units lists "subscription unit[:database] action" per result.
func units(rs []Result) []string {
	var out []string
	for _, r := range rs {
		k := r.Subscription + " " + r.Unit
		if r.Database != "" {
			k += ":" + r.Database
		}
		out = append(out, k+" "+r.Action)
	}
	return out
}

func TestBackupAllOrder(t *testing.T) {
	e, _, log := setup(t, "local")
	allFeatures(e)
	second := e.Inv.Subscriptions[0]
	second.Name = "second.example.test"
	second.MailDomains = []string{"example.test"}
	second.Databases = []inventory.Database{{Type: "mysql", Name: "a"}, {Type: "mysql", Name: "b"}}
	e.Inv.Subscriptions = append(e.Inv.Subscriptions, second)
	rs := e.BackupAll(context.Background(), "")
	want := []string{
		"example.test mail backup", "example.test files backup", "example.test db:example_wp backup",
		"second.example.test mail backup", "second.example.test files backup", "second.example.test db:a backup", "second.example.test db:b backup",
		"example.test  stats", "second.example.test  stats",
	}
	if got := units(rs); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	for _, r := range rs {
		if !r.OK {
			t.Errorf("%+v", r)
		}
	}
	var calls []string
	for _, l := range logLines(log()) {
		f := strings.Fields(l)
		switch {
		case f[0] == "backup" && strings.Contains(l, "--stdin-from-command"):
			calls = append(calls, "db "+f[len(f)-1])
		case f[0] == "backup":
			calls = append(calls, "backup "+f[indexOf(f, "--tag")+1])
		case f[0] == "stats":
			calls = append(calls, "stats")
		}
	}
	wantCalls := []string{"backup mail", "backup files", "db example_wp", "backup mail", "backup files", "db a", "db b", "stats", "stats"}
	if strings.Join(calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("restic calls %q\nwant %q", calls, wantCalls)
	}
	if rs[7].StoredBytes != 4096 {
		t.Errorf("stored bytes %+v", rs[7])
	}
}

func indexOf(f []string, s string) int {
	for i, x := range f {
		if x == s {
			return i
		}
	}
	return -1
}

func TestBackupAllUnitRestricts(t *testing.T) {
	e, _, log := setup(t, "local")
	allFeatures(e)
	rs := e.BackupAll(context.Background(), "files")
	if got := units(rs); strings.Join(got, "|") != "example.test files backup|example.test  stats" {
		t.Fatalf("%q", got)
	}
	if strings.Contains(log(), "--tag mail") || strings.Contains(log(), "--stdin-from-command") {
		t.Errorf("other units ran:\n%s", log())
	}
}

func TestFeaturesGateUnits(t *testing.T) {
	e, _, log := setup(t, "local")
	sub := mustSub(t, e, "example.test")
	sub.Features = []string{"files"} // mail_domains and databases are listed, but not in features
	rs := e.BackupSubscription(context.Background(), sub, "")
	if got := units(rs); len(got) != 1 || got[0] != "example.test files backup" || !rs[0].OK {
		t.Fatalf("%q %+v", got, rs)
	}
	l := log()
	if strings.Contains(l, "--tag mail") || strings.Contains(l, "--stdin-from-command") || !strings.Contains(l, "--tag files") {
		t.Fatalf("%s", l)
	}
	for _, u := range []string{"mail", "db"} {
		before := log()
		rs := e.BackupSubscription(context.Background(), sub, u)
		if len(rs) != 1 || !rs[0].OK || rs[0].Unit != u || !strings.Contains(rs[0].Note, "not among the subscription's features") {
			t.Errorf("--unit %s: %+v", u, rs)
		}
		if log() != before {
			t.Errorf("--unit %s ran restic", u)
		}
	}
}

func TestStatsFailureIsANote(t *testing.T) {
	e, env, _ := setup(t, "local")
	rs := e.BackupAll(context.Background(), "")
	if err := e.Record(rs); err != nil {
		t.Fatal(err)
	}
	env["FAKE_STATS_EXIT"] = "1"
	e.Now = func() time.Time { return time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC) }
	rs = e.BackupAll(context.Background(), "")
	last := rs[len(rs)-1]
	if last.Action != "stats" || !last.OK || last.StoredBytes != 4096 || !strings.Contains(last.Note, "restic stats: exit 1") || !strings.Contains(last.Note, "2026-10-15") {
		t.Fatalf("%+v", last)
	}
}

func TestStatsOnlyForRepositoriesWithASnapshot(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_CAT_EXIT"] = "12" // wrong password: no backup succeeds
	rs := e.BackupAll(context.Background(), "")
	for _, r := range rs {
		if r.Action == "stats" {
			t.Fatalf("stats after a run without snapshots: %+v", r)
		}
	}
	if strings.Contains(log(), "stats") {
		t.Fatal(log())
	}
}

func TestRepositoryOfAnotherIdIsRefused(t *testing.T) {
	e, _, log := setup(t, "local")
	allFeatures(e)
	e.Inv.Subscriptions[0].ID = "17"
	sub := mustSub(t, e, "example.test")
	if r := e.BackupMail(context.Background(), sub); !r.OK {
		t.Fatalf("%+v", r)
	}
	idFile := filepath.Join(e.Cfg.KeysDir, "example.test.id")
	if b, err := os.ReadFile(idFile); err != nil || string(b) != "17\n" {
		t.Fatalf("owner file: %q %v", b, err)
	}
	if fi, _ := os.Stat(idFile); fi.Mode().Perm() != 0o600 {
		t.Errorf("owner file mode %v", fi.Mode().Perm())
	}
	// The same id passes again.
	if r := e.BackupFiles(context.Background(), sub); !r.OK {
		t.Fatalf("%+v", r)
	}
	// The name now belongs to a different subscription.
	before := log()
	sub.ID = "42"
	rs := e.BackupSubscription(context.Background(), sub, "")
	rs = append(rs, e.Forget(context.Background(), sub), e.Check(context.Background(), sub, ""))
	for _, r := range rs {
		if r.OK || !strings.Contains(r.Error, `id "17"`) || !strings.Contains(r.Error, `id "42"`) || !strings.Contains(r.Error, "never deletes") {
			t.Errorf("%+v", r)
		}
	}
	if _, _, err := e.Runner(sub); err == nil {
		t.Error("Runner accepted another id's repository")
	}
	sub.ID = ""
	if r := e.BackupMail(context.Background(), sub); r.OK || !strings.Contains(r.Error, "gives no id") {
		t.Errorf("%+v", r)
	}
	if log() != before {
		t.Errorf("restic ran for another id's repository:\n%s", strings.TrimPrefix(log(), before))
	}
	if b, _ := os.ReadFile(idFile); string(b) != "17\n" {
		t.Errorf("owner file changed: %q", b)
	}
}

func TestNoIDRecordsNoOwner(t *testing.T) {
	e, _, _ := setup(t, "local")
	if r := e.BackupMail(context.Background(), mustSub(t, e, "example.test")); !r.OK {
		t.Fatalf("%+v", r)
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.KeysDir, "example.test.id")); err == nil {
		t.Error("owner file written without an id")
	}
}

func TestRecordKeepsUnitsApart(t *testing.T) {
	e, _, _ := setup(t, "local")
	allFeatures(e)
	e.Inv.Subscriptions[0].Databases = []inventory.Database{{Type: "mysql", Name: "a"}, {Type: "mysql", Name: "b"}}
	rs := e.BackupAll(context.Background(), "")
	if err := e.Record(rs); err != nil {
		t.Fatal(err)
	}
	if err := e.Record([]Result{e.Forget(context.Background(), mustSub(t, e, "example.test"))}); err != nil {
		t.Fatal(err)
	}
	st, err := ReadState(e.Cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	s := st["example.test"]
	for _, k := range []string{"mail", "files", "db:a", "db:b"} {
		if r := s[k]["backup"]; !r.OK || r.Snapshot == "" || r.StateKey() != k {
			t.Errorf("%s: %+v", k, r)
		}
	}
	if !s["repository"]["stats"].OK || !s["repository"]["forget"].OK || len(s) != 5 {
		t.Errorf("%+v", s)
	}
}

// With inventory_command, a key without an owner file (from a static
// inventory without ids or an earlier build) is refused until adopted.
func TestKeyWithoutOwnerIsRefusedUnderInventoryCommand(t *testing.T) {
	e, _, log := setup(t, "local")
	e.Cfg.InventoryCommand = []string{"/usr/local/bin/inventory"}
	keysEnsure(t, e)
	sub := mustSub(t, e, "example.test")
	sub.ID = "17"
	rs := e.BackupSubscription(context.Background(), sub, "")
	rs = append(rs, e.Forget(context.Background(), sub), e.Check(context.Background(), sub, ""))
	// Stats, like snapshots, ls and every restore, opens the repository
	// through Runner.
	if r := e.Stats(context.Background(), sub); !strings.Contains(r.Note+r.Error, "no owner file") {
		t.Errorf("stats: %+v", r)
	}
	for _, r := range rs {
		if r.OK || !strings.Contains(r.Error, "no owner file") || !strings.Contains(r.Error, "relical adopt --subscription example.test") {
			t.Errorf("%+v", r)
		}
	}
	if _, _, err := e.Runner(sub); err == nil {
		t.Error("Runner accepted a key without an owner")
	}
	// The panel configuration's repository falls under the same rule.
	os.WriteFile(filepath.Join(e.Cfg.KeysDir, "_server.key"), []byte("k\n"), 0o600)
	srv := ServerSubscription()
	if _, _, err := e.Runner(srv); err == nil || !strings.Contains(err.Error(), "relical adopt --subscription _server") {
		t.Errorf("_server: %v", err)
	}
	if r := e.Forget(context.Background(), srv); r.OK || !strings.Contains(r.Error, "no owner file") {
		t.Errorf("_server forget: %+v", r)
	}
	os.Remove(filepath.Join(e.Cfg.KeysDir, "_server.key"))
	if log() != "" {
		t.Errorf("restic ran:\n%s", log())
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.KeysDir, "example.test.id")); err == nil {
		t.Error("owner recorded without adopt")
	}
	// A static inventory keeps the earlier behaviour: the id is recorded.
	e.Cfg.InventoryCommand = nil
	if r := e.BackupMail(context.Background(), sub); !r.OK {
		t.Fatalf("static: %+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(e.Cfg.KeysDir, "example.test.id")); string(b) != "17\n" {
		t.Errorf("static: owner %q", b)
	}
}

// A new repository under inventory_command records its owner even when
// init fails, so the next run is not refused.
func TestNewRepositoryRecordsOwnerBeforeInit(t *testing.T) {
	e, env, _ := setup(t, "local")
	e.Cfg.InventoryCommand = []string{"/usr/local/bin/inventory"}
	sub := mustSub(t, e, "example.test")
	sub.ID = "17"
	env["FAKE_CAT_EXIT"], env["FAKE_OTHER_EXIT"] = "10", "1" // no repository; init fails
	if r := e.BackupMail(context.Background(), sub); r.OK || !strings.Contains(r.Error, "restic init") {
		t.Fatalf("%+v", r)
	}
	delete(env, "FAKE_CAT_EXIT")
	delete(env, "FAKE_OTHER_EXIT")
	if r := e.BackupMail(context.Background(), sub); !r.OK {
		t.Fatalf("second run: %+v", r)
	}
}

func TestAdopt(t *testing.T) {
	e, env, log := setup(t, "local")
	e.Cfg.InventoryCommand = []string{"/usr/local/bin/inventory"}
	sub := mustSub(t, e, "example.test")
	sub.ID = "17"
	idFile := filepath.Join(e.Cfg.KeysDir, "example.test.id")
	// No key: nothing to adopt.
	if r := e.Adopt(context.Background(), sub); r.OK || !strings.Contains(r.Error, "no key") {
		t.Errorf("no key: %+v", r)
	}
	keysEnsure(t, e)
	// The repository does not open: nothing is recorded.
	env["FAKE_CAT_EXIT"] = "12"
	if r := e.Adopt(context.Background(), sub); r.OK || !strings.Contains(r.Error, "exit 12") {
		t.Errorf("wrong password: %+v", r)
	}
	env["FAKE_CAT_EXIT"] = "10"
	if r := e.Adopt(context.Background(), sub); r.OK || !strings.Contains(r.Error, "does not") {
		t.Errorf("no repository: %+v", r)
	}
	if _, err := os.Stat(idFile); err == nil {
		t.Fatal("owner recorded for a repository that did not open")
	}
	if strings.Contains(log(), "init") {
		t.Errorf("adopt ran init:\n%s", log())
	}
	delete(env, "FAKE_CAT_EXIT")
	// Without an id there is nothing to record.
	noID := sub
	noID.ID = ""
	if r := e.Adopt(context.Background(), noID); r.OK || !strings.Contains(r.Error, "no id") {
		t.Errorf("no id: %+v", r)
	}
	r := e.Adopt(context.Background(), sub)
	if !r.OK || r.Action != "adopt" || !strings.Contains(r.Note, `recorded id "17"`) {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(idFile); string(b) != "17\n" {
		t.Fatalf("owner %q", b)
	}
	if r := e.BackupMail(context.Background(), sub); !r.OK {
		t.Fatalf("after adopt: %+v", r)
	}
	// Adopting again is a note; another id is refused.
	if r := e.Adopt(context.Background(), sub); !r.OK || !strings.Contains(r.Note, "already") {
		t.Errorf("again: %+v", r)
	}
	sub.ID = "42"
	if r := e.Adopt(context.Background(), sub); r.OK || !strings.Contains(r.Error, `id "17"`) {
		t.Errorf("other id: %+v", r)
	}
	if b, _ := os.ReadFile(idFile); string(b) != "17\n" {
		t.Errorf("owner changed: %q", b)
	}
}

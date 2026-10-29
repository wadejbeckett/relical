package job

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/testutil"
)

// withServer gives e a server whose config_command runs body (a shell
// script; $2 is the output file) and returns the staging file it will use.
func withServer(t *testing.T, e *Env, body string) string {
	t.Helper()
	bin := testutil.FakeCommand(t, "pleskbackup", body)
	e.Inv.Server = &inventory.Server{Features: []string{"panel-config"}, ConfigCommand: []string{bin, "-output-file", "{output}"}, ConfigExt: "tar"}
	return filepath.Join(e.StagingDir(), "panel-config-20261015T020000Z.tar")
}

const writeTar = `printf 'panel configuration tar\n' > "$2"`

func stagingEmpty(t *testing.T, e Env) {
	t.Helper()
	left, err := os.ReadDir(e.StagingDir())
	if err != nil || len(left) != 0 {
		t.Errorf("staging not empty: %v %v", left, err)
	}
}

func TestPanelConfigStagingIsDeleted(t *testing.T) {
	e, env, log := setup(t, "local")
	out := withServer(t, &e, writeTar)
	if fi, err := os.Stat(e.StagingDir()); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("staging dir: %v %v", fi, err)
	}
	r := e.BackupPanelConfig(context.Background())
	if !r.OK || r.Subscription != "_server" || r.Unit != "panel-config" || r.Snapshot == "" || r.StateKey() != "panel-config" {
		t.Fatalf("%+v", r)
	}
	l := logLines(log())
	if want := "backup --json --host host.example.test --one-file-system --tag panel-config " + out; l[len(l)-1] != want {
		t.Errorf("got  %s\nwant %s", l[len(l)-1], want)
	}
	if !strings.Contains(repoEnv(env), "RESTIC_REPOSITORY=/repo/_server") {
		t.Errorf("%s", repoEnv(env))
	}
	if seen, _ := os.ReadFile(env["FAKE_LOG"] + ".seen"); string(seen) != out+": panel configuration tar\n" {
		t.Errorf("restic did not see the staging file: %q", seen)
	}
	stagingEmpty(t, e)
	if b, err := os.ReadFile(filepath.Join(e.Cfg.KeysDir, "_server.id")); err != nil || string(b) != "_server\n" {
		t.Errorf("owner: %q %v", b, err)
	}

	// restic fails: the file is still deleted.
	env["FAKE_BACKUP_EXIT"] = "1"
	if r := e.BackupPanelConfig(context.Background()); r.OK || !strings.Contains(r.Error, "restic backup: exit 1") {
		t.Errorf("%+v", r)
	}
	stagingEmpty(t, e)
	delete(env, "FAKE_BACKUP_EXIT")

	// A partial backup of the one file is a failure.
	env["FAKE_BACKUP_EXIT"] = "3"
	if r := e.BackupPanelConfig(context.Background()); r.OK {
		t.Errorf("partial: %+v", r)
	}
	stagingEmpty(t, e)
	delete(env, "FAKE_BACKUP_EXIT")

	// The command fails after writing part of the file: deleted, and
	// restic never runs.
	withServer(t, &e, writeTar+"\necho 'pleskbackup: out of disk space' >&2; exit 4")
	before := log()
	if r := e.BackupPanelConfig(context.Background()); r.OK || !strings.Contains(r.Error, "config_command: exit 4: pleskbackup: out of disk space") {
		t.Errorf("%+v", r)
	}
	if strings.Contains(strings.TrimPrefix(log(), before), "backup ") {
		t.Errorf("restic backup ran after the command failed:\n%s", log())
	}
	stagingEmpty(t, e)

	// The command exits 0 without writing anything.
	withServer(t, &e, "exit 0")
	if r := e.BackupPanelConfig(context.Background()); r.OK || !strings.Contains(r.Error, "wrote no") {
		t.Errorf("%+v", r)
	}

	// A file left by a killed run is removed before the next one.
	stale := filepath.Join(e.StagingDir(), "panel-config-20261014T020000Z.tar")
	os.WriteFile(stale, []byte("old"), 0o600)
	withServer(t, &e, writeTar)
	if r := e.BackupPanelConfig(context.Background()); !r.OK {
		t.Fatalf("%+v", r)
	}
	stagingEmpty(t, e)
}

func TestPanelConfigStagingMustBePrivate(t *testing.T) {
	e, _, log := setup(t, "local")
	withServer(t, &e, writeTar)
	os.Chmod(e.StagingDir(), 0o755)
	if r := e.BackupPanelConfig(context.Background()); r.OK || !strings.Contains(r.Error, "mode 0700") {
		t.Fatalf("%+v", r)
	}
	if log() != "" {
		t.Errorf("restic ran:\n%s", log())
	}
	// Owned by someone other than root while relical runs as root.
	os.Chmod(e.StagingDir(), 0o700)
	func() {
		defer func(f func() bool) { asRoot = f }(asRoot)
		asRoot = func() bool { return true }
		if os.Getuid() == 0 {
			os.Chown(e.StagingDir(), 65534, 65534)
		}
		if r := e.BackupPanelConfig(context.Background()); r.OK || !strings.Contains(r.Error, "not owned by root") {
			t.Errorf("not root's: %+v", r)
		}
		if os.Getuid() == 0 {
			os.Chown(e.StagingDir(), 0, 0)
		}
	}()
	if log() != "" {
		t.Errorf("restic ran:\n%s", log())
	}
	os.Chmod(e.StagingDir(), 0o755)
	// Prepare restores the mode.
	if err := e.Prepare(); err != nil {
		t.Fatal(err)
	}
	if r := e.BackupPanelConfig(context.Background()); !r.OK {
		t.Fatalf("%+v", r)
	}
}

func TestPanelConfigCommandTimeout(t *testing.T) {
	old := PanelConfigTimeout
	PanelConfigTimeout = 300 * time.Millisecond
	defer func() { PanelConfigTimeout = old }()
	e, _, log := setup(t, "local")
	pidFile := filepath.Join(t.TempDir(), "pid")
	// The grandchild holds no pipe, so only the process-group kill ends it.
	withServer(t, &e, writeTar+"\nsleep 30 >/dev/null 2>&1 & echo $! > '"+pidFile+"'; wait")
	start := time.Now()
	r := e.BackupPanelConfig(context.Background())
	if r.OK || !strings.Contains(r.Error, "timed out") || !strings.Contains(r.Error, "killed") {
		t.Fatalf("%+v", r)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
	stagingEmpty(t, e)
	if strings.Contains(log(), "--tag panel-config") {
		t.Errorf("restic backup ran after the timeout:\n%s", log())
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 50; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d survived the timeout", pid)
}

// pidGone waits briefly for pid to exit; it reports false (and kills it)
// when it does not.
func pidGone(t *testing.T, pidFile string) bool {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 50; i++ {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	return false
}

func TestPanelConfigLingeringChildIsKilled(t *testing.T) {
	e, _, _ := setup(t, "local")
	pidFile := filepath.Join(t.TempDir(), "pid")
	// The command exits 0 and leaves a child in its process group.
	withServer(t, &e, writeTar+"\nsleep 30 >/dev/null 2>&1 & echo $! > '"+pidFile+"'; exit 0")
	if r := e.BackupPanelConfig(context.Background()); !r.OK {
		t.Fatalf("%+v", r)
	}
	if !pidGone(t, pidFile) {
		t.Fatal("the command's child outlived the run")
	}
	stagingEmpty(t, e)
}

func TestPanelConfigCommandInterrupted(t *testing.T) {
	e, _, log := setup(t, "local")
	keysEnsure(t, e)
	pidFile := filepath.Join(t.TempDir(), "pid")
	withServer(t, &e, writeTar+"\nsleep 30 >/dev/null 2>&1 & echo $! > '"+pidFile+"'; wait")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for i := 0; i < 200; i++ {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	r := e.BackupPanelConfig(ctx)
	if r.OK || !strings.Contains(r.Error, "interrupted") {
		t.Fatalf("%+v", r)
	}
	stagingEmpty(t, e)
	if strings.Contains(log(), "--tag panel-config") {
		t.Errorf("restic backup ran after the interruption:\n%s", log())
	}
	if !pidGone(t, pidFile) {
		t.Fatal("the grandchild survived the interruption")
	}
}

func TestCleanStagingRemovesStaleFiles(t *testing.T) {
	e, _, _ := setup(t, "local")
	stale := filepath.Join(e.StagingDir(), "panel-config-20261014T020000Z.tar")
	other := filepath.Join(e.StagingDir(), "keep-me")
	os.WriteFile(stale, []byte("old"), 0o600)
	os.WriteFile(other, []byte("x"), 0o600)
	if err := e.CleanStaging(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Error("stale panel configuration kept")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("removed a file that is not a panel configuration")
	}
}

func TestPanelConfigPasswordEnv(t *testing.T) {
	t.Setenv("RELICAL_CANARY", "leaked")
	e, _, log := setup(t, "local")
	envFile := filepath.Join(t.TempDir(), "env")
	// The command fails unless it gets the password, and records its
	// environment.
	withServer(t, &e, `env > '`+envFile+`'
[ -n "$PLESK_BACKUP_PASSWORD" ] || { echo 'PLESK_BACKUP_PASSWORD is not set' >&2; exit 1; }
`+writeTar)
	if r := e.BackupPanelConfig(context.Background()); !r.OK {
		t.Fatalf("%+v", r)
	}
	key, err := os.ReadFile(filepath.Join(e.Cfg.KeysDir, "_server.key"))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(envFile)
	want := "PLESK_BACKUP_PASSWORD=" + strings.TrimSpace(string(key)) + "\n"
	if len(strings.TrimSpace(string(key))) != 64 || !strings.Contains(string(got), want) {
		t.Errorf("env %q, want a line %q", got, want)
	}
	if !strings.Contains(string(got), "PATH="+os.Getenv("PATH")+"\n") {
		t.Errorf("PATH missing: %q", got)
	}
	if strings.Contains(string(got), "RELICAL_CANARY") || strings.Contains(string(got), "FAKE_") || strings.Contains(string(got), "RESTIC_") {
		t.Errorf("the command inherited other variables: %q", got)
	}
	// A failing command: its stderr tail is in the error, the key is not
	// (in argv, the error or the state file).
	withServer(t, &e, `echo "pleskbackup: cannot connect to the panel database" >&2; exit 3`)
	r := e.BackupPanelConfig(context.Background())
	k := strings.TrimSpace(string(key))
	if r.OK || !strings.Contains(r.Error, "cannot connect") || strings.Contains(r.Error, k) {
		t.Errorf("%+v", r)
	}
	if err := e.Record([]Result{r}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.Cfg.StateDir, "state.json")); strings.Contains(string(b), k) {
		t.Error("the key is in state.json")
	}
	if strings.Contains(log(), k) {
		t.Error("the key reached restic's arguments")
	}
	withServer(t, &e, `env > '`+envFile+`'
[ -n "$PLESK_BACKUP_PASSWORD" ] || { echo 'PLESK_BACKUP_PASSWORD is not set' >&2; exit 1; }
`+writeTar)
	// encrypt_with_repo_key false: no password.
	off := false
	e.Inv.Server.EncryptWithRepoKey = &off
	if r := e.BackupPanelConfig(context.Background()); r.OK || !strings.Contains(r.Error, "PLESK_BACKUP_PASSWORD is not set") {
		t.Errorf("%+v", r)
	}
}

func TestBackupAllRunsPanelConfigLast(t *testing.T) {
	e, _, log := setup(t, "local")
	allFeatures(e)
	withServer(t, &e, writeTar)
	rs := e.BackupAll(context.Background(), "")
	want := []string{
		"example.test mail backup", "example.test files backup", "example.test db:example_wp backup",
		"_server panel-config backup", "example.test  stats", "_server  stats",
	}
	if got := units(rs); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	for _, r := range rs {
		if !r.OK {
			t.Errorf("%+v", r)
		}
	}
	// --unit panel-config runs it alone; another unit leaves it out.
	before := log()
	rs = e.BackupAll(context.Background(), "panel-config")
	if got := units(rs); strings.Join(got, "|") != "_server panel-config backup|_server  stats" {
		t.Fatalf("%q", got)
	}
	if l := strings.TrimPrefix(log(), before); strings.Contains(l, "--tag mail") || strings.Contains(l, "--tag files") || strings.Contains(l, "--stdin-from-command") {
		t.Errorf("other units ran:\n%s", l)
	}
	rs = e.BackupAll(context.Background(), "mail")
	if got := units(rs); strings.Join(got, "|") != "example.test mail backup|example.test  stats" {
		t.Fatalf("%q", got)
	}
	// _server through BackupSubscription, as backup --subscription _server.
	if got := units(e.BackupSubscription(context.Background(), ServerSubscription(), "")); strings.Join(got, "|") != "_server panel-config backup" {
		t.Fatalf("%q", got)
	}
	if rs := e.BackupSubscription(context.Background(), ServerSubscription(), "mail"); len(rs) != 1 || !rs[0].OK || !strings.Contains(rs[0].Note, "not a unit of _server") {
		t.Fatalf("%+v", rs)
	}
	// Without a server, --all does nothing for it and --unit panel-config says so.
	e.Inv.Server = nil
	if got := units(e.BackupAll(context.Background(), "")); len(got) != 4 {
		t.Errorf("%q", got)
	}
	if rs := e.BackupAll(context.Background(), "panel-config"); len(rs) != 1 || !rs[0].OK || !strings.Contains(rs[0].Note, "does not list panel-config") {
		t.Errorf("%+v", rs)
	}
}

func TestForgetAndCheckServerRepository(t *testing.T) {
	e, _, log := setup(t, "local")
	withServer(t, &e, writeTar)
	sub := ServerSubscription()
	if r := e.Forget(context.Background(), sub); !r.OK || r.Note != NoRepositoryNote {
		t.Fatalf("before any backup: %+v", r)
	}
	if r := e.BackupPanelConfig(context.Background()); !r.OK {
		t.Fatalf("%+v", r)
	}
	before := log()
	if r := e.Forget(context.Background(), sub); !r.OK || r.Subscription != "_server" {
		t.Fatalf("%+v", r)
	}
	if r := e.Check(context.Background(), sub, ""); !r.OK {
		t.Fatalf("%+v", r)
	}
	l := strings.TrimPrefix(log(), before)
	if !strings.Contains(l, "forget --group-by tags --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune") || !strings.Contains(l, "check") {
		t.Errorf("%s", l)
	}
}

// A config_command that prints its PLESK_BACKUP_PASSWORD on stderr: the
// key is replaced by *** in the error, and so in state and the report.
func TestPanelConfigStderrNeverHoldsTheKey(t *testing.T) {
	e, _, _ := setup(t, "local")
	withServer(t, &e, `echo "pleskbackup: bad password $PLESK_BACKUP_PASSWORD" >&2; exit 1`)
	r := e.BackupPanelConfig(context.Background())
	key, _ := os.ReadFile(filepath.Join(e.Cfg.KeysDir, "_server.key"))
	k := strings.TrimSpace(string(key))
	if r.OK || len(k) == 0 || strings.Contains(r.Error, k) || !strings.Contains(r.Error, "bad password ***") {
		t.Errorf("%+v", r)
	}
}

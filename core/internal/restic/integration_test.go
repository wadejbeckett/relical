//go:build integration

package restic

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
)

func realRunner(t *testing.T, repo string) Runner {
	t.Helper()
	bin := os.Getenv("RELICAL_RESTIC")
	if bin == "" {
		t.Skip("RELICAL_RESTIC not set (run make tools, then make integration)")
	}
	pw := filepath.Join(t.TempDir(), "pw")
	os.WriteFile(pw, []byte("integration-test-password\n"), 0o600)
	return Runner{Bin: bin, Repository: repo, PasswordFile: pw, Host: "itest", CacheDir: t.TempDir()}
}

func maildir(t *testing.T) string {
	t.Helper()
	m := filepath.Join(t.TempDir(), "alice", "Maildir")
	for _, d := range []string{"cur", "new", "tmp", ".Sent/cur", ".Sent/tmp"} {
		os.MkdirAll(filepath.Join(m, d), 0o700)
	}
	os.WriteFile(filepath.Join(m, "cur", "1.h:2,S"), []byte("Subject: one\n\nx\n"), 0o600)
	os.WriteFile(filepath.Join(m, ".Sent", "cur", "2.h:2,S"), []byte("Subject: two\n\nx\n"), 0o600)
	os.WriteFile(filepath.Join(m, "dovecot.index.cache"), []byte("cache"), 0o600)
	os.WriteFile(filepath.Join(m, ".Sent", "dovecot.index.log"), []byte("log"), 0o600)
	os.WriteFile(filepath.Join(m, "tmp", "inflight"), []byte("partial"), 0o600)
	os.WriteFile(filepath.Join(m, ".Sent", "tmp", "inflight"), []byte("partial"), 0o600)
	return m
}

func excludesFor(m string) []string {
	return []string{"**/dovecot.index*", "**/dovecot.list.index*", m + "/tmp/*", m + "/.*/tmp/*"}
}

func TestRealRepoRoundTrip(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	if ok, err := r.Exists(ctx); ok || err != nil {
		t.Fatalf("before init: %v %v", ok, err)
	}
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	m := maildir(t)
	sum, err := r.Backup(ctx, []string{m}, []string{"backup"}, excludesFor(m))
	if err != nil || sum.SnapshotID == "" {
		t.Fatalf("%+v %v", sum, err)
	}
	target := t.TempDir()
	if err := r.Restore(ctx, sum.SnapshotID, target, []string{m}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"cur/1.h:2,S", ".Sent/cur/2.h:2,S"} {
		if _, err := os.Stat(filepath.Join(target, m, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	for _, gone := range []string{"dovecot.index.cache", ".Sent/dovecot.index.log", "tmp/inflight", ".Sent/tmp/inflight"} {
		if _, err := os.Stat(filepath.Join(target, m, gone)); err == nil {
			t.Errorf("excluded file %s was backed up", gone)
		}
	}
	if err := r.Check(ctx, "100%"); err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.PasswordFile = filepath.Join(t.TempDir(), "wrong")
	os.WriteFile(bad.PasswordFile, []byte("wrong\n"), 0o600)
	if _, err := bad.Exists(ctx); ExitCode(err) != ExitWrongPassword {
		t.Fatalf("wrong password: %v", err)
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestAppendOnlyRefusesForget(t *testing.T) {
	srv := os.Getenv("RELICAL_REST_SERVER")
	if srv == "" {
		t.Skip("RELICAL_REST_SERVER not set")
	}
	port := freePort(t)
	cmd := exec.Command(srv, "--append-only", "--no-auth", "--path", t.TempDir(), "--listen", fmt.Sprintf("127.0.0.1:%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			c.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	ctx := context.Background()
	r := realRunner(t, fmt.Sprintf("rest:http://127.0.0.1:%d/example.test/", port))
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	m := maildir(t)
	for i := 0; i < 2; i++ {
		if _, err := r.Backup(ctx, []string{m}, []string{"backup"}, excludesFor(m)); err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(m, "cur", fmt.Sprintf("x%d.h:2,S", i)), []byte("new"), 0o600)
	}
	err := r.Forget(ctx, "tags", config.Retention{Daily: 1}, true)
	if err == nil {
		t.Fatal("append-only server allowed forget --prune")
	}
	s, _ := r.Snapshots(ctx, "backup")
	if len(s) != 2 {
		t.Fatalf("snapshots after refused forget: %d", len(s))
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Errorf("unexpected error shape: %v", err)
	}
}

// TestStaleLockIsRemovedByUnlock leaves the stale lock that a SIGKILLed
// backup leaves behind, shows check cannot get past it, and shows that
// Unlock (as run before every forget and check) clears it.
func TestStaleLockIsRemovedByUnlock(t *testing.T) {
	ctx := context.Background()
	repo := filepath.Join(t.TempDir(), "repo")
	r := realRunner(t, repo)
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	big := make([]byte, 8<<20) // incompressible, so the upload limit really slows the backup
	rand.New(rand.NewSource(1)).Read(big)
	if err := os.WriteFile(filepath.Join(src, "big"), big, 0o600); err != nil {
		t.Fatal(err)
	}
	slow := r
	slow.LimitUploadKiB = 64 // keeps the backup running long enough to be killed mid-run
	cmd := slow.command(ctx, "backup", src)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	locked := false
	for i := 0; i < 300 && !locked; i++ {
		if e, _ := os.ReadDir(filepath.Join(repo, "locks")); len(e) > 0 {
			locked = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Let restic finish writing the lock file and get into the upload, as
	// a backup killed by the OOM killer or a power loss would be.
	time.Sleep(time.Second)
	cmd.Process.Kill()
	t.Logf("killed backup: %v", cmd.Wait())
	if !locked {
		t.Fatal("backup never took a lock")
	}
	if e, _ := os.ReadDir(filepath.Join(repo, "locks")); len(e) == 0 {
		t.Fatal("SIGKILLed backup left no lock behind")
	}

	blocked, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	err := r.Check(blocked, "")
	if err == nil {
		t.Fatal("check succeeded despite the stale lock")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("check before unlock: %v", err)
	}
	t.Logf("check before unlock: %v", err)

	if err := r.Unlock(ctx); err != nil {
		t.Fatal(err)
	}
	if e, _ := os.ReadDir(filepath.Join(repo, "locks")); len(e) != 0 {
		t.Errorf("locks left after unlock: %d", len(e))
	}
	if err := r.Check(ctx, ""); err != nil {
		t.Fatalf("check after unlock: %v", err)
	}
}

// TestForgetGroupsByTags shows that --group-by tags keeps each tag set
// apart: with keep-daily 1, the newest snapshot of each group survives.
func TestForgetGroupsByTags(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	newest := map[string]string{}
	for i := 0; i < 2; i++ {
		os.WriteFile(filepath.Join(src, "f"), []byte(fmt.Sprint(i)), 0o600)
		sum, err := r.Backup(ctx, []string{src}, []string{"files"}, nil)
		if err != nil {
			t.Fatal(err)
		}
		newest["files"] = sum.SnapshotID
		sum, err = r.BackupStdin(ctx, []string{"echo", fmt.Sprint("dump ", i)}, "db1.sql", []string{"db", "db:db1"})
		if err != nil {
			t.Fatal(err)
		}
		newest["db,db:db1"] = sum.SnapshotID
	}
	if err := r.Forget(ctx, "tags", config.Retention{Daily: 1}, true); err != nil {
		t.Fatal(err)
	}
	snaps, err := r.Snapshots(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range snaps {
		got[strings.Join(s.Tags, ",")] = s.ID
	}
	if len(snaps) != 2 || got["files"] != newest["files"] || got["db,db:db1"] != newest["db,db:db1"] {
		t.Fatalf("kept %+v, want the newest of each group %v", snaps, newest)
	}
}

// TestBackupStdinFailingCommandSavesNothing shows restic 0.19 fails, and
// saves no snapshot, when the --stdin-from-command command exits non-zero
// after writing part of its output.
func TestBackupStdinFailingCommandSavesNothing(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := r.BackupStdin(ctx, []string{"sh", "-c", "echo 'CREATE TABLE t;'; echo 'mysqldump: Got error: 2013' >&2; exit 2"}, "db1.sql", []string{"db", "db:db1"})
	if err == nil {
		t.Fatal("failing command accepted")
	}
	if !strings.Contains(err.Error(), "exit status 2") || !strings.Contains(err.Error(), "Got error: 2013") {
		t.Errorf("error does not carry the command's failure: %v", err)
	}
	if s, err := r.Snapshots(ctx, ""); err != nil || len(s) != 0 {
		t.Fatalf("snapshots after a failed dump: %+v %v", s, err)
	}
	sum, err := r.BackupStdin(ctx, []string{"echo", "CREATE TABLE t;"}, "db1.sql", []string{"db", "db:db1"})
	if err != nil || sum.SnapshotID == "" {
		t.Fatalf("%+v %v", sum, err)
	}
	s, _ := r.Snapshots(ctx, "db:db1")
	if len(s) != 1 || len(s[0].Paths) != 1 || s[0].Paths[0] != "/db1.sql" {
		t.Fatalf("%+v", s)
	}
	n, err := r.Stats(ctx)
	if err != nil || n == 0 {
		t.Fatalf("stats: %d %v", n, err)
	}
}

// TestFilesExcludesAndCaches backs up a webspace with anchored excludes
// and a CACHEDIR.TAG directory, as the files unit does.
func TestFilesExcludesAndCaches(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "odd[1]")
	for _, d := range []string{"httpdocs/logs", "logs", "tmp", "cache"} {
		os.MkdirAll(filepath.Join(home, d), 0o700)
	}
	for _, f := range []string{"httpdocs/index.php", "httpdocs/logs/keep.txt", "logs/access_log", "tmp/sess", "cache/blob"} {
		os.WriteFile(filepath.Join(home, f), []byte("x"), 0o600)
	}
	os.WriteFile(filepath.Join(home, "cache", "CACHEDIR.TAG"), []byte("Signature: 8a477f597d28d172789f06886806bc55\n"), 0o600)
	esc := strings.NewReplacer("[", `\[`).Replace(home)
	sum, err := r.Backup(ctx, []string{home}, []string{"files"}, []string{esc + "/logs/**", esc + "/tmp/**"}, "--exclude-caches")
	if err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := r.Restore(ctx, sum.SnapshotID, target, []string{esc}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"httpdocs/index.php", "httpdocs/logs/keep.txt", "cache/CACHEDIR.TAG"} {
		if _, err := os.Stat(filepath.Join(target, home, want)); err != nil {
			t.Errorf("missing %s", want)
		}
	}
	for _, gone := range []string{"logs/access_log", "tmp/sess", "cache/blob"} {
		if _, err := os.Stat(filepath.Join(target, home, gone)); err == nil {
			t.Errorf("excluded %s was backed up", gone)
		}
	}
}

// TestSftpArgsReachSshIntact runs the real restic against a fake ssh on
// PATH: a comma in sftp_args must reach ssh, not cut the value off.
func TestSftpArgsReachSshIntact(t *testing.T) {
	r := realRunner(t, "sftp:u000000-sub1@u000000-sub1.your-storagebox.example:relical/example.test")
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nfor a in \"$@\"; do echo \"[$a]\"; done > '"+argv+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))
	r.SftpArgs = "-p 23 -o Ciphers=aes128-ctr,aes256-ctr -i /etc/relical/ssh/id_ed25519"
	if _, err := r.Exists(context.Background()); err == nil {
		t.Fatal("fake ssh connected")
	}
	b, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(strings.TrimSpace(string(b)), "\n", " ")
	if !strings.Contains(got, "[-p] [23] [-o] [Ciphers=aes128-ctr,aes256-ctr] [-i] [/etc/relical/ssh/id_ed25519]") {
		t.Errorf("ssh argv: %s", got)
	}
}

// webspace makes a small vhost-shaped tree and backs it up.
func webspace(t *testing.T, ctx context.Context, r Runner) (home, snapshot string) {
	t.Helper()
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	home = filepath.Join(t.TempDir(), "example.test")
	for _, d := range []string{"httpdocs/sub", "logs"} {
		os.MkdirAll(filepath.Join(home, d), 0o750)
	}
	os.WriteFile(filepath.Join(home, "httpdocs", "index.html"), []byte("hello\n"), 0o640)
	os.WriteFile(filepath.Join(home, "httpdocs", "sub", "deep.txt"), []byte("deep\n"), 0o600)
	os.WriteFile(filepath.Join(home, "logs", "access.log"), []byte("log\n"), 0o600)
	sum, err := r.Backup(ctx, []string{home}, []string{"files"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return home, sum.SnapshotID
}

func TestRealRestoreInclude(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	home, snap := webspace(t, ctx, r)
	target := t.TempDir()
	if err := r.Restore(ctx, snap, target, []string{home + "/httpdocs"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(target, home, "httpdocs", "sub", "deep.txt"))
	if err != nil || string(b) != "deep\n" {
		t.Fatalf("%q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(target, home, "httpdocs", "index.html")); err != nil || fi.Mode().Perm() != 0o640 {
		t.Errorf("mode not kept: %v %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(target, home, "logs")); err == nil {
		t.Error("restored logs although only httpdocs was included")
	}
	// An include that matches nothing: exit 0 and nothing restored.
	empty := t.TempDir()
	if err := r.Restore(ctx, snap, empty, []string{home + "/nope"}); err != nil {
		t.Fatalf("restic restore with an unmatched include: %v", err)
	}
	if entries, _ := os.ReadDir(empty); len(entries) != 0 {
		t.Errorf("unmatched include restored %v", entries)
	}
}

func TestRealLsOneLevel(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	home, snap := webspace(t, ctx, r)
	nodes, truncated, err := r.Ls(ctx, snap, home, 100)
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	got := []string{}
	for _, n := range nodes {
		got = append(got, n.Type+" "+n.Path)
	}
	// The directory itself, then its direct children only.
	want := []string{"dir " + home, "dir " + home + "/httpdocs", "dir " + home + "/logs"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if nodes[1].Name != "httpdocs" || nodes[1].MTime.IsZero() {
		t.Errorf("%+v", nodes[1])
	}
	file, _, err := r.Ls(ctx, snap, home+"/httpdocs/index.html", 100)
	if err != nil || len(file) != 1 || file[0].Type != "file" || file[0].Size != 6 || file[0].Name != "index.html" {
		t.Fatalf("file: %+v %v", file, err)
	}
	missing, _, err := r.Ls(ctx, snap, home+"/nope", 100)
	if err != nil || len(missing) != 0 {
		t.Fatalf("missing path: %+v %v", missing, err)
	}
	capped, truncated, err := r.Ls(ctx, snap, home, 2)
	if err != nil || !truncated || len(capped) != 2 {
		t.Fatalf("cap: %d %v %v", len(capped), truncated, err)
	}
}

func TestRealDumpStreams(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	sum, err := r.BackupStdin(ctx, []string{"printf", "CREATE TABLE t (id int);\n"}, "example_wp.sql", []string{"db", "db:example_wp"})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := r.Dump(ctx, sum.SnapshotID, "/example_wp.sql", &b); err != nil {
		t.Fatal(err)
	}
	if b.String() != "CREATE TABLE t (id int);\n" {
		t.Fatalf("%q", b.String())
	}
	nodes, _, err := r.Ls(ctx, sum.SnapshotID, "/", 10)
	if err != nil || len(nodes) != 1 || nodes[0].Path != "/example_wp.sql" {
		t.Fatalf("ls /: %+v %v", nodes, err)
	}
	err = r.Dump(ctx, sum.SnapshotID, "/other.sql", io.Discard)
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("dump of a missing file: %v", err)
	}
}

// TestRealRestoreIncludeSiblingsAndEscapes pins what restore's include
// patterns rely on: a directory include leaves a sibling with the same
// prefix alone, and an escaped [ matches itself (job.GlobEscape).
func TestRealRestoreIncludeSiblingsAndEscapes(t *testing.T) {
	ctx := context.Background()
	r := realRunner(t, filepath.Join(t.TempDir(), "repo"))
	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "example.test")
	for _, d := range []string{"httpdocs", "httpdocs2", "a[b]", "ab"} {
		os.MkdirAll(filepath.Join(home, d), 0o750)
		os.WriteFile(filepath.Join(home, d, "f"), []byte(d), 0o600)
	}
	sum, err := r.Backup(ctx, []string{home}, []string{"files"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for include, want := range map[string]string{
		home + "/httpdocs": "httpdocs",
		home + `/a\[b]`:    "a[b]",
	} {
		target := t.TempDir()
		if err := r.Restore(ctx, sum.SnapshotID, target, []string{include}); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(filepath.Join(target, home))
		if len(entries) != 1 || entries[0].Name() != want {
			t.Errorf("include %s restored %v, want only %s", include, entries, want)
		}
	}
	// The restore size of the whole snapshot and of one path.
	if n, err := r.RestoreSize(ctx, sum.SnapshotID, ""); err != nil || n < 8 {
		t.Errorf("whole snapshot: %d %v", n, err)
	}
	if n, err := r.RestoreSize(ctx, sum.SnapshotID, home+"/httpdocs2"); err != nil || n != uint64(len("httpdocs2")) {
		t.Errorf("httpdocs2: %d %v", n, err)
	}
}

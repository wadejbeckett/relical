//go:build integration

package restic

import (
	"context"
	"fmt"
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
	if err := r.Restore(ctx, sum.SnapshotID, m, target); err != nil {
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
	err := r.Forget(ctx, "backup", config.Retention{Daily: 1}, true)
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

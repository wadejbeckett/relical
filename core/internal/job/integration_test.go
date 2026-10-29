//go:build integration

package job

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

// TestRealPanelConfigRoundTrip backs up the panel configuration with the
// real restic into a local _server repository, then checks that the
// snapshot holds the file, the staging file is gone, the command got the
// repository key as PLESK_BACKUP_PASSWORD, and forget and check pass.
func TestRealPanelConfigRoundTrip(t *testing.T) {
	bin := os.Getenv("RELICAL_RESTIC")
	if bin == "" {
		t.Skip("RELICAL_RESTIC not set (run make tools, then make integration)")
	}
	ctx := context.Background()
	e, _, _ := setup(t, "local")
	e.Cfg.ResticBin = bin
	e.ExtraEnv = nil
	repos := t.TempDir()
	e.Cfg.Destinations["d"] = config.Destination{Repository: repos + "/{subscription}", Prune: "local"}
	// The tar's content is the password it was given, so the test can
	// compare it with the key after a restic dump.
	withServer(t, &e, `printf 'password=%s\n' "$PLESK_BACKUP_PASSWORD" > "$2"`)
	r := e.BackupPanelConfig(ctx)
	if !r.OK || r.Snapshot == "" {
		t.Fatalf("%+v", r)
	}
	stagingEmpty(t, e)
	run, _, err := e.Runner(ServerSubscription())
	if err != nil {
		t.Fatal(err)
	}
	snaps, err := run.Snapshots(ctx, PanelConfigTag)
	if err != nil || len(snaps) != 1 || snaps[0].ID != r.Snapshot {
		t.Fatalf("%+v %v", snaps, err)
	}
	out := filepath.Join(e.StagingDir(), "panel-config-20261015T020000Z.tar")
	if len(snaps[0].Paths) != 1 || snaps[0].Paths[0] != out {
		t.Errorf("paths %q", snaps[0].Paths)
	}
	key, _ := os.ReadFile(filepath.Join(e.Cfg.KeysDir, "_server.key"))
	cmd := exec.Command(bin, "dump", r.Snapshot, out)
	cmd.Env = []string{"RESTIC_REPOSITORY=" + repos + "/_server", "RESTIC_PASSWORD_FILE=" + filepath.Join(e.Cfg.KeysDir, "_server.key"), "RESTIC_CACHE_DIR=" + t.TempDir()}
	got, err := cmd.Output()
	if err != nil || string(got) != "password="+strings.TrimSpace(string(key))+"\n" {
		t.Fatalf("dump %q %v", got, err)
	}
	if r := e.Forget(ctx, ServerSubscription()); !r.OK {
		t.Fatalf("%+v", r)
	}
	if r := e.Check(ctx, ServerSubscription(), ""); !r.OK {
		t.Fatalf("%+v", r)
	}
	// A failing command leaves no snapshot and no staging file.
	withServer(t, &e, `printf 'partial\n' > "$2"; exit 1`)
	if r := e.BackupPanelConfig(ctx); r.OK {
		t.Fatalf("%+v", r)
	}
	stagingEmpty(t, e)
	if snaps, err := run.Snapshots(ctx, PanelConfigTag); err != nil || len(snaps) != 1 {
		t.Fatalf("%+v %v", snaps, err)
	}
}

// TestRealUnreachableDestinationIsGivenUp runs the real restic and ssh
// against a TCP listener that accepts connections and never answers, as a
// stalled Storage Box would. Each of the first three subscriptions fails
// on ssh's ConnectTimeout, and the rest are not tried, so the run ends
// within a bound instead of hanging.
func TestRealUnreachableDestinationIsGivenUp(t *testing.T) {
	bin := os.Getenv("RELICAL_RESTIC")
	if bin == "" {
		t.Skip("RELICAL_RESTIC not set (run make tools, then make integration)")
	}
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("no ssh on PATH")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			held = append(held, c) // accepted, never answered
		}
	}()
	e, _, _ := setup(t, "local")
	sftpSubs(t, &e)
	e.Cfg.ResticBin = bin
	e.ExtraEnv = nil
	d := e.Cfg.Destinations["d"]
	d.Repository = "sftp:relical@127.0.0.1:relical/{subscription}"
	d.SftpArgs = fmt.Sprintf("-p %d -o ConnectTimeout=2 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null", ln.Addr().(*net.TCPAddr).Port)
	e.Cfg.Destinations["d"] = d
	e.Breaker = NewBreaker()
	start := time.Now()
	rs := e.BackupAll(context.Background(), "")
	if took := time.Since(start); took > 45*time.Second {
		t.Errorf("the run took %s", took)
	}
	if len(rs) != 5 {
		t.Fatalf("%+v", rs)
	}
	for i, r := range rs {
		if r.OK {
			t.Errorf("%d: %+v", i, r)
		}
		if notTried := strings.Contains(r.Error, "unreachable; not tried"); notTried != (i >= BreakerTrips) {
			t.Errorf("%d: %s", i, r.Error)
		}
	}
	if !strings.Contains(rs[0].Error, "timed out") {
		t.Errorf("first error: %s", rs[0].Error)
	}
}

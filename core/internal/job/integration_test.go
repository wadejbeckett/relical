//go:build integration

package job

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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

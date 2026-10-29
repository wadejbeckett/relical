package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestorePanelConfig(t *testing.T) {
	e, env, log := webSetup(t)
	res, err := RunPanelConfig(context.Background(), e, PanelConfigRequest{Subscription: "_server", Snapshot: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(e.Cfg.StateDir, "restore", "panel-config-2026-10-22-cccccccc.tar")
	if res.RestoredTo != want || !strings.Contains(res.Note, "plesk bin pleskrestore") || !strings.Contains(res.Note, "PLESK_BACKUP_PASSWORD") {
		t.Errorf("%+v", res)
	}
	if fi, err := os.Stat(want); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi, err)
	}
	if !strings.Contains(log(), "dump "+panelSnap+" "+filepath.Join(e.Cfg.StateDir, "staging", "panel-config-20261022T020000Z.tar")) {
		t.Errorf("%s", log())
	}
	if strings.Contains(log(), "pleskrestore") {
		t.Error("ran pleskrestore")
	}
	if _, err := RunPanelConfig(context.Background(), e, PanelConfigRequest{Subscription: "_server", Snapshot: "latest"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second restore: %v", err)
	}
	// A failed dump leaves no file.
	os.Remove(want)
	env["FAKE_DUMP_EXIT"] = "1"
	if _, err := RunPanelConfig(context.Background(), e, PanelConfigRequest{Subscription: "_server", Snapshot: "latest"}); err == nil {
		t.Fatal("failed dump not reported")
	}
	if entries, _ := os.ReadDir(filepath.Join(e.Cfg.StateDir, "restore")); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestRestorePanelConfigOnlyFromServer(t *testing.T) {
	e, _, _ := webSetup(t)
	if _, err := RunPanelConfig(context.Background(), e, PanelConfigRequest{Subscription: "example.test", Snapshot: "latest"}); err == nil {
		t.Error("accepted a subscription")
	}
	if _, err := RunPanelConfig(context.Background(), e, PanelConfigRequest{Subscription: "_server", Snapshot: "aaaaaaaa"}); err == nil {
		t.Error("accepted a files snapshot")
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wadejbeckett/relical/core/internal/testutil"
)

func cliSetup(t *testing.T) (cfgPath string) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "mail", "example.test", "alice", "Maildir", "cur"), 0o700)
	os.Mkdir(filepath.Join(root, "keys"), 0o700)
	bin, env, _ := testutil.FakeRestic(t)
	for k, v := range env {
		t.Setenv(k, v) // the CLI passes RELICAL_TEST_ENV through; see fakeEnvFromProcess
	}
	t.Setenv("RELICAL_TEST_ENV", "1")
	cfg := map[string]any{
		"server_id": "h", "restic_bin": bin, "state_dir": filepath.Join(root, "state"), "keys_dir": filepath.Join(root, "keys"),
		"mailbox_pattern": filepath.Join(root, "mail") + "/{domain}/{mailbox}/Maildir", "domains": []string{"example.test"},
		"default_destination": "d", "destinations": map[string]any{"d": map[string]any{"repository": "/repo/{domain}", "prune": "local"}},
	}
	b, _ := json.Marshal(cfg)
	cfgPath = filepath.Join(root, "config.json")
	os.WriteFile(cfgPath, b, 0o600)
	return cfgPath
}

func runCLI(args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := run(context.Background(), args, &out, &errb)
	return code, out.String()
}

func TestVersion(t *testing.T) {
	code, out := runCLI("version")
	if code != 0 || !strings.Contains(out, `"version"`) {
		t.Fatalf("%d %s", code, out)
	}
}

func TestUsageErrors(t *testing.T) {
	if code, _ := runCLI(); code != 2 {
		t.Errorf("no command: %d", code)
	}
	cfg := cliSetup(t)
	if code, _ := runCLI("-config", cfg, "frobnicate"); code != 2 {
		t.Errorf("unknown command: %d", code)
	}
	if code, _ := runCLI("-config", cfg, "backup"); code != 2 {
		t.Errorf("backup without --domain/--all: %d", code)
	}
	if code, _ := runCLI("-config", cfg, "backup", "--domain", "example.test", "--all"); code != 2 {
		t.Errorf("both --domain and --all: %d", code)
	}
}

func TestMissingConfigIsJSONError(t *testing.T) {
	code, out := runCLI("-config", "/nonexistent/relical.json", "status")
	var v map[string]string
	if code != 1 || json.Unmarshal([]byte(out), &v) != nil || v["error"] == "" {
		t.Fatalf("%d %s", code, out)
	}
}

func TestBackupAllThenStatus(t *testing.T) {
	cfg := cliSetup(t)
	code, out := runCLI("-config", cfg, "backup", "--all")
	if code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 1 || results[0]["ok"] != true {
		t.Fatalf("%v %s", err, out)
	}
	code, out = runCLI("-config", cfg, "status")
	if code != 0 || !strings.Contains(out, `"example.test"`) || !strings.Contains(out, `"backup"`) {
		t.Fatalf("%d %s", code, out)
	}
}

func TestFailedDomainExitsOne(t *testing.T) {
	cfg := cliSetup(t)
	t.Setenv("FAKE_CAT_EXIT", "12")
	code, out := runCLI("-config", cfg, "backup", "--domain", "example.test")
	if code != 1 || !strings.Contains(out, "exit 12") {
		t.Fatalf("%d %s", code, out)
	}
}

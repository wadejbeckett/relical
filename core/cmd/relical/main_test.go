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

var testSub = map[string]any{
	"id": "17", "name": "example.test", "sys_user": "exampleuser", "uid": 10001, "gid": 1004,
	"home": "/var/www/vhosts/example.test", "features": []string{"mail"}, "mail_domains": []string{"example.test"},
	"files": []string{"/var/www/vhosts/example.test"},
}

func cliSetup(t *testing.T) (cfgPath string) {
	t.Helper()
	return cliSetupWith(t, map[string]any{"subscriptions": []any{testSub}})
}

// cliSetupWith writes a config whose inventory part is inv.
func cliSetupWith(t *testing.T, inv map[string]any) (cfgPath string) {
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
		"version": 2, "server_id": "h", "restic_bin": bin, "state_dir": filepath.Join(root, "state"), "keys_dir": filepath.Join(root, "keys"),
		"roots":               map[string]any{"mail": []string{filepath.Join(root, "mail")}},
		"mailbox_pattern":     filepath.Join(root, "mail") + "/{domain}/{mailbox}/Maildir",
		"default_destination": "d", "destinations": map[string]any{"d": map[string]any{"repository": "/repo/{subscription}", "prune": "local"}},
	}
	for k, v := range inv {
		cfg[k] = v
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
	for _, args := range [][]string{
		{"backup"},
		{"backup", "--subscription", "example.test", "--all"},
		{"backup", "--domain", "example.test"},
		{"backup", "--all", "--unit", "everything"},
		{"forget", "--all", "--unit", "mail"},
		{"backup", "--all", "extra"},
		{"snapshots"},
		{"mailboxes"},
		{"restore", "--domain", "example.test"},
		{"restore", "--mailbox", "alice"},
		{"version", "--all"},
		{"inventory", "--subscription", "example.test"},
	} {
		if code, _ := runCLI(append([]string{"-config", cfg}, args...)...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
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
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 2 || results[0]["ok"] != true ||
		results[0]["subscription"] != "example.test" || results[0]["unit"] != "mail" ||
		results[1]["action"] != "stats" || results[1]["stored_bytes"] != float64(4096) {
		t.Fatalf("%v %s", err, out)
	}
	code, out = runCLI("-config", cfg, "status")
	var st map[string]map[string]map[string]map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &st) != nil || st["example.test"]["mail"]["backup"]["ok"] != true ||
		st["example.test"]["repository"]["stats"]["stored_bytes"] != float64(4096) {
		t.Fatalf("%d %s", code, out)
	}
}

func TestBackupAllUnits(t *testing.T) {
	root := t.TempDir()
	cnf := filepath.Join(root, "mysql.cnf")
	os.WriteFile(cnf, []byte("[client]\n"), 0o600)
	sub := map[string]any{}
	for k, v := range testSub {
		sub[k] = v
	}
	sub["features"] = []string{"mail", "files", "db"}
	sub["databases"] = []any{map[string]any{"type": "mysql", "name": "example_wp"}}
	cfg := cliSetupWith(t, map[string]any{
		"subscriptions": []any{sub}, "mysql": map[string]any{"defaults_file": cnf}, "mysqldump_bin": testutil.FakeMysqldump(t),
	})
	code, out := runCLI("-config", cfg, "backup", "--all")
	var results []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 4 {
		t.Fatalf("%d %s", code, out)
	}
	for i, u := range []string{"mail", "files", "db", ""} {
		if results[i]["unit"] != nil && results[i]["unit"] != u || results[i]["ok"] != true {
			t.Errorf("result %d: %v", i, results[i])
		}
	}
	if results[2]["database"] != "example_wp" {
		t.Errorf("%v", results[2])
	}
	code, out = runCLI("-config", cfg, "backup", "--subscription", "example.test", "--unit", "db")
	if code != 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 1 || results[0]["database"] != "example_wp" {
		t.Fatalf("%d %s", code, out)
	}
	code, out = runCLI("-config", cfg, "status")
	if code != 0 || !strings.Contains(out, `"db:example_wp"`) || !strings.Contains(out, `"files"`) {
		t.Fatalf("%d %s", code, out)
	}
	t.Setenv("FAKE_MYSQLDUMP_FAIL", "example_wp")
	if code, out := runCLI("-config", cfg, "backup", "--subscription", "example.test", "--unit", "db"); code != 1 || !strings.Contains(out, `"ok": false`) {
		t.Fatalf("%d %s", code, out)
	}
}

func TestFailedSubscriptionExitsOne(t *testing.T) {
	cfg := cliSetup(t)
	t.Setenv("FAKE_CAT_EXIT", "12")
	code, out := runCLI("-config", cfg, "backup", "--subscription", "example.test")
	if code != 1 || !strings.Contains(out, "exit 12") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestUnknownSubscriptionIsJSONError(t *testing.T) {
	cfg := cliSetup(t)
	code, out := runCLI("-config", cfg, "backup", "--subscription", "nope.example.test")
	if code != 1 || !strings.Contains(out, "not in the inventory") {
		t.Fatalf("%d %s", code, out)
	}
}

func TestUnimplementedUnitIsJSONError(t *testing.T) {
	cfg := cliSetup(t)
	for _, args := range [][]string{
		{"backup", "--all", "--unit", "panel-config"},
		{"restore", "--unit", "db", "--domain", "example.test", "--mailbox", "alice"},
	} {
		code, out := runCLI(append([]string{"-config", cfg}, args...)...)
		if code != 1 || !strings.Contains(out, "not supported") {
			t.Errorf("%q: %d %s", args, code, out)
		}
	}
}

func TestInventoryPrintsStatic(t *testing.T) {
	cfg := cliSetup(t)
	code, out := runCLI("-config", cfg, "inventory")
	var inv struct {
		Version       int              `json:"version"`
		Subscriptions []map[string]any `json:"subscriptions"`
		Server        any              `json:"server"`
		Renamed       []any            `json:"renamed"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &inv) != nil || inv.Version != 2 || len(inv.Subscriptions) != 1 ||
		inv.Subscriptions[0]["name"] != "example.test" || inv.Renamed == nil {
		t.Fatalf("%d %s", code, out)
	}
	if _, ok := inv.Subscriptions[0]["file_excludes"]; !ok {
		t.Errorf("defaults not shown: %s", out)
	}
}

func TestInventoryFromCommand(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"version": 2, "subscriptions": []any{testSub}})
	cfg := cliSetupWith(t, map[string]any{"inventory_command": []string{testutil.FakeInventory(t, string(b))}})
	code, out := runCLI("-config", cfg, "inventory")
	if code != 0 || !strings.Contains(out, `"name": "example.test"`) {
		t.Fatalf("%d %s", code, out)
	}
	code, out = runCLI("-config", cfg, "backup", "--subscription", "example.test")
	if code != 0 || !strings.Contains(out, `"ok": true`) {
		t.Fatalf("%d %s", code, out)
	}
}

func TestInventoryCommandFailureIsJSONError(t *testing.T) {
	cfg := cliSetupWith(t, map[string]any{"inventory_command": []string{testutil.FakeInventoryFailing(t)}})
	for _, cmd := range []string{"inventory", "backup"} {
		args := []string{"-config", cfg, cmd}
		if cmd == "backup" {
			args = append(args, "--all")
		}
		code, out := runCLI(args...)
		var v map[string]string
		if code != 1 || json.Unmarshal([]byte(out), &v) != nil || !strings.Contains(v["error"], "exit 1") || !strings.Contains(v["error"], "panel database unreachable") {
			t.Errorf("%s: %d %s", cmd, code, out)
		}
	}
	// status reads only the state file and still works.
	if code, out := runCLI("-config", cfg, "status"); code != 0 {
		t.Errorf("status: %d %s", code, out)
	}
}

func TestSnapshotsByUnitAndServer(t *testing.T) {
	cfg := cliSetup(t)
	if code, out := runCLI("-config", cfg, "backup", "--subscription", "example.test"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := runCLI("-config", cfg, "snapshots", "--subscription", "example.test", "--unit", "mail"); code != 0 || !strings.Contains(out, `"mail"`) {
		t.Fatalf("%d %s", code, out)
	}
	// No _server key exists yet: a JSON error, not a usage error.
	if code, out := runCLI("-config", cfg, "snapshots", "--subscription", "_server"); code != 1 || !strings.Contains(out, "no key for _server") {
		t.Fatalf("%d %s", code, out)
	}
}

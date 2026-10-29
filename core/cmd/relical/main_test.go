package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/job"
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
	if err := json.Unmarshal([]byte(out), &results); err != nil || len(results) != 3 || results[0]["ok"] != true ||
		results[0]["subscription"] != "example.test" || results[0]["unit"] != "mail" ||
		results[1]["action"] != "stats" || results[1]["stored_bytes"] != float64(4096) ||
		results[2]["action"] != "forget" || results[2]["ok"] != true {
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
	if code != 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 5 {
		t.Fatalf("%d %s", code, out)
	}
	for i, u := range []string{"mail", "files", "db", "", ""} {
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

func TestRestoreUnitFlags(t *testing.T) {
	cfg := cliSetup(t)
	for _, args := range [][]string{
		{"restore", "--unit", "db", "--subscription", "example.test"},
		{"restore", "--unit", "db", "--subscription", "example.test", "--database", ""},
		{"restore", "--unit", "db", "--subscription", "example.test", "--database", "example_wp", "--path", "httpdocs"},
		{"restore", "--unit", "files", "--subscription", "example.test", "--domain", "example.test"},
		{"restore", "--unit", "files", "--subscription", "example.test", "--folder", "Sent"},
		{"restore", "--unit", "files", "--path", "httpdocs"},
		{"restore", "--unit", "panel-config", "--subscription", "_server", "--path", "x"},
		{"restore", "--domain", "example.test", "--mailbox", "alice", "--path", "httpdocs"},
		{"ls", "--subscription", "example.test"},
		{"ls", "--snapshot", "01234567"},
	} {
		if code, _ := runCLI(append([]string{"-config", cfg}, args...)...); code != 2 {
			t.Errorf("%q: exit %d, want 2", args, code)
		}
	}
	// A valid combination reaches the restore code: a JSON error here.
	code, out := runCLI("-config", cfg, "restore", "--unit", "db", "--subscription", "example.test", "--database", "mysql")
	if code != 1 || !strings.Contains(out, "not a valid subscription database name") {
		t.Errorf("%d %s", code, out)
	}
}

func TestLsCommand(t *testing.T) {
	cfg := cliSetup(t)
	os.WriteFile(filepath.Join(filepath.Dir(cfg), "keys", "example.test.key"), []byte("k\n"), 0o600)
	code, out := runCLI("-config", cfg, "ls", "--subscription", "example.test", "--snapshot", "fedcba98")
	var res struct {
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Type string `json:"type"`
			Size int    `json:"size"`
		} `json:"entries"`
		Truncated *bool `json:"truncated"`
	}
	if code != 0 || json.Unmarshal([]byte(out), &res) != nil || res.Path != "/" || len(res.Entries) != 1 ||
		res.Entries[0].Name != "index.html" || res.Truncated == nil || *res.Truncated {
		t.Fatalf("%d %s", code, out)
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

func TestAdoptUnderInventoryCommand(t *testing.T) {
	b, _ := json.Marshal(map[string]any{"version": 2, "subscriptions": []any{testSub}})
	cfg := cliSetupWith(t, map[string]any{"inventory_command": []string{testutil.FakeInventory(t, string(b))}})
	keys := filepath.Join(filepath.Dir(cfg), "keys")
	os.WriteFile(filepath.Join(keys, "example.test.key"), []byte("k\n"), 0o600)
	code, out := runCLI("-config", cfg, "backup", "--subscription", "example.test")
	if code != 1 || !strings.Contains(out, "relical adopt --subscription example.test") {
		t.Fatalf("%d %s", code, out)
	}
	if code, _ := runCLI("-config", cfg, "adopt"); code != 2 {
		t.Errorf("adopt without --subscription: %d", code)
	}
	code, out = runCLI("-config", cfg, "adopt", "--subscription", "example.test")
	if code != 0 || !strings.Contains(out, `"action": "adopt"`) || !strings.Contains(out, `recorded id \"17\"`) {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := runCLI("-config", cfg, "backup", "--subscription", "example.test"); code != 0 {
		t.Fatalf("after adopt: %d %s", code, out)
	}
}

func TestPanelConfigCommands(t *testing.T) {
	bin := testutil.FakeCommand(t, "pleskbackup", `printf 'panel configuration tar\n' > "$2"`)
	cfg := cliSetupWith(t, map[string]any{
		"subscriptions": []any{testSub},
		"server":        map[string]any{"features": []string{"panel-config"}, "config_command": []string{bin, "-output-file", "{output}"}, "config_ext": "tar"},
	})
	var results []map[string]any
	code, out := runCLI("-config", cfg, "backup", "--subscription", "_server")
	if code != 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 1 ||
		results[0]["subscription"] != "_server" || results[0]["unit"] != "panel-config" || results[0]["snapshot"] == nil {
		t.Fatalf("%d %s", code, out)
	}
	code, out = runCLI("-config", cfg, "backup", "--all", "--unit", "panel-config")
	if code != 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 2 || results[0]["unit"] != "panel-config" || results[1]["action"] != "stats" {
		t.Fatalf("%d %s", code, out)
	}
	code, out = runCLI("-config", cfg, "backup", "--all")
	if code != 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 6 || results[1]["unit"] != "panel-config" ||
		results[4]["action"] != "forget" || results[5]["action"] != "forget" || results[5]["subscription"] != "_server" {
		t.Fatalf("%d %s", code, out)
	}
	for _, cmd := range []string{"forget", "check"} {
		code, out = runCLI("-config", cfg, cmd, "--all")
		if code != 0 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 2 || results[1]["subscription"] != "_server" {
			t.Fatalf("%s: %d %s", cmd, code, out)
		}
	}
	if code, out := runCLI("-config", cfg, "forget", "--subscription", "_server"); code != 0 || !strings.Contains(out, `"_server"`) {
		t.Fatalf("%d %s", code, out)
	}
	if code, out := runCLI("-config", cfg, "snapshots", "--subscription", "_server", "--unit", "panel-config"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	code, out = runCLI("-config", cfg, "status")
	if code != 0 || !strings.Contains(out, `"panel-config"`) {
		t.Fatalf("%d %s", code, out)
	}
	left, _ := os.ReadDir(filepath.Join(filepath.Dir(cfg), "state", "staging"))
	if len(left) != 0 {
		t.Errorf("staging not empty: %v", left)
	}
}

func TestAdoptTakesTheRunLock(t *testing.T) {
	cfg := cliSetup(t)
	os.MkdirAll(filepath.Join(filepath.Dir(cfg), "state"), 0o711)
	unlock, err := job.Lock(filepath.Join(filepath.Dir(cfg), "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	code, out := runCLI("-config", cfg, "adopt", "--subscription", "example.test")
	if code != 1 || !strings.Contains(out, "another relical run holds the lock") {
		t.Fatalf("%d %s", code, out)
	}
}

// Stale panel configuration files are removed by every command that
// takes the run lock, and never by one that does not (a backup may be
// writing one at that moment).
func TestLockedCommandsRemoveStaleStaging(t *testing.T) {
	cfg := cliSetup(t)
	staging := filepath.Join(filepath.Dir(cfg), "state", "staging")
	stale := filepath.Join(staging, "panel-config-20261014T020000Z.tar")
	put := func() {
		os.MkdirAll(staging, 0o700)
		os.WriteFile(stale, []byte("old"), 0o600)
	}
	put()
	runCLI("-config", cfg, "snapshots", "--subscription", "example.test")
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("an unlocked command removed a staging file")
	}
	for _, args := range [][]string{
		{"forget", "--all"},
		{"check", "--subscription", "example.test"},
		{"backup", "--subscription", "example.test", "--unit", "mail"},
		{"adopt", "--subscription", "example.test"},
	} {
		put()
		runCLI(append([]string{"-config", cfg}, args...)...)
		if _, err := os.Stat(stale); err == nil {
			t.Errorf("%q kept the stale staging file", args)
		}
	}
}

func TestReportCommand(t *testing.T) {
	cfg := cliSetup(t)
	if code, out := runCLI("-config", cfg, "backup", "--all"); code != 0 {
		t.Fatalf("%d %s", code, out)
	}
	code, out := runCLI("-config", cfg, "report")
	var rep job.Report
	if code != 0 || json.Unmarshal([]byte(out), &rep) != nil || rep.Subscriptions != 1 || rep.OK != 1 ||
		rep.StoredBytes != 4096 || len(rep.Rows) != 1 || rep.Rows[0].Unit != "mail" {
		t.Fatalf("%d %s", code, out)
	}
	if code, _ := runCLI("-config", cfg, "report", "--all"); code != 2 {
		t.Errorf("report --all: exit %d, want 2", code)
	}
}

func TestReportSurvivesInventoryFailure(t *testing.T) {
	cfg := cliSetupWith(t, map[string]any{"inventory_command": []string{testutil.FakeInventoryFailing(t)}})
	code, out := runCLI("-config", cfg, "report")
	if code != 0 || !strings.Contains(out, "the inventory could not be loaded") {
		t.Fatalf("%d %s", code, out)
	}
	// An unreadable state file is the one error.
	var c map[string]any
	b, _ := os.ReadFile(cfg)
	json.Unmarshal(b, &c)
	state := c["state_dir"].(string)
	os.MkdirAll(state, 0o700)
	os.WriteFile(filepath.Join(state, "state.json"), []byte("{not json"), 0o600)
	if code, out := runCLI("-config", cfg, "report"); code != 1 || !strings.Contains(out, `"error"`) {
		t.Fatalf("%d %s", code, out)
	}
}

func TestRunLogWritten(t *testing.T) {
	cfg := cliSetup(t)
	runCLI("-config", cfg, "backup", "--all")
	runCLI("-config", cfg, "check", "--subscription", "nope.test") // fails before any result
	runCLI("-config", cfg, "status")                               // not logged
	var c map[string]any
	b, _ := os.ReadFile(cfg)
	json.Unmarshal(b, &c)
	raw, err := os.ReadFile(filepath.Join(c["state_dir"].(string), job.LogName))
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, ln := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var l job.LogLine
		if err := json.Unmarshal([]byte(ln), &l); err != nil {
			t.Fatalf("%v: %s", err, ln)
		}
		ev := l.Command + " " + l.Event
		if l.Code != nil {
			ev += fmt.Sprintf(" %d", *l.Code)
		}
		events = append(events, ev)
	}
	want := "backup start,backup result,backup result,backup result,backup end 0,check start,check result,check end 1"
	if got := strings.Join(events, ","); got != want {
		t.Errorf("log events\n got %s\nwant %s", got, want)
	}
}

// backup --all applies retention at the end, unless it was interrupted,
// --no-forget was given or the run was for one unit.
func TestBackupAllRunsRetentionUnlessInterrupted(t *testing.T) {
	cfg := cliSetup(t)
	forgets := func() int {
		b, _ := os.ReadFile(os.Getenv("FAKE_LOG"))
		return strings.Count(string(b), "forget --group-by tags")
	}
	if code, out := runCLI("-config", cfg, "backup", "--all"); code != 0 || forgets() != 1 {
		t.Fatalf("%d forgets %d: %s", code, forgets(), out)
	}
	for _, args := range [][]string{{"--no-forget"}, {"--unit", "mail"}} {
		if code, out := runCLI(append([]string{"-config", cfg, "backup", "--all"}, args...)...); code != 0 || forgets() != 1 {
			t.Fatalf("%q: %d forgets %d: %s", args, code, forgets(), out)
		}
	}
	if code, _ := runCLI("-config", cfg, "backup", "--subscription", "example.test", "--no-forget"); code != 2 {
		t.Errorf("--no-forget without --all: exit %d", code)
	}
	// A stop (SIGTERM) during the backup: exit 4 and no forget afterwards.
	t.Setenv("FAKE_HANG", "1")
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	var out, errb bytes.Buffer
	if code := run(ctx, []string{"-config", cfg, "backup", "--all"}, &out, &errb); code != exitInterrupted || exitInterrupted != 4 {
		t.Fatalf("interrupted run: exit %d %s", code, out.String())
	}
	if forgets() != 1 || !strings.Contains(errb.String(), "interrupted") {
		t.Errorf("forgets %d after an interrupted run; stderr %s", forgets(), errb.String())
	}
	// The run log records the same exit code.
	var c map[string]any
	b, _ := os.ReadFile(cfg)
	json.Unmarshal(b, &c)
	raw, _ := os.ReadFile(filepath.Join(c["state_dir"].(string), job.LogName))
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var last job.LogLine
	json.Unmarshal([]byte(lines[len(lines)-1]), &last)
	if last.Event != "end" || last.Code == nil || *last.Code != exitInterrupted {
		t.Errorf("last log line after an interrupted run: %s", lines[len(lines)-1])
	}
}

// An invalid inventory entry no longer stops the run: the valid
// subscriptions are backed up, the invalid one is a failed result (exit
// 1, not in the state file) and a failed row in the report.
func TestInvalidSubscriptionIsLeftOutOfTheRun(t *testing.T) {
	bad := map[string]any{}
	for k, v := range testSub {
		bad[k] = v
	}
	bad["id"], bad["name"], bad["mail_domains"] = "18", "bad.example.test", []string{}
	bad["features"], bad["databases"] = []string{"db"}, []any{map[string]any{"type": "mysql", "name": "sys"}}
	bad["home"], bad["files"] = "/var/www/vhosts/bad.example.test", []string{}
	cfg := cliSetupWith(t, map[string]any{"subscriptions": []any{testSub, bad}})
	code, out := runCLI("-config", cfg, "backup", "--all", "--no-forget")
	var results []map[string]any
	if code != 1 || json.Unmarshal([]byte(out), &results) != nil || len(results) != 3 ||
		results[0]["subscription"] != "bad.example.test" || results[0]["ok"] != false ||
		!strings.Contains(results[0]["error"].(string), `"sys" is a server or panel database`) ||
		results[1]["subscription"] != "example.test" || results[1]["ok"] != true {
		t.Fatalf("%d %s", code, out)
	}
	if _, out := runCLI("-config", cfg, "status"); strings.Contains(out, "bad.example.test") {
		t.Errorf("the invalid entry is in the state file: %s", out)
	}
	_, out = runCLI("-config", cfg, "report")
	var rep job.Report
	if json.Unmarshal([]byte(out), &rep) != nil || rep.Failed != 1 || rep.OK != 1 {
		t.Fatalf("%s", out)
	}
	found := false
	for _, r := range rep.Rows {
		found = found || (r.Subscription == "bad.example.test" && r.Unit == "inventory" && !r.OK && strings.Contains(r.Error, "invalid inventory entry"))
	}
	if !found {
		t.Errorf("no failed inventory row: %s", out)
	}
	// The run log names what the inventory left out.
	var c map[string]any
	b, _ := os.ReadFile(cfg)
	json.Unmarshal(b, &c)
	raw, _ := os.ReadFile(filepath.Join(c["state_dir"].(string), job.LogName))
	if !strings.Contains(string(raw), `"event":"skipped","command":"backup","result":[{"name":"bad.example.test","reason":"invalid inventory entry: `) {
		t.Errorf("no skipped line in the run log:\n%s", raw)
	}
	// Restores and listings of the valid subscription still work.
	if code, out := runCLI("-config", cfg, "snapshots", "--subscription", "example.test"); code != 0 {
		t.Errorf("%d %s", code, out)
	}
	if code, out := runCLI("-config", cfg, "snapshots", "--subscription", "bad.example.test"); code != 1 || !strings.Contains(out, "not in the inventory") {
		t.Errorf("%d %s", code, out)
	}
}

// check --all waits for a run lock that is released within the wait, and
// gives up once the wait is over; other commands fail at once.
func TestCheckAllWaitsForTheRunLock(t *testing.T) {
	cfg := cliSetup(t)
	oldPoll, oldWait := job.LockPoll, checkLockWait
	job.LockPoll, checkLockWait = 50*time.Millisecond, 3*time.Second
	t.Cleanup(func() { job.LockPoll, checkLockWait = oldPoll, oldWait })
	state := filepath.Join(filepath.Dir(cfg), "state")
	os.MkdirAll(state, 0o711)
	unlock, err := job.Lock(state)
	if err != nil {
		t.Fatal(err)
	}
	if code, out := runCLI("-config", cfg, "check", "--subscription", "example.test"); code != 1 || !strings.Contains(out, "holds the lock") {
		t.Fatalf("check --subscription: %d %s", code, out)
	}
	time.AfterFunc(400*time.Millisecond, unlock)
	start := time.Now()
	code, out := runCLI("-config", cfg, "check", "--all")
	if code != 0 || time.Since(start) < 300*time.Millisecond {
		t.Fatalf("check --all: %d after %s: %s", code, time.Since(start), out)
	}
	unlock, err = job.Lock(state)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	checkLockWait = 300 * time.Millisecond
	if code, out := runCLI("-config", cfg, "check", "--all"); code != 1 || !strings.Contains(out, "waited 300ms") {
		t.Fatalf("check --all after the wait: %d %s", code, out)
	}
	// A stop while it waits is an interruption (exit 4), not a failure.
	checkLockWait = 3 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	var stdout, errb bytes.Buffer
	if code := run(ctx, []string{"-config", cfg, "check", "--all"}, &stdout, &errb); code != exitInterrupted {
		t.Fatalf("check --all stopped while waiting: %d %s %s", code, stdout.String(), errb.String())
	}
}

package job

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
)

// reportSamplePath is the core's report for a sample state; the Plesk
// extension's ReportTest reads a copy of it (reportFixturePath), so the
// extension is tested against what this core prints, not a hand-written
// shape. Regenerate both with
//
//	RELICAL_WRITE_REPORT_SAMPLE=1 go test -run TestReportSample ./internal/job/
const (
	reportSamplePath  = "testdata/report-sample.json"
	reportFixturePath = "../../../panels/plesk/tests/fixtures/report.json"
)

// sampleReport builds the report `relical report` prints for a sample
// state and inventory: OK, failed and skipped-database rows, a failed row
// for an invalid inventory entry, the adapter's skipped list, a rename
// and warnings. It goes through inventory.Load (an inventory_command) and
// ReadState as the command does, and is encoded as the command encodes.
func sampleReport(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	webspace := func(name, id string, dbs ...map[string]string) map[string]any {
		return map[string]any{
			"id": id, "name": name, "sys_user": "u" + id, "uid": 10000, "gid": 1004,
			"home": "/var/www/vhosts/" + name, "features": []string{"mail", "files", "db"},
			"mail_domains": []string{name}, "files": []string{"/var/www/vhosts/" + name},
			"databases": dbs,
		}
	}
	other := webspace("other.example.test", "18", map[string]string{"type": "pgsql", "name": "legacy"})
	other["features"] = []string{"files", "db"}
	other["mail_domains"] = []string{}
	renamed := webspace("new.example.test", "19")
	renamed["features"] = []string{"mail"}
	renamed["renamed_from"] = "old.example.test"
	renamed["databases"] = []map[string]string{}
	invalid := webspace("bad.example.test", "20", map[string]string{"type": "mysql", "name": "phpmyadmin_old"})
	out := map[string]any{
		"version": config.Version,
		"subscriptions": []any{
			webspace("example.test", "17", map[string]string{"type": "mysql", "name": "example_wp"}),
			other, renamed, invalid,
		},
		"skipped": []map[string]string{
			{"name": "mailonly.example.test", "reason": "nothing to back up: the subscription has no system user (no web hosting)"},
		},
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	invFile := filepath.Join(dir, "inventory.json")
	if err := os.WriteFile(invFile, b, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Version:            config.Version,
		StateDir:           dir,
		Roots:              config.Roots{Files: []string{"/var/www/vhosts"}, Mail: []string{"/var/qmail/mailnames"}},
		DefaultDestination: "d",
		Destinations:       map[string]config.Destination{"d": {Repository: "/repo/{subscription}", Prune: "local"}},
		InventoryCommand:   []string{"/bin/cat", invFile},
	}

	now := time.Date(2026, 10, 29, 6, 0, 0, 0, time.UTC)
	at := func(d time.Duration) time.Time { return now.Add(-d) }
	res := func(sub, unit, db, action string, ok bool, when time.Time) Result {
		return Result{Subscription: sub, Unit: unit, Database: db, Action: action, OK: ok, Time: when}
	}
	stats := res("example.test", "", "", "stats", true, at(5*time.Hour))
	stats.StoredBytes = 1024
	failed := res("example.test", DBTag, "example_wp", "backup", false, at(4*time.Hour+57*time.Minute))
	failed.Error = "mysqldump: exit 2"
	skippedDB := res("other.example.test", DBTag, "legacy", "backup", true, at(5*time.Hour))
	skippedDB.Note = "skipped: type pgsql not supported"
	otherStats := res("other.example.test", "", "", "stats", true, at(5*time.Hour))
	otherStats.StoredBytes = 512
	results := []Result{
		res("example.test", MailTag, "", "backup", true, at(4*time.Hour+59*time.Minute)),
		res("example.test", FilesTag, "", "backup", true, at(4*time.Hour+58*time.Minute)),
		failed, stats,
		res("example.test", "", "", "check", true, at(3*24*time.Hour)),
		res("other.example.test", FilesTag, "", "backup", true, at(3*24*time.Hour+4*time.Hour)),
		skippedDB, otherStats,
		res("other.example.test", "", "", "check", true, at(11*24*time.Hour)),
		res("new.example.test", MailTag, "", "backup", true, at(5*time.Hour)),
	}
	st := State{}
	for _, r := range results {
		k := r.StateKey()
		if st[r.Subscription] == nil {
			st[r.Subscription] = map[string]map[string]Result{}
		}
		if st[r.Subscription][k] == nil {
			st[r.Subscription][k] = map[string]Result{}
		}
		st[r.Subscription][k][r.Action] = r
	}
	if b, err = json.Marshal(st); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}

	// What report() in cmd/relical does.
	read, err := ReadState(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	var inv *inventory.Inventory
	loaded, invErr := inventory.Load(context.Background(), cfg)
	if invErr == nil {
		inv = &loaded
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(BuildReport(read, inv, invErr, now)); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestReportSample(t *testing.T) {
	got := sampleReport(t)

	var rep Report
	if err := json.Unmarshal(got, &rep); err != nil {
		t.Fatal(err)
	}
	units := map[string]bool{}
	for _, r := range rep.Rows {
		units[r.Subscription+" "+r.Unit] = r.OK
	}
	if ok, has := units["example.test mail"]; !has || !ok {
		t.Errorf("no OK row: %+v", rep.Rows)
	}
	if ok, has := units["example.test db:example_wp"]; !has || ok {
		t.Errorf("no failed row: %+v", rep.Rows)
	}
	if ok, has := units["bad.example.test "+InventoryUnit]; !has || ok {
		t.Errorf("no failed inventory row: %+v", rep.Rows)
	}
	if rep.SkippedRows != 1 || len(rep.Skipped) != 2 || len(rep.Renamed) != 1 || len(rep.Warnings) < 2 {
		t.Errorf("sample lacks a case: skipped_rows %d, skipped %+v, renamed %+v, warnings %q", rep.SkippedRows, rep.Skipped, rep.Renamed, rep.Warnings)
	}

	if os.Getenv("RELICAL_WRITE_REPORT_SAMPLE") == "1" {
		for _, p := range []string{reportSamplePath, reportFixturePath} {
			if err := os.WriteFile(p, got, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return
	}
	for _, p := range []string{reportSamplePath, reportFixturePath} {
		want, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is not this core's report; regenerate it with RELICAL_WRITE_REPORT_SAMPLE=1 go test -run TestReportSample ./internal/job/\ngot:\n%s", p, got)
		}
	}
}

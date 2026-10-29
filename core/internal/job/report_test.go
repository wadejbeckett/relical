package job

import (
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/inventory"
)

// TestReportShape pins the report's keys, which the panel adapters read
// (the Plesk extension's Relical\Report).
func TestReportShape(t *testing.T) {
	now := time.Date(2026, 10, 29, 6, 0, 0, 0, time.UTC)
	st := State{"example.test": {"mail": {"backup": {Subscription: "example.test", Unit: "mail", Action: "backup", OK: true, Time: now}}}}
	inv := &inventory.Inventory{Renamed: []inventory.Rename{{Subscription: "new.test", RenamedFrom: "old.test", Note: "n"}}}
	b, err := json.Marshal(BuildReport(st, inv, nil, now))
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		t.Fatal(err)
	}
	keySet := func(m map[string]json.RawMessage) []string {
		var ks []string
		for k := range m {
			ks = append(ks, k)
		}
		sort.Strings(ks)
		return ks
	}
	want := []string{"failed", "generated", "ok", "renamed", "rows", "skipped", "stored_bytes", "subscriptions", "warnings"}
	if got := keySet(top); !reflect.DeepEqual(got, want) {
		t.Errorf("report keys %v, want %v", got, want)
	}
	var rows, renamed []map[string]json.RawMessage
	json.Unmarshal(top["rows"], &rows)
	json.Unmarshal(top["renamed"], &renamed)
	if len(rows) != 1 || !reflect.DeepEqual(keySet(rows[0]), []string{"error", "ok", "stored_bytes", "subscription", "time", "unit"}) {
		t.Errorf("row keys: %s", top["rows"])
	}
	if len(renamed) != 1 || !reflect.DeepEqual(keySet(renamed[0]), []string{"name", "note", "renamed_from"}) {
		t.Errorf("renamed keys: %s", top["renamed"])
	}
	// Empty lists are [], never null, so an adapter can iterate them.
	b, _ = json.Marshal(BuildReport(State{}, nil, nil, now))
	for _, k := range []string{`"rows":[]`, `"renamed":[]`, `"warnings":[]`} {
		if !strings.Contains(string(b), k) {
			t.Errorf("empty report lacks %s: %s", k, b)
		}
	}
}

func TestReportCountsAndWarnings(t *testing.T) {
	now := time.Date(2026, 10, 29, 6, 0, 0, 0, time.UTC)
	recent, old := now.Add(-5*time.Hour), now.Add(-72*time.Hour)
	res := func(sub, unit, action string, ok bool, at time.Time) Result {
		return Result{Subscription: sub, Unit: unit, Action: action, OK: ok, Time: at}
	}
	pg := res("b.test", "db", "backup", true, recent)
	pg.Database, pg.Note = "pg", "skipped: type postgresql not supported"
	failedDB := res("b.test", "db", "backup", false, recent)
	failedDB.Database, failedDB.Error = "shop", "mysqldump: exit 2"
	st := State{}
	put := func(r Result) {
		k := r.StateKey()
		if st[r.Subscription] == nil {
			st[r.Subscription] = map[string]map[string]Result{}
		}
		if st[r.Subscription][k] == nil {
			st[r.Subscription][k] = map[string]Result{}
		}
		st[r.Subscription][k][r.Action] = r
	}
	stats := func(sub string, n uint64) Result {
		r := res(sub, "", "stats", true, recent)
		r.StoredBytes = n
		return r
	}
	put(res("b.test", "mail", "backup", true, recent))
	put(res("b.test", "files", "backup", true, old)) // older than 48 h
	put(pg)
	put(failedDB)
	put(stats("b.test", 1000))
	put(res("b.test", "", "check", true, now.Add(-11*24*time.Hour))) // older than 10 days
	put(res("a.test", "mail", "backup", true, recent))
	put(res("a.test", "mail", "restore", true, recent)) // not a backup: no row
	put(stats("a.test", 24))
	put(res("gone.test", "mail", "backup", true, recent))
	put(res("_server", "panel-config", "backup", true, recent))
	put(stats("_server", 500))
	inv := &inventory.Inventory{Subscriptions: []inventory.Subscription{{Name: "a.test"}, {Name: "b.test"}},
		Renamed: []inventory.Rename{{Subscription: "b.test", RenamedFrom: "bee.test", Note: "the old repository is kept"}}}

	rep := BuildReport(st, inv, nil, now)
	if rep.Subscriptions != 3 || rep.OK != 5 || rep.Failed != 1 || rep.Skipped != 1 || rep.StoredBytes != 1524 {
		t.Errorf("counts: %+v", rep)
	}
	var order []string
	for _, r := range rep.Rows {
		order = append(order, r.Subscription+" "+r.Unit)
	}
	wantOrder := []string{"_server panel-config", "a.test mail", "b.test db:pg", "b.test db:shop", "b.test files", "b.test mail", "gone.test mail"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("rows %q, want %q", order, wantOrder)
	}
	if r := rep.Rows[3]; r.OK || r.Error != "mysqldump: exit 2" || r.StoredBytes != 1000 || r.Time != "2026-10-29T01:00:00Z" {
		t.Errorf("failed row: %+v", r)
	}
	if len(rep.Renamed) != 1 || rep.Renamed[0] != (ReportRenamed{Name: "b.test", RenamedFrom: "bee.test", Note: "the old repository is kept"}) {
		t.Errorf("renamed: %+v", rep.Renamed)
	}
	all := strings.Join(rep.Warnings, "\n")
	for _, w := range []string{
		"b.test files: the last backup (2026-10-26T06:00:00Z) is older than 48 hours",
		"b.test: the last check (2026-10-18T06:00:00Z) is older than 10 days",
		"gone.test: in the state file but no longer in the inventory",
	} {
		if !strings.Contains(all, w) {
			t.Errorf("warnings lack %q:\n%s", w, all)
		}
	}
	if len(rep.Warnings) != 3 {
		t.Errorf("want 3 warnings:\n%s", all)
	}

	// An inventory that cannot be loaded is a warning, and the rest of the
	// report still renders.
	rep = BuildReport(st, nil, errors.New("inventory_command: exit 1"), now)
	if len(rep.Rows) != 7 || len(rep.Renamed) != 0 || !strings.Contains(strings.Join(rep.Warnings, "\n"), "the inventory could not be loaded") ||
		strings.Contains(strings.Join(rep.Warnings, "\n"), "no longer in the inventory") {
		t.Errorf("without an inventory: %+v", rep)
	}
}

func TestReportNeverChecked(t *testing.T) {
	now := time.Date(2026, 10, 29, 6, 0, 0, 0, time.UTC)
	st := State{"a.test": {"mail": {"backup": {Subscription: "a.test", Unit: "mail", Action: "backup", OK: true, Time: now.Add(-11 * 24 * time.Hour)}}}}
	rep := BuildReport(st, &inventory.Inventory{Subscriptions: []inventory.Subscription{{Name: "a.test"}}}, nil, now)
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "a.test: backed up since 2026-10-18T06:00:00Z but never checked") {
		t.Errorf("%q", rep.Warnings)
	}
}

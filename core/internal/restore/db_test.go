package restore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRestoreDBHappyPath(t *testing.T) {
	e, _, log := webSetup(t)
	res, err := RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "latest"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home(e), "restored-2026-10-21-bbbbbbbb")
	want := filepath.Join(dir, "example_wp.sql")
	if res.RestoredTo != want || res.Snapshot != dbSnap || res.Bytes == 0 {
		t.Errorf("%+v", res)
	}
	b, err := os.ReadFile(want)
	if err != nil || !strings.Contains(string(b), "-- dump of /example_wp.sql") || int64(len(b)) != res.Bytes {
		t.Fatalf("%q %v", b, err)
	}
	if fi, _ := os.Stat(want); fi.Mode().Perm() != 0o600 {
		t.Errorf("dump mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("folder mode %v", fi.Mode())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("folder holds %v", entries)
	}
	if !strings.Contains(log(), "dump "+dbSnap+" /example_wp.sql") {
		t.Errorf("%s", log())
	}
	// Same snapshot again: refused.
	if _, err := RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "latest"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("second restore: %v", err)
	}
}

func TestRestoreDBDroppedSinceIsRestorable(t *testing.T) {
	e, _, _ := webSetup(t)
	e.Inv.Subscriptions[0].Databases = nil // dropped since the backup
	if _, err := RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "bbbbbbbb"}); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreDBRefusesForeignName(t *testing.T) {
	e, _, log := webSetup(t)
	for _, tc := range []struct{ db, snap, want string }{
		{"other_wp", "latest", "no backup of database other_wp"}, // another subscription's: not in this repository
		{"mysql", "latest", "not a valid"},
		{"psa", "latest", "not a valid"},
		{"../x", "latest", "not a valid"},
		{"-x", "latest", "not a valid"},
		{"-rx", "latest", "not a valid"},
		{"", "latest", "not a valid"},
		{"example_wp", "aaaaaaaa", "not found among the backups of database example_wp"}, // a files snapshot
	} {
		_, err := RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: tc.db, Snapshot: tc.snap})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v", tc.db, err)
		}
	}
	if strings.Contains(log(), "dump ") {
		t.Errorf("dumped:\n%s", log())
	}
	if entries, _ := os.ReadDir(home(e)); len(entries) != 1 {
		t.Errorf("home holds %v", entries)
	}
}

func TestRestoreDBFailedDumpLeavesNothing(t *testing.T) {
	e, env, _ := webSetup(t)
	env["FAKE_DUMP_EXIT"] = "1"
	_, err := RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "connection lost") {
		t.Fatalf("err = %v", err)
	}
	entries, _ := os.ReadDir(home(e))
	for _, en := range entries {
		if en.Name() != "httpdocs" {
			t.Errorf("left behind: %s", en.Name())
		}
	}
	// And the restore can be run again.
	delete(env, "FAKE_DUMP_EXIT")
	if _, err := RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "latest"}); err != nil {
		t.Fatal(err)
	}
}

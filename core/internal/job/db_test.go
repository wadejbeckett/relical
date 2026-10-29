package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wadejbeckett/relical/core/internal/inventory"
)

func dbSub(t *testing.T, e Env, names ...string) inventory.Subscription {
	t.Helper()
	sub := mustSub(t, e, "example.test")
	sub.Features = []string{"db"}
	sub.Databases = nil
	for _, n := range names {
		sub.Databases = append(sub.Databases, inventory.Database{Type: "mysql", Name: n})
	}
	return sub
}

func TestDumpStreamsThroughStdin(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_NO_SQL_UNDER"] = e.Cfg.StateDir + " " + os.TempDir()
	rs := e.BackupDatabases(context.Background(), dbSub(t, e, "example_wp"))
	if len(rs) != 1 || !rs[0].OK || rs[0].Unit != "db" || rs[0].Database != "example_wp" || rs[0].Snapshot == "" {
		t.Fatalf("%+v", rs)
	}
	l := logLines(log())
	got := l[len(l)-1]
	want := "backup --json --host host.example.test --stdin-from-command --stdin-filename example_wp.sql --tag db --tag db:example_wp -- " +
		e.Cfg.MysqldumpBin + " --defaults-file=" + e.Cfg.MySQL.DefaultsFile +
		" --single-transaction --quick --routines --triggers --events --no-tablespaces -- example_wp"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if strings.Contains(log(), "SQL FILE ON DISK") {
		t.Errorf("a dump was written to disk:\n%s", log())
	}
	stdin, _ := os.ReadFile(env["FAKE_LOG"] + ".stdin")
	if !strings.Contains(string(stdin), "-- fake dump of example_wp") || !strings.Contains(string(stdin), "INSERT INTO t") {
		t.Errorf("restic did not receive the dump on stdin: %q", stdin)
	}
}

// The fake restic's disk check must notice a dump file, or
// TestDumpStreamsThroughStdin proves nothing.
func TestDumpOnDiskWouldBeNoticed(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_NO_SQL_UNDER"] = e.Cfg.StateDir
	if err := os.WriteFile(filepath.Join(e.Cfg.StateDir, "restore", "example_wp.sql"), []byte("-- fake dump of example_wp ("+env["FAKE_TOKEN"]+")\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.BackupDatabases(context.Background(), dbSub(t, e, "example_wp"))
	if !strings.Contains(log(), "SQL FILE ON DISK: "+filepath.Join(e.Cfg.StateDir, "restore", "example_wp.sql")) {
		t.Fatalf("planted dump not found:\n%s", log())
	}
}

func TestDumpFailureIsPerDatabase(t *testing.T) {
	e, env, _ := setup(t, "local")
	env["FAKE_MYSQLDUMP_FAIL"] = "broken_db"
	rs := e.BackupDatabases(context.Background(), dbSub(t, e, "broken_db", "example_wp"))
	if len(rs) != 2 {
		t.Fatalf("%+v", rs)
	}
	if rs[0].Database != "broken_db" || rs[0].OK || rs[0].Snapshot != "" || !strings.Contains(rs[0].Error, "command failed") ||
		!strings.Contains(rs[0].Error, "Unknown database") {
		t.Errorf("failed database: %+v", rs[0])
	}
	if rs[1].Database != "example_wp" || !rs[1].OK || rs[1].Snapshot == "" {
		t.Errorf("other database: %+v", rs[1])
	}
	dumps, _ := os.ReadFile(env["FAKE_LOG"] + ".mysqldump")
	if strings.Count(string(dumps), "\n") != 2 {
		t.Errorf("mysqldump calls:\n%s", dumps)
	}
}

func TestDefaultsFileIsCheckedFirst(t *testing.T) {
	for name, prep := range map[string]func(Env){
		"missing":  func(e Env) { os.Remove(e.Cfg.MySQL.DefaultsFile) },
		"readable": func(e Env) { os.Chmod(e.Cfg.MySQL.DefaultsFile, 0o644) },
		"unset":    func(e Env) { e.Cfg.MySQL.DefaultsFile = "" },
		"dir":      func(e Env) { os.Remove(e.Cfg.MySQL.DefaultsFile); os.Mkdir(e.Cfg.MySQL.DefaultsFile, 0o700) },
		"group":    func(e Env) { os.Chmod(e.Cfg.MySQL.DefaultsFile, 0o640) },
		"exec":     func(e Env) { os.Chmod(e.Cfg.MySQL.DefaultsFile, 0o700) },
		"symlink": func(e Env) {
			target := e.Cfg.MySQL.DefaultsFile + ".real"
			os.Rename(e.Cfg.MySQL.DefaultsFile, target)
			os.Symlink(target, e.Cfg.MySQL.DefaultsFile)
		},
		// The test's files belong to the test user, never root.
		"not root's": func(Env) { asRoot = func() bool { return true } },
	} {
		t.Run(name, func(t *testing.T) {
			if os.Geteuid() == 0 && name == "not root's" {
				t.Skip("running as root: the file is root's")
			}
			defer func(f func() bool) { asRoot = f }(asRoot)
			e, _, log := setup(t, "local")
			prep(e)
			rs := e.BackupDatabases(context.Background(), dbSub(t, e, "a", "b"))
			if len(rs) != 2 {
				t.Fatalf("%+v", rs)
			}
			for _, r := range rs {
				if r.OK || !strings.Contains(r.Error, "mysql.defaults_file") {
					t.Errorf("%+v", r)
				}
			}
			if log() != "" {
				t.Errorf("restic ran:\n%s", log())
			}
		})
	}
}

func TestDefaultsFileStricterModeIsAccepted(t *testing.T) {
	e, _, _ := setup(t, "local")
	os.Chmod(e.Cfg.MySQL.DefaultsFile, 0o400)
	if err := e.checkDefaultsFile(); err != nil {
		t.Fatal(err)
	}
}

func TestOtherDatabaseTypesAreSkipped(t *testing.T) {
	e, _, log := setup(t, "local")
	sub := dbSub(t, e, "example_wp")
	sub.Databases = append([]inventory.Database{{Type: "postgresql", Name: "pg"}}, sub.Databases...)
	rs := e.BackupDatabases(context.Background(), sub)
	if len(rs) != 2 || rs[0].Database != "pg" || !rs[0].OK || rs[0].Note != "skipped: type postgresql not supported" || !rs[1].OK {
		t.Fatalf("%+v", rs)
	}
	if strings.Count(log(), "--stdin-from-command") != 1 {
		t.Errorf("%s", log())
	}
	// Only unsupported types: no defaults file is needed and restic never runs.
	e2, _, log2 := setup(t, "local")
	e2.Cfg.MySQL.DefaultsFile = ""
	sub = dbSub(t, e2)
	sub.Databases = []inventory.Database{{Type: "postgresql", Name: "pg"}}
	if rs := e2.BackupDatabases(context.Background(), sub); len(rs) != 1 || !rs[0].OK || log2() != "" {
		t.Fatalf("%+v\n%s", rs, log2())
	}
}

func TestNoDatabasesIsOK(t *testing.T) {
	e, _, log := setup(t, "local")
	rs := e.BackupDatabases(context.Background(), dbSub(t, e))
	if len(rs) != 1 || !rs[0].OK || rs[0].Note == "" || log() != "" {
		t.Fatalf("%+v", rs)
	}
}

func TestLeadingDashDatabaseIsRefused(t *testing.T) {
	e, env, _ := setup(t, "local")
	rs := e.BackupDatabases(context.Background(), dbSub(t, e, "-x", "example_wp"))
	if len(rs) != 2 || rs[0].OK || !strings.Contains(rs[0].Error, "starts with -") || !rs[1].OK {
		t.Fatalf("%+v", rs)
	}
	if b, _ := os.ReadFile(env["FAKE_LOG"] + ".mysqldump"); strings.Contains(string(b), " -x") {
		t.Errorf("mysqldump got -x: %s", b)
	}
}

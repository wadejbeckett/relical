package job

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"

	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

// mysqldumpFlags give a consistent InnoDB dump without locking the
// tables, streamed row by row, with routines, triggers and events, and
// without the tablespace statements that need the PROCESS privilege.
var mysqldumpFlags = []string{"--single-transaction", "--quick", "--routines", "--triggers", "--events", "--no-tablespaces"}

// DumpFilename is the file name a database's dump has in its snapshot.
func DumpFilename(db string) string { return db + ".sql" }

// MysqldumpCommand is the dump command for one database.
func (e Env) MysqldumpCommand(db string) []string {
	cmd := []string{e.Cfg.MysqldumpBin, "--defaults-file=" + e.Cfg.MySQL.DefaultsFile}
	cmd = append(cmd, mysqldumpFlags...)
	return append(cmd, db)
}

// asRoot reports whether relical runs as root; a variable so that tests
// can check the owner rule without root.
var asRoot = func() bool { return os.Geteuid() == 0 }

// checkDefaultsFile requires mysql.defaults_file to be a regular file, not
// a symlink, with mode 0600 or stricter and, when relical runs as root,
// owned by root: whoever can write it chooses the host, the credentials
// and options (such as result-file) of a mysqldump run as root.
func (e Env) checkDefaultsFile() error {
	p := e.Cfg.MySQL.DefaultsFile
	if p == "" {
		return errors.New("mysql.defaults_file is not set in the configuration")
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("mysql.defaults_file: %w", err)
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("mysql.defaults_file %s is a symlink; give the file itself", p)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("mysql.defaults_file %s is not a regular file", p)
	}
	if fi.Mode().Perm()&^0o600 != 0 {
		return fmt.Errorf("mysql.defaults_file %s has mode %04o; it holds a password, chmod 600", p, fi.Mode().Perm())
	}
	if asRoot() {
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 {
			return fmt.Errorf("mysql.defaults_file %s is not owned by root; chown root %s", p, p)
		}
	}
	return nil
}

// BackupDatabases backs up each of the subscription's databases into its
// own snapshot tagged db and db:<name>. The dump is streamed from
// mysqldump into restic and never written to disk. A failing dump fails
// that database only. Database types other than mysql are skipped.
func (e Env) BackupDatabases(ctx context.Context, sub inventory.Subscription) []Result {
	if !sub.Has(DBTag) {
		res := e.result(sub.Name, DBTag, "backup")
		res.OK = true
		res.Note = "db is not among the subscription's features; nothing to back up"
		return []Result{res}
	}
	if len(sub.Databases) == 0 {
		res := e.result(sub.Name, DBTag, "backup")
		res.OK = true
		res.Note = "the subscription has no databases"
		return []Result{res}
	}
	// What would fail every database is checked once, before anything
	// runs; each database still gets its own result.
	var (
		r     restic.Runner
		err   error
		ready bool
	)
	out := []Result{}
	for _, d := range sub.Databases {
		if d.Type != "mysql" {
			res := e.dbResult(sub, d.Name)
			res.OK = true
			res.Note = "skipped: type " + d.Type + " not supported"
			out = append(out, res)
			continue
		}
		if !ready {
			ready = true
			if err = e.checkDefaultsFile(); err == nil {
				r, err = e.openRepo(ctx, sub)
			}
		}
		if err != nil {
			out = append(out, failed(e.dbResult(sub, d.Name), err))
			continue
		}
		if ctx.Err() != nil {
			break
		}
		out = append(out, e.backupDatabase(ctx, r, sub, d.Name))
	}
	return out
}

func (e Env) dbResult(sub inventory.Subscription, db string) Result {
	res := e.result(sub.Name, DBTag, "backup")
	res.Database = db
	return res
}

func (e Env) backupDatabase(ctx context.Context, r restic.Runner, sub inventory.Subscription, db string) Result {
	res := e.dbResult(sub, db)
	// mysqldump would read a leading - as an option.
	if strings.HasPrefix(db, "-") {
		return failed(res, fmt.Errorf("database name %q starts with -; refused", db))
	}
	sum, err := r.BackupStdin(ctx, e.MysqldumpCommand(db), DumpFilename(db), []string{DBTag, DBTag + ":" + db})
	if err != nil {
		return failed(res, err)
	}
	res.OK = true
	res.Snapshot = sum.SnapshotID
	res.DataAdded = sum.DataAddedPacked
	res.Seconds = sum.TotalDuration
	return res
}

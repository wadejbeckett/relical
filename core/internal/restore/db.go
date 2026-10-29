package restore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

type DBRequest struct {
	Subscription string
	Database     string
	Snapshot     string // "latest", or 8–64 hex characters
}

type DBResult struct {
	Subscription string `json:"subscription"`
	Unit         string `json:"unit"`
	Database     string `json:"database"`
	Snapshot     string `json:"snapshot"`
	RestoredTo   string `json:"restored_to"`
	Bytes        int64  `json:"bytes"`
	Note         string `json:"note"`
}

// withTag keeps the snapshots that carry tag.
func withTag(snaps []restic.Snapshot, tag string) []restic.Snapshot {
	var out []restic.Snapshot
	for _, s := range snaps {
		for _, t := range s.Tags {
			if t == tag {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// RunDB writes a database's dump from a db snapshot to
// home/restored-<date>-<id>/<database>.sql, mode 0600, owned by the
// subscription's user. It never imports the dump.
//
// The database may be one the subscription no longer has (dropped or
// removed from the panel since): what makes it restorable is a snapshot
// tagged db:<database> in the subscription's own repository, which only
// a backup of that subscription's database can have written. The name
// must still pass the inventory's database name rule.
func RunDB(ctx context.Context, env job.Env, req DBRequest) (DBResult, error) {
	res := DBResult{Subscription: req.Subscription, Unit: job.DBTag, Database: req.Database}
	// Best effort: a failure here never stops this restore.
	_ = CleanStale(env.Cfg, env.Now())
	if !snapshotRE.MatchString(req.Snapshot) {
		return res, fmt.Errorf("snapshot must be latest or 8-64 hex characters, got %q", req.Snapshot)
	}
	if !inventory.ValidDatabaseName(req.Database) {
		return res, fmt.Errorf("database %q is not a valid subscription database name", req.Database)
	}
	sub, err := subscription(env, req.Subscription)
	if err != nil {
		return res, err
	}
	ws, err := openWebspace(env.Cfg, sub)
	if err != nil {
		return res, err
	}
	defer ws.Close()
	r, _, err := env.Runner(sub)
	if err != nil {
		return res, err
	}
	tag := job.DBTag + ":" + req.Database
	snaps, err := r.Snapshots(ctx, tag)
	if err != nil {
		return res, err
	}
	snaps = withTag(withTag(snaps, job.DBTag), tag)
	if len(snaps) == 0 {
		return res, fmt.Errorf("subscription %s has no backup of database %s", sub.Name, req.Database)
	}
	snap, err := pick(snaps, req.Snapshot)
	if err != nil {
		return res, fmt.Errorf("%w among the backups of database %s", err, req.Database)
	}
	res.Snapshot = snap.ID
	name := RestoredDirName(snap)
	res.RestoredTo = filepath.Join(ws.home, name, job.DumpFilename(req.Database))
	if ex, err := ws.exists(name); err != nil {
		return res, err
	} else if ex {
		return res, errTargetExists(filepath.Join(ws.home, name))
	}
	if err := ws.ensureSpace(ctx, r, snap.ID, "/"+job.DumpFilename(req.Database)); err != nil {
		return res, err
	}
	stage, err := ws.stage(sub.Name + "-" + snap.Short())
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(stage)
	// The finished folder is built in root-only staging and moved into
	// home in one step (see webspace.place).
	built := filepath.Join(stage, name)
	if err := os.Mkdir(built, 0o700); err != nil {
		return res, err
	}
	bfd, err := syscall.Open(built, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return res, err
	}
	defer syscall.Close(bfd)
	res.Bytes, err = dumpInto(ctx, r, snap.ID, "/"+job.DumpFilename(req.Database), bfd, job.DumpFilename(req.Database), ws.uid, ws.gid)
	if err == nil {
		err = syscall.Fchown(bfd, ws.uid, ws.gid)
	}
	if err == nil {
		err = syscall.Fchmod(bfd, 0o700)
	}
	if err == nil {
		err = ws.place(stage, name)
	}
	if err != nil {
		res.Bytes = 0
		return res, err
	}
	res.Note = "the dump is not imported; load it into a database with the mysql client or phpMyAdmin"
	return res, nil
}

// dumpInto streams path from the snapshot into a temporary file in dirfd
// and renames it to name once complete, mode 0600, owned by uid:gid. A
// failed dump leaves no file behind.
func dumpInto(ctx context.Context, r restic.Runner, snapshotID, path string, dirfd int, name string, uid, gid int) (int64, error) {
	tmp, err := partName(name)
	if err != nil {
		return 0, err
	}
	fd, err := syscall.Openat(dirfd, tmp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return 0, fmt.Errorf("creating %s: %w", tmp, err)
	}
	f := os.NewFile(uintptr(fd), tmp)
	cw := &countWriter{w: f}
	err = r.Dump(ctx, snapshotID, path, cw)
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Chown(uid, gid)
	}
	if err == nil {
		err = f.Chmod(0o600) // after chown, which may clear bits
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameInPlace(dirfd, tmp, name)
	}
	if err != nil {
		syscall.Unlinkat(dirfd, tmp)
		return 0, err
	}
	return cw.n, nil
}

// partName is a temporary name for name in the same directory, with a
// random suffix, so that a temporary file left by a killed run never
// blocks the next one.
func partName(name string) (string, error) {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "." + name + "." + hex.EncodeToString(b) + ".part", nil
}

// renameInPlace renames tmp to name in dirfd (a root-only directory)
// without replacing an existing name; without renameat2 it falls back to
// renameat after checking that name is absent.
func renameInPlace(dirfd int, tmp, name string) error {
	err := rawRenameat2(dirfd, tmp, dirfd, name, renameNoReplace)
	if errors.Is(err, syscall.ENOSYS) || errors.Is(err, syscall.EINVAL) {
		var st syscall.Stat_t
		if fstatat(dirfd, name, &st) == nil {
			return syscall.EEXIST
		}
		err = syscall.Renameat(dirfd, tmp, dirfd, name)
	}
	if errors.Is(err, syscall.EEXIST) {
		return fmt.Errorf("%s already exists", name)
	}
	return err
}

type countWriter struct {
	w interface{ Write([]byte) (int, error) }
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

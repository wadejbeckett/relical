package restore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
)

type FilesRequest struct {
	Subscription string
	Snapshot     string // "latest", or 8–64 hex characters
	Path         string // relative to home; empty restores the whole webspace
}

type FilesResult struct {
	Subscription string `json:"subscription"`
	Unit         string `json:"unit"`
	Snapshot     string `json:"snapshot"`
	Path         string `json:"path"`
	RestoredTo   string `json:"restored_to"`
	Files        int    `json:"files"`
	Bytes        int64  `json:"bytes"`
	// Dropped lists the device nodes, fifos and sockets in the snapshot
	// that were left out, relative to restored_to.
	Dropped []string `json:"dropped"`
}

// subscription returns the inventory entry for a file or database
// restore; the panel configuration's repository has no webspace.
func subscription(env job.Env, name string) (inventory.Subscription, error) {
	if name == config.ServerName {
		return inventory.Subscription{}, fmt.Errorf("%s holds the panel configuration only; restore it with --unit panel-config", name)
	}
	s, ok := env.Inv.Find(name)
	if !ok {
		return inventory.Subscription{}, fmt.Errorf("subscription %s is not in the inventory", name)
	}
	return s, nil
}

// RunFiles restores home/<path> (or all of home) from a files snapshot
// into home/restored-<date>-<id>/, owned by the subscription's user, mode
// 0700. It never writes anywhere else in home and refuses when that
// folder exists.
func RunFiles(ctx context.Context, env job.Env, req FilesRequest) (FilesResult, error) {
	res := FilesResult{Subscription: req.Subscription, Unit: job.FilesTag, Path: req.Path}
	// Best effort: a failure here never stops this restore.
	_ = CleanStale(env.Cfg, env.Now())
	if !snapshotRE.MatchString(req.Snapshot) {
		return res, fmt.Errorf("snapshot must be latest or 8-64 hex characters, got %q", req.Snapshot)
	}
	if err := validRelPath(req.Path); err != nil {
		return res, err
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
	snaps, err := r.Snapshots(ctx, job.FilesTag)
	if err != nil {
		return res, err
	}
	snap, err := pick(withTag(snaps, job.FilesTag), req.Snapshot)
	if err != nil {
		return res, err
	}
	res.Snapshot = snap.ID
	name := RestoredDirName(snap)
	res.RestoredTo = filepath.Join(ws.home, name)
	if ex, err := ws.exists(name); err != nil {
		return res, err
	} else if ex {
		return res, errTargetExists(res.RestoredTo)
	}

	src := ws.home
	if req.Path != "" {
		src = filepath.Join(ws.home, req.Path)
	}
	// restic restore exits 0 having restored nothing when the include
	// matches nothing, so ask first.
	nodes, _, err := r.Ls(ctx, snap.ID, src, 1)
	if err != nil {
		return res, err
	}
	if len(nodes) == 0 {
		return res, fmt.Errorf("path %q is not in snapshot %s", req.Path, snap.Short())
	}

	// A files snapshot holds only paths in home, so the whole webspace is
	// the whole snapshot.
	sizePath := ""
	if req.Path != "" {
		sizePath = src
	}
	if err := ws.ensureSpace(ctx, r, snap.ID, sizePath); err != nil {
		return res, err
	}
	stage, done, err := ws.stage(sub.Name + "-" + snap.Short())
	if err != nil {
		return res, err
	}
	defer done()
	if err := r.Restore(ctx, snap.ID, stage, []string{job.GlobEscape(src)}); err != nil {
		return res, err
	}
	// The staged copy of home; with a path, it holds only that path.
	tree := filepath.Join(stage, ws.home)
	if fi, err := os.Lstat(tree); errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("path %q is not in snapshot %s", req.Path, snap.Short())
	} else if err != nil {
		return res, err
	} else if !fi.IsDir() {
		return res, fmt.Errorf("snapshot %s holds %s as a %s, not a directory", snap.Short(), ws.home, fi.Mode().Type())
	}
	// The finished folder, built and handed to the user while it is
	// still in root-only staging, then moved into home in one step.
	built := filepath.Join(stage, name)
	if err := os.Rename(tree, built); err != nil {
		return res, err
	}
	if res.Files, res.Bytes, res.Dropped, err = prepareTree(built, ws.uid, ws.gid); err != nil {
		return res, err
	}
	if res.Dropped == nil {
		res.Dropped = []string{}
	}
	if err := os.Chmod(built, 0o700); err != nil {
		return res, err
	}
	if err := ws.place(stage, name); err != nil {
		return res, err
	}
	return res, nil
}

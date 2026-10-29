package restore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

type PanelConfigRequest struct {
	Subscription string // must be config.ServerName
	Snapshot     string // "latest", or 8–64 hex characters
}

type PanelConfigResult struct {
	Subscription string `json:"subscription"`
	Unit         string `json:"unit"`
	Snapshot     string `json:"snapshot"`
	RestoredTo   string `json:"restored_to"`
	Bytes        int64  `json:"bytes"`
	Note         string `json:"note"`
}

var extRE = regexp.MustCompile(`^[a-z0-9]{1,8}$`)

// panelConfigPath is the one file a panel-config snapshot holds.
func panelConfigPath(snap restic.Snapshot) (string, error) {
	if len(snap.Paths) != 1 || !config.CleanAbs(snap.Paths[0]) || !strings.HasPrefix(filepath.Base(snap.Paths[0]), "panel-config-") {
		return "", fmt.Errorf("snapshot %s is not a panel configuration backup (paths %v)", snap.Short(), snap.Paths)
	}
	return snap.Paths[0], nil
}

// RunPanelConfig writes the panel configuration file of a panel-config
// snapshot to <state_dir>/restore/panel-config-<date>-<id>.<ext>, mode
// 0600, owned by root. Relical never applies it: the administrator
// restores it with the panel's own tool.
func RunPanelConfig(ctx context.Context, env job.Env, req PanelConfigRequest) (PanelConfigResult, error) {
	res := PanelConfigResult{Subscription: req.Subscription, Unit: job.PanelConfigTag}
	if req.Subscription != config.ServerName {
		return res, fmt.Errorf("the panel configuration is restored from --subscription %s", config.ServerName)
	}
	if !snapshotRE.MatchString(req.Snapshot) {
		return res, fmt.Errorf("snapshot must be latest or 8-64 hex characters, got %q", req.Snapshot)
	}
	r, _, err := env.Runner(job.ServerSubscription())
	if err != nil {
		return res, err
	}
	snaps, err := r.Snapshots(ctx, job.PanelConfigTag)
	if err != nil {
		return res, err
	}
	snap, err := pick(withTag(snaps, job.PanelConfigTag), req.Snapshot)
	if err != nil {
		return res, err
	}
	res.Snapshot = snap.ID
	src, err := panelConfigPath(snap)
	if err != nil {
		return res, err
	}
	ext := strings.TrimPrefix(filepath.Ext(src), ".")
	if !extRE.MatchString(ext) {
		ext = "tar"
	}
	dir := filepath.Join(env.Cfg.StateDir, "restore")
	name := fmt.Sprintf("panel-config-%s-%s.%s", snap.Time.UTC().Format("2006-01-02"), snap.Short(), ext)
	res.RestoredTo = filepath.Join(dir, name)
	if _, err := os.Lstat(res.RestoredTo); err == nil {
		return res, errTargetExists(res.RestoredTo)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	dirfd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return res, fmt.Errorf("restore directory %s: %w", dir, err)
	}
	defer syscall.Close(dirfd)
	res.Bytes, err = dumpInto(ctx, r, snap.ID, src, dirfd, name, os.Geteuid(), os.Getegid())
	if err != nil {
		return res, err
	}
	res.Note = "Relical does not apply the panel configuration. Restore it with Plesk's own tool, plesk bin pleskrestore, " +
		"and delete this file afterwards: it holds the panel's settings and, encrypted, its passwords."
	if env.Inv.Server.Encrypt() {
		res.Note += " Its password (PLESK_BACKUP_PASSWORD) is the content of the " + config.ServerName + " repository key in " + env.Cfg.KeysDir + "."
	}
	return res, nil
}

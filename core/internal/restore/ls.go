package restore

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

// MaxLsEntries caps one ls listing.
const MaxLsEntries = 5000

type LsRequest struct {
	Subscription string
	Snapshot     string // 8–64 hex characters
	Path         string // relative to the snapshot's base (see Ls)
}

type LsEntry struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	Size  uint64    `json:"size"`
	MTime time.Time `json:"mtime"`
}

type LsResult struct {
	Subscription string    `json:"subscription"`
	Snapshot     string    `json:"snapshot"`
	Path         string    `json:"path"` // the absolute path listed
	Entries      []LsEntry `json:"entries"`
	Truncated    bool      `json:"truncated"`
}

var snapshotIDRE = regexp.MustCompile(`^[0-9a-f]{8,64}$`)

// Ls lists one directory level of a snapshot. Path is relative to the
// subscription's home for a files snapshot and to / for a db or mail
// snapshot; for _server it lists the panel configuration file and takes
// no path. A file path lists that file.
func Ls(ctx context.Context, env job.Env, req LsRequest) (LsResult, error) {
	res := LsResult{Subscription: req.Subscription, Entries: []LsEntry{}}
	if !snapshotIDRE.MatchString(req.Snapshot) {
		return res, fmt.Errorf("snapshot must be 8-64 hex characters, got %q", req.Snapshot)
	}
	if err := validRelPath(req.Path); err != nil {
		return res, err
	}
	var sub inventory.Subscription
	if req.Subscription == config.ServerName {
		sub = job.ServerSubscription()
	} else {
		s, ok := env.Inv.Find(req.Subscription)
		if !ok {
			return res, fmt.Errorf("subscription %s is not in the inventory", req.Subscription)
		}
		sub = s
	}
	r, _, err := env.Runner(sub)
	if err != nil {
		return res, err
	}
	snaps, err := r.Snapshots(ctx, "")
	if err != nil {
		return res, err
	}
	snap, err := pick(snaps, req.Snapshot)
	if err != nil {
		return res, err
	}
	res.Snapshot = snap.ID
	base := "/"
	switch {
	case sub.Name == config.ServerName:
		if req.Path != "" {
			return res, fmt.Errorf("%s snapshots hold one file; ls takes no --path for them", config.ServerName)
		}
		if base, err = panelConfigPath(snap); err != nil {
			return res, err
		}
	case len(withTag([]restic.Snapshot{snap}, job.FilesTag)) == 1:
		base = sub.Home
	}
	res.Path = filepath.Join(base, req.Path)
	// One more than the cap: the listed directory itself comes first.
	nodes, truncated, err := r.Ls(ctx, snap.ID, res.Path, MaxLsEntries+1)
	if err != nil {
		return res, err
	}
	if len(nodes) == 0 {
		return res, fmt.Errorf("path %s is not in snapshot %s", res.Path, snap.Short())
	}
	for _, n := range nodes {
		if n.Path == res.Path && n.Type == "dir" {
			continue
		}
		if len(res.Entries) == MaxLsEntries {
			truncated = true
			break
		}
		res.Entries = append(res.Entries, LsEntry{Name: n.Name, Type: n.Type, Size: n.Size, MTime: n.MTime})
	}
	res.Truncated = truncated
	return res, nil
}

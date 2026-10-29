package job

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
)

// Report is the nightly summary that relical report prints and the panel
// adapters render. Its keys are a contract with the adapters (the Plesk
// extension's Relical\Report); TestReportShape pins them.
type Report struct {
	Generated     string          `json:"generated"`
	Subscriptions int             `json:"subscriptions"`
	OK            int             `json:"ok"`
	Failed        int             `json:"failed"`
	Skipped       int             `json:"skipped"`
	StoredBytes   uint64          `json:"stored_bytes"`
	Rows          []ReportRow     `json:"rows"`
	Renamed       []ReportRenamed `json:"renamed"`
	Warnings      []string        `json:"warnings"`
}

// ReportRow is the latest backup of one subscription's unit (the state
// file's unit key: mail, files, db:<name>, panel-config). StoredBytes is
// the size of the subscription's repository from the last stats, repeated
// on each of its rows; Report.StoredBytes counts each repository once.
type ReportRow struct {
	Subscription string `json:"subscription"`
	Unit         string `json:"unit"`
	OK           bool   `json:"ok"`
	Time         string `json:"time"`
	Error        string `json:"error"`
	StoredBytes  uint64 `json:"stored_bytes"`
}

// ReportRenamed is a rename from the inventory: backups go to a new
// repository under Name, and the one under RenamedFrom is left in place.
type ReportRenamed struct {
	Name        string `json:"name"`
	RenamedFrom string `json:"renamed_from"`
	Note        string `json:"note"`
}

// Report thresholds: a backup older than BackupStale or a check older
// than CheckStale is a warning.
const (
	BackupStale = 48 * time.Hour
	CheckStale  = 10 * 24 * time.Hour
)

// BuildReport summarises the state. inv is the inventory, or nil with
// invErr when it could not be loaded: that is a warning, so the report
// always renders. It never runs restic.
func BuildReport(st State, inv *inventory.Inventory, invErr error, now time.Time) Report {
	rep := Report{Generated: now.UTC().Format(time.RFC3339), Rows: []ReportRow{}, Renamed: []ReportRenamed{}, Warnings: []string{}}
	subs := make([]string, 0, len(st))
	for s := range st {
		subs = append(subs, s)
	}
	sort.Strings(subs)
	ts := func(t time.Time) string { return t.UTC().Format(time.RFC3339) }
	for _, sub := range subs {
		units := st[sub]
		var size uint64
		if stats, ok := units[RepositoryKey]["stats"]; ok {
			size = stats.StoredBytes
		}
		rep.StoredBytes += size
		keys := make([]string, 0, len(units))
		for k := range units {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var oldest time.Time
		hasRow := false
		for _, k := range keys {
			r, ok := units[k]["backup"]
			if !ok {
				continue
			}
			hasRow = true
			rep.Rows = append(rep.Rows, ReportRow{Subscription: sub, Unit: k, OK: r.OK, Time: ts(r.Time), Error: r.Error, StoredBytes: size})
			switch {
			case !r.OK:
				rep.Failed++
			case strings.HasPrefix(r.Note, "skipped"):
				rep.Skipped++
			default:
				rep.OK++
			}
			if now.Sub(r.Time) > BackupStale {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s %s: the last backup (%s) is older than 48 hours", sub, k, ts(r.Time)))
			}
			if oldest.IsZero() || r.Time.Before(oldest) {
				oldest = r.Time // for the never-checked warning below
			}
		}
		if hasRow && sub != config.ServerName {
			rep.Subscriptions++
		}
		if c, ok := units[RepositoryKey]["check"]; ok {
			if now.Sub(c.Time) > CheckStale {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: the last check (%s) is older than 10 days", sub, ts(c.Time)))
			}
		} else if hasRow && now.Sub(oldest) > CheckStale {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: backed up since %s but never checked", sub, ts(oldest)))
		}
		if inv != nil && sub != config.ServerName {
			if _, ok := inv.Find(sub); !ok {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: in the state file but no longer in the inventory; its repository is kept", sub))
			}
		}
	}
	if invErr != nil {
		rep.Warnings = append(rep.Warnings, "the inventory could not be loaded, so renames and removed subscriptions are not reported: "+invErr.Error())
	}
	if inv != nil {
		for _, r := range inv.Renamed {
			rep.Renamed = append(rep.Renamed, ReportRenamed{Name: r.Subscription, RenamedFrom: r.RenamedFrom, Note: r.Note})
		}
	}
	return rep
}

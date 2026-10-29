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
	Generated     string `json:"generated"`
	Subscriptions int    `json:"subscriptions"`
	OK            int    `json:"ok"`
	Failed        int    `json:"failed"`
	// SkippedRows counts the rows that are OK with a note starting with
	// "skipped" (a database of an unsupported type).
	SkippedRows int             `json:"skipped_rows"`
	StoredBytes uint64          `json:"stored_bytes"`
	Rows        []ReportRow     `json:"rows"`
	Renamed     []ReportRenamed `json:"renamed"`
	// Skipped are the subscriptions the inventory left out: by the
	// adapter, or by validation (those also have a failed row).
	Skipped  []ReportSkipped `json:"skipped"`
	Warnings []string        `json:"warnings"`
}

// ReportSkipped is a subscription the inventory left out, with the reason.
type ReportSkipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
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

// InventoryUnit is the unit of the report row of a subscription whose
// inventory entry is invalid.
const InventoryUnit = "inventory"

// Report thresholds: a backup older than BackupStale or a check older
// than CheckStale is a warning.
const (
	BackupStale = 48 * time.Hour
	CheckStale  = 10 * 24 * time.Hour
)

// expectedUnits gives, per subscription in the inventory (and _server
// when the server backs up its panel configuration), the state keys of
// the units a backup gives rows for.
func expectedUnits(inv *inventory.Inventory) map[string]map[string]bool {
	exp := map[string]map[string]bool{}
	for _, s := range inv.Subscriptions {
		units := map[string]bool{RepositoryKey: true}
		for _, u := range []string{MailTag, FilesTag} {
			if s.Has(u) {
				units[u] = true
			}
		}
		if s.Has(DBTag) {
			if len(s.Databases) == 0 {
				units[DBTag] = true
			}
			for _, d := range s.Databases {
				units[DBTag+":"+d.Name] = true
			}
		}
		exp[s.Name] = units
	}
	if inv.Server.Has(PanelConfigTag) {
		exp[config.ServerName] = map[string]bool{RepositoryKey: true, PanelConfigTag: true}
	}
	return exp
}

// BuildReport summarises the state. inv is the inventory, or nil with
// invErr when it could not be loaded: that is a warning, so the report
// always renders. It never runs restic.
//
// With an inventory, the rows are the units it covers. A unit in the
// inventory without a row is warned about as never backed up. A unit in
// the state file that the inventory no longer covers (a dropped database,
// a removed subscription) has no row and no stale-backup warning; it is
// reported once as "no longer backed up", in the reports made within
// BackupStale of its last backup, and then left alone.
func BuildReport(st State, inv *inventory.Inventory, invErr error, now time.Time) Report {
	rep := Report{Generated: now.UTC().Format(time.RFC3339), Rows: []ReportRow{}, Renamed: []ReportRenamed{}, Skipped: []ReportSkipped{}, Warnings: []string{}}
	var exp map[string]map[string]bool
	if inv != nil {
		exp = expectedUnits(inv)
	}
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
		_, covered := exp[sub]
		var oldest, lastRetired time.Time
		var retired []string // within BackupStale of their last backup
		hasRow := false
		for _, k := range keys {
			r, ok := units[k]["backup"]
			if !ok {
				continue
			}
			if exp != nil && !exp[sub][k] {
				if now.Sub(r.Time) <= BackupStale {
					retired = append(retired, k)
				}
				if r.Time.After(lastRetired) {
					lastRetired = r.Time
				}
				continue
			}
			hasRow = true
			rep.Rows = append(rep.Rows, ReportRow{Subscription: sub, Unit: k, OK: r.OK, Time: ts(r.Time), Error: r.Error, StoredBytes: size})
			switch {
			case !r.OK:
				rep.Failed++
			case strings.HasPrefix(r.Note, "skipped"):
				rep.SkippedRows++
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
		switch {
		case covered:
			for _, k := range retired {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s %s: no longer backed up (no longer in the inventory); its backups are kept", sub, k))
			}
		case exp != nil && !lastRetired.IsZero() && now.Sub(lastRetired) <= BackupStale:
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: no longer in the inventory, so no longer backed up; its repository is kept", sub))
		}
		if hasRow && sub != config.ServerName {
			rep.Subscriptions++
		}
		if exp != nil && !covered {
			continue // no check warnings for what is no longer backed up
		}
		if c, ok := units[RepositoryKey]["check"]; ok {
			if now.Sub(c.Time) > CheckStale {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: the last check (%s) is older than 10 days", sub, ts(c.Time)))
			}
		} else if hasRow && now.Sub(oldest) > CheckStale {
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s: backed up since %s but never checked", sub, ts(oldest)))
		}
	}
	if exp != nil {
		names := make([]string, 0, len(exp))
		for n := range exp {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			ks := make([]string, 0, len(exp[n]))
			for k := range exp[n] {
				if _, ok := st[n][k]["backup"]; !ok && k != RepositoryKey {
					ks = append(ks, k)
				}
			}
			sort.Strings(ks)
			for _, k := range ks {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s %s: in the inventory but never backed up", n, k))
			}
		}
	}
	if invErr != nil {
		rep.Warnings = append(rep.Warnings, "the inventory could not be loaded, so renames and removed subscriptions are not reported: "+invErr.Error())
	}
	if inv != nil {
		// A subscription whose inventory entry is invalid is not backed
		// up at all: a failed row, until the entry is fixed.
		for _, s := range inv.Skipped {
			rep.Skipped = append(rep.Skipped, ReportSkipped{Name: s.Name, Reason: s.Reason})
		}
		for _, s := range inv.Invalid() {
			rep.Rows = append(rep.Rows, ReportRow{Subscription: s.Name, Unit: InventoryUnit, OK: false, Time: rep.Generated, Error: s.Reason})
			rep.Failed++
		}
		for _, r := range inv.Renamed {
			rep.Renamed = append(rep.Renamed, ReportRenamed{Name: r.Subscription, RenamedFrom: r.RenamedFrom, Note: r.Note})
		}
	}
	return rep
}

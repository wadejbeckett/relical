// Package job runs backup, forget and check for one subscription at a time
// and records the outcome in state.json. A subscription's units (mail,
// files, db) each have their own file; this one holds what they share:
// the environment, results, ordering, the run lock and the state file.
package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/keys"
	"github.com/wadejbeckett/relical/core/internal/locator"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

// Units, which are also the snapshot tags. A database snapshot is tagged
// both DBTag and DBTag:<name>.
const (
	MailTag        = "mail"
	FilesTag       = "files"
	DBTag          = "db"
	PanelConfigTag = "panel-config"
)

// RepositoryKey is the state file's unit key for results that concern a
// whole repository rather than one unit: forget, check and stats.
const RepositoryKey = "repository"

// GroupBy groups snapshots for retention: each tag set (mail, files, and
// db,db:<name> per database) keeps its own daily, weekly and monthly
// snapshots.
const GroupBy = "tags"

type Env struct {
	Cfg      *config.Config
	Inv      inventory.Inventory
	Locator  locator.Pattern
	Now      func() time.Time
	ExtraEnv map[string]string // tests only
}

type Result struct {
	Subscription string    `json:"subscription"`
	Unit         string    `json:"unit,omitempty"`
	Database     string    `json:"database,omitempty"`
	Action       string    `json:"action"`
	Time         time.Time `json:"time"`
	OK           bool      `json:"ok"`
	Snapshot     string    `json:"snapshot,omitempty"`
	Mailboxes    int       `json:"mailboxes,omitempty"`
	FilesNew     uint64    `json:"files_new,omitempty"`
	DataAdded    uint64    `json:"data_added,omitempty"`
	StoredBytes  uint64    `json:"stored_bytes,omitempty"`
	Seconds      float64   `json:"seconds,omitempty"`
	Note         string    `json:"note,omitempty"`
	Error        string    `json:"error,omitempty"`
}

// StateKey is the result's unit key in state.json: the unit, db:<name>
// for a database, or RepositoryKey for forget, check and stats.
func (r Result) StateKey() string {
	switch {
	case r.Unit == "":
		return RepositoryKey
	case r.Unit == DBTag && r.Database != "":
		return DBTag + ":" + r.Database
	}
	return r.Unit
}

// Prepare creates the state directories. state_dir and restore/ are 0711
// so that doveadm, running as the mail user, can reach its own staging
// directory; cache/ is 0700.
func (e Env) Prepare() error {
	for _, d := range []struct {
		p string
		m os.FileMode
	}{
		{e.Cfg.StateDir, 0o711},
		{filepath.Join(e.Cfg.StateDir, "cache"), 0o700},
		{filepath.Join(e.Cfg.StateDir, "restore"), 0o711},
	} {
		if err := os.MkdirAll(d.p, d.m); err != nil {
			return err
		}
		if err := os.Chmod(d.p, d.m); err != nil {
			return err
		}
	}
	return nil
}

// Subscription returns the inventory entry for name. config.ServerName
// stands for the panel configuration's repository, on the default
// destination.
func (e Env) Subscription(name string) (inventory.Subscription, error) {
	if name == config.ServerName {
		return inventory.Subscription{Name: name}, nil
	}
	if s, ok := e.Inv.Find(name); ok {
		return s, nil
	}
	return inventory.Subscription{}, fmt.Errorf("subscription %s is not in the inventory", name)
}

// DestinationFor returns the subscription's destination (its own when set,
// else the default) with the repository named after the subscription.
func (e Env) DestinationFor(sub inventory.Subscription) (string, config.Destination, error) {
	return e.Cfg.DestinationFor(sub.Destination, sub.Name)
}

// RetentionFor returns the subscription's retention, else the default.
func (e Env) RetentionFor(sub inventory.Subscription) config.Retention {
	if sub.Retention != nil {
		return *sub.Retention
	}
	return e.Cfg.Retention
}

func (e Env) runnerWithKey(sub inventory.Subscription, keyPath string) (restic.Runner, config.Destination, error) {
	_, dst, err := e.DestinationFor(sub)
	if err != nil {
		return restic.Runner{}, dst, err
	}
	env := map[string]string{}
	for k, v := range dst.Env {
		env[k] = v
	}
	for k, v := range e.ExtraEnv {
		env[k] = v
	}
	return restic.Runner{
		Bin: e.Cfg.ResticBin, Repository: dst.Repository, PasswordFile: keyPath, Env: env,
		Host: e.Cfg.ServerID, CacheDir: filepath.Join(e.Cfg.StateDir, "cache"),
		LimitUploadKiB: e.Cfg.LimitUploadKiB, LowPriority: e.Cfg.LowPriority, SftpArgs: dst.SftpArgs,
	}, dst, nil
}

// Runner returns a runner for a subscription that already has a key, and
// refuses a repository recorded as another subscription id's.
func (e Env) Runner(sub inventory.Subscription) (restic.Runner, config.Destination, error) {
	kp, err := keys.Path(e.Cfg.KeysDir, sub.Name)
	if err != nil {
		return restic.Runner{}, config.Destination{}, err
	}
	if err := keys.CheckOwner(e.Cfg.KeysDir, sub.Name, sub.ID); err != nil {
		return restic.Runner{}, config.Destination{}, err
	}
	return e.runnerWithKey(sub, kp)
}

// openRepo returns a runner for the subscription's repository, creating
// the key and the repository on first use and recording the subscription's
// id as the repository's owner. A repository recorded as another id's is
// refused. A wrong password is an error, never a reason to init.
func (e Env) openRepo(ctx context.Context, sub inventory.Subscription) (restic.Runner, error) {
	if err := keys.CheckOwner(e.Cfg.KeysDir, sub.Name, sub.ID); err != nil {
		return restic.Runner{}, err
	}
	kp, _, err := keys.Ensure(e.Cfg.KeysDir, sub.Name)
	if err != nil {
		return restic.Runner{}, err
	}
	r, _, err := e.runnerWithKey(sub, kp)
	if err != nil {
		return restic.Runner{}, err
	}
	exists, err := r.Exists(ctx)
	if err != nil {
		return restic.Runner{}, err
	}
	if !exists {
		if err := r.Init(ctx); err != nil {
			return restic.Runner{}, err
		}
	}
	if err := keys.RecordOwner(e.Cfg.KeysDir, sub.Name, sub.ID); err != nil {
		return restic.Runner{}, err
	}
	return r, nil
}

func (e Env) result(sub, unit, action string) Result {
	return Result{Subscription: sub, Unit: unit, Action: action, Time: e.Now().UTC()}
}

func failed(r Result, err error) Result {
	r.Error = err.Error()
	return r
}

// saved fills res from a path backup's outcome. A partial backup (restic
// exit 3: some files could not be read, but the snapshot was saved) is
// OK with a note.
func saved(res Result, sum restic.BackupSummary, err error) Result {
	if err != nil && (restic.ExitCode(err) != restic.ExitPartial || sum.SnapshotID == "") {
		return failed(res, err)
	}
	if err != nil {
		res.Note = "restic could not read some files (they may have changed, vanished or be unreadable); the snapshot was saved: " + err.Error()
	}
	res.OK = true
	res.Snapshot = sum.SnapshotID
	res.FilesNew = sum.FilesNew
	res.DataAdded = sum.DataAddedPacked
	res.Seconds = sum.TotalDuration
	return res
}

func globEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)
	return r.Replace(s)
}

// unitOrder is the order in which a subscription's units run.
var unitOrder = []string{MailTag, FilesTag, DBTag}

// hasUnits reports whether the subscription has any unit to back up.
func hasUnits(sub inventory.Subscription) bool {
	for _, u := range unitOrder {
		if sub.Has(u) {
			return true
		}
	}
	return false
}

const noUnits = "the subscription has none of the features mail, files or db; nothing to do"

// BackupSubscription backs up the subscription's units in order (mail,
// files, db), each only when it is among the subscription's features.
// unit, when not empty, restricts the run to that unit.
func (e Env) BackupSubscription(ctx context.Context, sub inventory.Subscription, unit string) []Result {
	if unit != "" && !sub.Has(unit) {
		res := e.result(sub.Name, unit, "backup")
		res.OK = true
		res.Note = unit + " is not among the subscription's features; nothing to back up"
		return []Result{res}
	}
	if unit == "" && !hasUnits(sub) {
		res := e.result(sub.Name, "", "backup")
		res.OK = true
		res.Note = noUnits
		return []Result{res}
	}
	var out []Result
	for _, u := range unitOrder {
		if (unit != "" && u != unit) || !sub.Has(u) {
			continue
		}
		if ctx.Err() != nil {
			break
		}
		switch u {
		case MailTag:
			out = append(out, e.BackupMail(ctx, sub))
		case FilesTag:
			out = append(out, e.BackupFiles(ctx, sub))
		case DBTag:
			out = append(out, e.BackupDatabases(ctx, sub)...)
		}
	}
	return out
}

// BackupAll backs up every subscription in inventory order, then the
// server (the panel configuration, last), then records the stored size of
// every repository that received a snapshot. unit, when not empty,
// restricts the run to that unit.
func (e Env) BackupAll(ctx context.Context, unit string) []Result {
	results := []Result{}
	var touched []inventory.Subscription
	for _, s := range e.Inv.Subscriptions {
		if ctx.Err() != nil {
			return results
		}
		rs := e.BackupSubscription(ctx, s, unit)
		results = append(results, rs...)
		for _, r := range rs {
			if r.Snapshot != "" {
				touched = append(touched, s)
				break
			}
		}
	}
	if ctx.Err() != nil {
		return results
	}
	results = append(results, e.runServer(ctx, unit)...)
	for _, s := range touched {
		if ctx.Err() != nil {
			break
		}
		results = append(results, e.Stats(ctx, s))
	}
	return results
}

// runServer is the hook for the server's own units, run after every
// subscription in BackupAll.
//
// TASK 3 HOOK: back up panel-config into the _server repository here
// (when unit is "" or panel-config and the inventory's server lists the
// feature), and add _server to the repositories whose size is recorded.
func (e Env) runServer(ctx context.Context, unit string) []Result {
	return nil
}

// Stats records the bytes the subscription's repository stores. A failed
// stats call is a note, never a failure: the result is OK and keeps the
// size from the last successful call, if any.
func (e Env) Stats(ctx context.Context, sub inventory.Subscription) Result {
	res := e.result(sub.Name, "", "stats")
	res.OK = true
	r, _, err := e.Runner(sub)
	var n uint64
	if err == nil {
		n, err = r.Stats(ctx)
	}
	if err == nil {
		res.StoredBytes = n
		return res
	}
	res.Note = "repository size not updated: " + err.Error()
	if st, rerr := ReadState(e.Cfg.StateDir); rerr == nil {
		if prev, ok := st[sub.Name][RepositoryKey]["stats"]; ok && prev.StoredBytes > 0 {
			res.StoredBytes = prev.StoredBytes
			res.Note += "; stored_bytes is from " + prev.Time.Format(time.RFC3339)
		}
	}
	return res
}

// Forget applies the subscription's retention to every tag group of its
// repository (one call per repository) and prunes when prune is local.
func (e Env) Forget(ctx context.Context, sub inventory.Subscription) Result {
	res := e.result(sub.Name, "", "forget")
	if !hasUnits(sub) {
		res.OK = true
		res.Note = noUnits
		return res
	}
	r, dst, err := e.Runner(sub)
	if err != nil {
		return failed(res, err)
	}
	if dst.Prune == "remote" {
		res.OK = true
		res.Note = "pruning runs on the destination host (prune: remote)"
		return res
	}
	// A restic killed mid-run (SIGKILL, OOM, power loss) leaves a stale lock
	// that would make forget --prune and check fail until it is removed.
	if err := r.Unlock(ctx); err != nil {
		return failed(res, err)
	}
	if err := r.Forget(ctx, GroupBy, e.RetentionFor(sub), true); err != nil {
		return failed(res, err)
	}
	res.OK = true
	return res
}

func (e Env) Check(ctx context.Context, sub inventory.Subscription, subset string) Result {
	res := e.result(sub.Name, "", "check")
	if !hasUnits(sub) {
		res.OK = true
		res.Note = noUnits
		return res
	}
	r, _, err := e.Runner(sub)
	if err != nil {
		return failed(res, err)
	}
	if err := r.Unlock(ctx); err != nil {
		return failed(res, err)
	}
	if err := r.Check(ctx, subset); err != nil {
		return failed(res, err)
	}
	res.OK = true
	return res
}

// Lock takes an exclusive, non-blocking lock so that runs never overlap.
func Lock(stateDir string) (func(), error) {
	f, err := os.OpenFile(filepath.Join(stateDir, "relical.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another relical run holds the lock")
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// State is state.json: subscription -> unit key (see Result.StateKey) ->
// action -> the last result.
type State map[string]map[string]map[string]Result

// ReadState reads state.json. Entries in an older shape (subscription ->
// action -> result, from before units) are dropped.
func ReadState(stateDir string) (State, error) {
	st := State{}
	b, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	var raw map[string]map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("state.json: %w", err)
	}
	for sub, units := range raw {
		for unit, v := range units {
			var actions map[string]Result
			if json.Unmarshal(v, &actions) != nil {
				continue
			}
			if st[sub] == nil {
				st[sub] = map[string]map[string]Result{}
			}
			st[sub][unit] = actions
		}
	}
	return st, nil
}

// Record merges results into state.json, written atomically.
func (e Env) Record(results []Result) error {
	st, err := ReadState(e.Cfg.StateDir)
	if err != nil {
		return err
	}
	for _, r := range results {
		k := r.StateKey()
		if st[r.Subscription] == nil {
			st[r.Subscription] = map[string]map[string]Result{}
		}
		if st[r.Subscription][k] == nil {
			st[r.Subscription][k] = map[string]Result{}
		}
		st[r.Subscription][k][r.Action] = r
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(e.Cfg.StateDir, "state-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(e.Cfg.StateDir, "state.json"))
}

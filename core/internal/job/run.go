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
	// Breaker stops an --all run from trying a destination that has
	// failed to connect BreakerTrips times in a row; nil runs without one.
	Breaker *Breaker
	// Progress, when set, gets the results of each subscription (and of
	// the server, each stats call and each forget or check) as soon as
	// they are known, so that the caller can record them during the run.
	Progress func([]Result)
}

func (e Env) progress(rs []Result) {
	if e.Progress != nil && len(rs) > 0 {
		e.Progress(rs)
	}
}

// BreakerTrips is how many subscriptions in a row may fail to reach a
// destination before the rest of an --all run stops trying it.
const BreakerTrips = 3

// Breaker counts, per destination, the subscriptions in a row whose
// repository could not be reached (restic.IsConnectionFailure). Once a
// destination has BreakerTrips of them, the remaining subscriptions on it
// fail at once with "destination unreachable; not tried", so that an
// unreachable Storage Box costs a few connection timeouts instead of
// several per subscription. Any subscription that reaches the destination
// resets its count.
type Breaker struct {
	fails map[string]int
	last  map[string]string
}

func NewBreaker() *Breaker {
	return &Breaker{fails: map[string]int{}, last: map[string]string{}}
}

// open returns the error for a destination that is no longer tried.
func (b *Breaker) open(dest string) (string, bool) {
	if b == nil || b.fails[dest] < BreakerTrips {
		return "", false
	}
	return fmt.Sprintf("destination %s unreachable; not tried (%d subscriptions in a row could not connect; the last: %s)",
		dest, b.fails[dest], b.last[dest]), true
}

// observe counts a subscription's results against its destination.
func (b *Breaker) observe(dest string, rs []Result) {
	if b == nil {
		return
	}
	reached, unreachable := false, ""
	for _, r := range rs {
		switch {
		case r.Snapshot != "" || (r.OK && r.Note == "" && (r.Action == "forget" || r.Action == "check")):
			reached = true
		case !r.OK && restic.IsConnectionFailure(r.Error):
			unreachable = r.Error
		}
	}
	switch {
	case reached:
		b.fails[dest] = 0
	case unreachable != "":
		b.fails[dest]++
		b.last[dest] = unreachable
	}
}

// destinationName is the name of the destination sub's repository is on.
func (e Env) destinationName(sub inventory.Subscription) string {
	if sub.Destination != "" {
		return sub.Destination
	}
	return e.Cfg.DefaultDestination
}

// guarded runs fn for sub unless the breaker has given up on its
// destination, in which case notTried gives the results instead.
func (e Env) guarded(sub inventory.Subscription, fn func() []Result, notTried func(msg string) []Result) []Result {
	dest := e.destinationName(sub)
	if msg, open := e.Breaker.open(dest); open {
		return notTried(msg)
	}
	rs := fn()
	e.Breaker.observe(dest, rs)
	return rs
}

// plannedUnits lists the results a backup of sub (restricted to unit when
// not empty) would give, as unit and database, for "not tried" results.
func plannedUnits(sub inventory.Subscription, unit string) [][2]string {
	if sub.Name == config.ServerName {
		return [][2]string{{PanelConfigTag, ""}}
	}
	var out [][2]string
	for _, u := range unitOrder {
		if (unit != "" && u != unit) || !sub.Has(u) {
			continue
		}
		if u == DBTag && len(sub.Databases) > 0 {
			for _, d := range sub.Databases {
				out = append(out, [2]string{DBTag, d.Name})
			}
			continue
		}
		out = append(out, [2]string{u, ""})
	}
	return out
}

// InvalidResults gives one failed result per subscription that the
// inventory left out because its entry is invalid (inventory.Skip with
// Invalid), so that an --all run of action reports them and exits 1.
// They are not recorded in the state file: report shows them from the
// inventory, and they disappear once the entry is fixed.
func (e Env) InvalidResults(action string) []Result {
	var out []Result
	for _, s := range e.Inv.Invalid() {
		out = append(out, failed(e.result(s.Name, "", action), fmt.Errorf("%s; left out of this run", s.Reason)))
	}
	return out
}

// ForgetAll runs Forget for each of subs in order, with the breaker.
func (e Env) ForgetAll(ctx context.Context, subs []inventory.Subscription) []Result {
	return e.eachRepository(ctx, subs, "forget", func(s inventory.Subscription) Result { return e.Forget(ctx, s) })
}

// CheckAll runs Check for each of subs in order, with the breaker.
func (e Env) CheckAll(ctx context.Context, subs []inventory.Subscription, subset string) []Result {
	return e.eachRepository(ctx, subs, "check", func(s inventory.Subscription) Result { return e.Check(ctx, s, subset) })
}

func (e Env) eachRepository(ctx context.Context, subs []inventory.Subscription, action string, fn func(inventory.Subscription) Result) []Result {
	results := []Result{}
	for _, s := range subs {
		if ctx.Err() != nil {
			break
		}
		rs := e.guarded(s, func() []Result { return []Result{fn(s)} }, func(msg string) []Result {
			return []Result{failed(e.result(s.Name, "", action), errors.New(msg))}
		})
		e.progress(rs)
		results = append(results, rs...)
	}
	return results
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
// directory; cache/ and staging/ (the panel configuration) are 0700. As
// root, it also creates the restore staging directory in each files root
// (see RestoreStagingName).
func (e Env) Prepare() error {
	for _, d := range []struct {
		p string
		m os.FileMode
	}{
		{e.Cfg.StateDir, 0o711},
		{filepath.Join(e.Cfg.StateDir, "cache"), 0o700},
		{filepath.Join(e.Cfg.StateDir, "restore"), 0o711},
		{e.StagingDir(), 0o700},
	} {
		if err := os.MkdirAll(d.p, d.m); err != nil {
			return err
		}
		if err := os.Chmod(d.p, d.m); err != nil {
			return err
		}
	}
	return e.prepareRestoreStaging()
}

// Subscription returns the inventory entry for name. config.ServerName
// stands for the panel configuration's repository, on the default
// destination.
func (e Env) Subscription(name string) (inventory.Subscription, error) {
	if name == config.ServerName {
		return ServerSubscription(), nil
	}
	if s, ok := e.Inv.Find(name); ok {
		return s, nil
	}
	for _, s := range e.Inv.Skipped {
		if s.Name == name && s.Reason != "" {
			return inventory.Subscription{}, fmt.Errorf("subscription %s is not in the inventory: it was left out (%s)", name, s.Reason)
		}
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
// refuses a repository recorded as another subscription id's (see
// checkOwner).
func (e Env) Runner(sub inventory.Subscription) (restic.Runner, config.Destination, error) {
	kp, err := keys.Path(e.Cfg.KeysDir, sub.Name)
	if err != nil {
		return restic.Runner{}, config.Destination{}, err
	}
	if err := e.checkOwner(sub); err != nil {
		return restic.Runner{}, config.Destination{}, err
	}
	return e.runnerWithKey(sub, kp)
}

// checkOwner refuses a repository recorded as another id's. When the
// inventory comes from inventory_command, it also refuses an existing
// key without an owner file (from a static inventory without ids, or an
// earlier build): the repository may be an earlier subscription's of the
// same name, so the administrator adopts it once with relical adopt.
// A static inventory keeps the earlier behaviour: the first id to use
// such a repository is recorded as its owner.
func (e Env) checkOwner(sub inventory.Subscription) error {
	dir := e.Cfg.KeysDir
	if err := keys.CheckOwner(dir, sub.Name, sub.ID); err != nil {
		return err
	}
	if len(e.Cfg.InventoryCommand) == 0 || sub.ID == "" {
		return nil
	}
	has, err := keys.HasOwner(dir, sub.Name)
	if err != nil || has {
		return err
	}
	kp, err := keys.Path(dir, sub.Name)
	if errors.Is(err, keys.ErrNoKey) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("the repository for %s has a key (%s) but no owner file, so it may belong to an earlier subscription of the same name: "+
		"if it is this subscription's (id %q), run relical adopt --subscription %s once to record the id", sub.Name, kp, sub.ID, sub.Name)
}

// openRepo returns a runner for the subscription's repository, creating
// the key and the repository on first use and recording the subscription's
// id as the repository's owner. A repository recorded as another id's is
// refused (see checkOwner). A wrong password is an error, never a reason
// to init.
func (e Env) openRepo(ctx context.Context, sub inventory.Subscription) (restic.Runner, error) {
	if err := e.checkOwner(sub); err != nil {
		return restic.Runner{}, err
	}
	kp, _, err := keys.Ensure(e.Cfg.KeysDir, sub.Name)
	if err != nil {
		return restic.Runner{}, err
	}
	// Recorded before init, so that a failed init does not leave a key
	// without an owner, which checkOwner would then refuse.
	if err := keys.RecordOwner(e.Cfg.KeysDir, sub.Name, sub.ID); err != nil {
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
	return r, nil
}

// Adopt records the subscription's id as the owner of its existing
// repository, after confirming that the repository opens with the key.
// It is for a key without an owner file, which checkOwner refuses when
// the inventory comes from inventory_command. It never creates a key or
// a repository, and never replaces a recorded owner.
func (e Env) Adopt(ctx context.Context, sub inventory.Subscription) Result {
	res := e.result(sub.Name, "", "adopt")
	if sub.ID == "" {
		return failed(res, fmt.Errorf("the inventory gives no id for %s; there is nothing to record", sub.Name))
	}
	kp, err := keys.Path(e.Cfg.KeysDir, sub.Name)
	if err != nil {
		return failed(res, err)
	}
	if err := keys.CheckOwner(e.Cfg.KeysDir, sub.Name, sub.ID); err != nil {
		return failed(res, err)
	}
	if has, err := keys.HasOwner(e.Cfg.KeysDir, sub.Name); err != nil {
		return failed(res, err)
	} else if has {
		res.OK = true
		res.Note = fmt.Sprintf("the repository is already recorded as id %q's", sub.ID)
		return res
	}
	r, _, err := e.runnerWithKey(sub, kp)
	if err != nil {
		return failed(res, err)
	}
	exists, err := r.Exists(ctx)
	if err != nil {
		return failed(res, err)
	}
	if !exists {
		return failed(res, fmt.Errorf("the key for %s exists but its repository does not; nothing was recorded", sub.Name))
	}
	if err := keys.RecordOwner(e.Cfg.KeysDir, sub.Name, sub.ID); err != nil {
		return failed(res, err)
	}
	res.OK = true
	res.Note = fmt.Sprintf("recorded id %q as the repository's owner", sub.ID)
	return res
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

// GlobEscape escapes s for a restic --include or --exclude pattern, so that
// a literal \, *, ? or [ in a path matches itself.
func GlobEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)
	return r.Replace(s)
}

// unitOrder is the order in which a subscription's units run.
var unitOrder = []string{MailTag, FilesTag, DBTag}

// hasUnits reports whether the subscription has any unit to back up.
// The panel configuration's repository always has.
func hasUnits(sub inventory.Subscription) bool {
	if sub.Name == config.ServerName {
		return true
	}
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
//
// config.ServerName backs up the server's units instead (BackupServer).
func (e Env) BackupSubscription(ctx context.Context, sub inventory.Subscription, unit string) []Result {
	if sub.Name == config.ServerName {
		return e.BackupServer(ctx, unit)
	}
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
		if unit == PanelConfigTag {
			break
		}
		rs := e.guarded(s, func() []Result { return e.BackupSubscription(ctx, s, unit) }, e.notTried(s, unit))
		e.progress(rs)
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
	var rs []Result
	if (unit == "" || unit == PanelConfigTag) && e.Inv.Server.Has(PanelConfigTag) {
		rs = e.guarded(ServerSubscription(), func() []Result { return e.runServer(ctx, unit) }, e.notTried(ServerSubscription(), PanelConfigTag))
	} else {
		rs = e.runServer(ctx, unit)
	}
	e.progress(rs)
	results = append(results, rs...)
	for _, r := range rs {
		if r.Snapshot != "" {
			touched = append(touched, ServerSubscription())
			break
		}
	}
	for _, s := range touched {
		if ctx.Err() != nil {
			break
		}
		st := e.Stats(ctx, s)
		e.progress([]Result{st})
		results = append(results, st)
	}
	return results
}

// notTried gives sub's backup results when the breaker has given up on
// its destination: one failed result per unit the backup would have run.
func (e Env) notTried(sub inventory.Subscription, unit string) func(msg string) []Result {
	return func(msg string) []Result {
		var out []Result
		for _, u := range plannedUnits(sub, unit) {
			r := e.result(sub.Name, u[0], "backup")
			r.Database = u[1]
			out = append(out, failed(r, errors.New(msg)))
		}
		return out
	}
}

// runServer runs the server's own units after every subscription in
// BackupAll: panel-config, when the inventory's server lists it. When
// --unit panel-config was asked for and the server does not list it, the
// result says so.
func (e Env) runServer(ctx context.Context, unit string) []Result {
	switch {
	case unit != "" && unit != PanelConfigTag:
		return nil
	case unit == "" && !e.Inv.Server.Has(PanelConfigTag):
		return nil
	}
	return e.BackupServer(ctx, PanelConfigTag)
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
		return e.noRepository(res, sub, err)
	}
	if dst.Prune == "remote" {
		// The destination host must group and keep exactly as a local
		// forget would; restic's default grouping (host, paths) differs.
		k := e.RetentionFor(sub)
		res.OK = true
		repo, _ := config.RedactRepository(dst.Repository)
		res.Note = fmt.Sprintf("pruning runs on the destination host (prune: remote); it must run, for the repository %s: "+
			"restic forget --group-by %s --keep-daily %d --keep-weekly %d --keep-monthly %d --prune",
			repo, GroupBy, k.Daily, k.Weekly, k.Monthly)
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

// noRepository turns Runner's error for forget and check into the result:
// a repository without a key has never been backed up into (a db-only
// subscription without databases, or one not yet backed up), which is a
// note. Any other error, such as a key readable by others or a repository
// recorded as another id's, fails.
func (e Env) noRepository(res Result, sub inventory.Subscription, err error) Result {
	if !errors.Is(err, keys.ErrNoKey) {
		return failed(res, err)
	}
	if oerr := keys.CheckOwner(e.Cfg.KeysDir, sub.Name, sub.ID); oerr != nil {
		return failed(res, oerr)
	}
	// An owner file is written before the repository is created, so with
	// one the repository may exist and its key is what is missing.
	if has, oerr := keys.HasOwner(e.Cfg.KeysDir, sub.Name); oerr != nil || has {
		if oerr == nil {
			oerr = fmt.Errorf("the key for %s is missing but its owner file exists, so its repository may exist and cannot be opened: %w", sub.Name, err)
		}
		return failed(res, oerr)
	}
	res.OK = true
	res.Note = NoRepositoryNote
	return res
}

// NoRepositoryNote is the note of forget and check for a repository that
// does not exist yet.
const NoRepositoryNote = "no repository yet (nothing has been backed up)"

func (e Env) Check(ctx context.Context, sub inventory.Subscription, subset string) Result {
	res := e.result(sub.Name, "", "check")
	if !hasUnits(sub) {
		res.OK = true
		res.Note = noUnits
		return res
	}
	r, _, err := e.Runner(sub)
	if err != nil {
		return e.noRepository(res, sub, err)
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

// LockPoll is how often LockWait tries the run lock again; a variable so
// that tests can shorten it.
var LockPoll = 10 * time.Second

// LockWait takes the run lock like Lock, waiting up to max for another run
// to release it, or until ctx is cancelled.
func LockWait(ctx context.Context, stateDir string, max time.Duration) (func(), error) {
	deadline := time.Now().Add(max)
	for {
		unlock, err := Lock(stateDir)
		if err == nil || time.Now().Add(LockPoll).After(deadline) {
			if err != nil {
				err = fmt.Errorf("%w (waited %s)", err, max)
			}
			return unlock, err
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("%w (stopped waiting: %v)", err, ctx.Err())
		case <-time.After(LockPoll):
		}
	}
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

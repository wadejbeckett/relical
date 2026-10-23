// Package job runs backup, forget and check for one subscription at a time
// and records the outcome in state.json.
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

// CommonExcludes are Dovecot's rebuildable index and cache files.
var CommonExcludes = []string{"**/dovecot.index*", "**/dovecot.list.index*"}

// MailTag tags mail snapshots.
const MailTag = "mail"

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
	Action       string    `json:"action"`
	Time         time.Time `json:"time"`
	OK           bool      `json:"ok"`
	Snapshot     string    `json:"snapshot,omitempty"`
	Mailboxes    int       `json:"mailboxes,omitempty"`
	FilesNew     uint64    `json:"files_new,omitempty"`
	DataAdded    uint64    `json:"data_added,omitempty"`
	Seconds      float64   `json:"seconds,omitempty"`
	Note         string    `json:"note,omitempty"`
	Error        string    `json:"error,omitempty"`
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

// Runner returns a runner for a subscription that already has a key.
func (e Env) Runner(sub inventory.Subscription) (restic.Runner, config.Destination, error) {
	kp, err := keys.Path(e.Cfg.KeysDir, sub.Name)
	if err != nil {
		return restic.Runner{}, config.Destination{}, err
	}
	return e.runnerWithKey(sub, kp)
}

func (e Env) result(sub, unit, action string) Result {
	return Result{Subscription: sub, Unit: unit, Action: action, Time: e.Now().UTC()}
}

func failed(r Result, err error) Result {
	r.Error = err.Error()
	return r
}

// Mailboxes lists the mailboxes of every mail domain of the subscription.
// A mail domain with no mail directory has no mailboxes.
func (e Env) Mailboxes(sub inventory.Subscription) ([]locator.Mailbox, error) {
	all := []locator.Mailbox{}
	for _, d := range sub.MailDomains {
		boxes, err := e.Locator.Mailboxes(d)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		all = append(all, boxes...)
	}
	return all, nil
}

// Backup backs up the subscription's mail, across all its mail domains,
// into one snapshot tagged mail in the subscription's repository.
func (e Env) Backup(ctx context.Context, sub inventory.Subscription) Result {
	res := e.result(sub.Name, MailTag, "backup")
	if !sub.Has("mail") {
		res.OK = true
		res.Note = "mail is not among the subscription's features; nothing to back up"
		return res
	}
	boxes, err := e.Mailboxes(sub)
	if err != nil {
		return failed(res, err)
	}
	if len(boxes) == 0 {
		return failed(res, errors.New("no mailboxes found"))
	}
	kp, _, err := keys.Ensure(e.Cfg.KeysDir, sub.Name)
	if err != nil {
		return failed(res, err)
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
		if err := r.Init(ctx); err != nil {
			return failed(res, err)
		}
	}
	paths := []string{}
	excludes := append([]string{}, CommonExcludes...)
	for _, b := range boxes {
		paths = append(paths, b.Path)
		p := globEscape(b.Path)
		excludes = append(excludes, p+"/tmp/*", p+"/.*/tmp/*")
	}
	sum, err := r.Backup(ctx, paths, []string{MailTag}, excludes)
	if err != nil && restic.ExitCode(err) != restic.ExitPartial {
		return failed(res, err)
	}
	if err != nil {
		if sum.SnapshotID == "" {
			return failed(res, err)
		}
		res.Note = "restic could not read some files (they may have changed, vanished or be unreadable); the snapshot was saved: " + err.Error()
	}
	res.OK = true
	res.Snapshot = sum.SnapshotID
	res.Mailboxes = len(boxes)
	res.FilesNew = sum.FilesNew
	res.DataAdded = sum.DataAddedPacked
	res.Seconds = sum.TotalDuration
	return res
}

func globEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `*`, `\*`, `?`, `\?`, `[`, `\[`)
	return r.Replace(s)
}

// hasUnits reports whether this build backs up anything for sub; until
// files and databases are implemented, only mail is.
func hasUnits(sub inventory.Subscription) bool { return sub.Has("mail") }

const noUnits = "the subscription has no feature this version backs up; nothing to do"

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
	if err := r.Forget(ctx, MailTag, e.RetentionFor(sub), true); err != nil {
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

func ReadState(stateDir string) (map[string]map[string]Result, error) {
	st := map[string]map[string]Result{}
	b, err := os.ReadFile(filepath.Join(stateDir, "state.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return nil, err
	}
	return st, json.Unmarshal(b, &st)
}

// Record merges results into state.json, written atomically.
func (e Env) Record(results []Result) error {
	st, err := ReadState(e.Cfg.StateDir)
	if err != nil {
		return err
	}
	for _, r := range results {
		if st[r.Subscription] == nil {
			st[r.Subscription] = map[string]Result{}
		}
		st[r.Subscription][r.Action] = r
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

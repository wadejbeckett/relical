// Package job runs backup, forget and check for one domain at a time and
// records the outcome in state.json.
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
	"github.com/wadejbeckett/relical/core/internal/keys"
	"github.com/wadejbeckett/relical/core/internal/locator"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

// CommonExcludes are Dovecot's rebuildable index and cache files.
var CommonExcludes = []string{"**/dovecot.index*", "**/dovecot.list.index*"}

type Env struct {
	Cfg      *config.Config
	Locator  locator.Pattern
	Now      func() time.Time
	ExtraEnv map[string]string // tests only
}

type Result struct {
	Domain    string    `json:"domain"`
	Action    string    `json:"action"`
	Time      time.Time `json:"time"`
	OK        bool      `json:"ok"`
	Snapshot  string    `json:"snapshot,omitempty"`
	Mailboxes int       `json:"mailboxes,omitempty"`
	FilesNew  uint64    `json:"files_new,omitempty"`
	DataAdded uint64    `json:"data_added,omitempty"`
	Seconds   float64   `json:"seconds,omitempty"`
	Note      string    `json:"note,omitempty"`
	Error     string    `json:"error,omitempty"`
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

func (e Env) runnerWithKey(domain, keyPath string) (restic.Runner, config.Destination, error) {
	_, dst, err := e.Cfg.DestinationFor(domain)
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
		LimitUploadKiB: e.Cfg.LimitUploadKiB, LowPriority: e.Cfg.LowPriority,
	}, dst, nil
}

// Runner returns a runner for a domain that already has a key.
func (e Env) Runner(domain string) (restic.Runner, config.Destination, error) {
	kp, err := keys.Path(e.Cfg.KeysDir, domain)
	if err != nil {
		return restic.Runner{}, config.Destination{}, err
	}
	return e.runnerWithKey(domain, kp)
}

func (e Env) result(domain, action string) Result {
	return Result{Domain: domain, Action: action, Time: e.Now().UTC()}
}

func failed(r Result, err error) Result {
	r.Error = err.Error()
	return r
}

func (e Env) Backup(ctx context.Context, domain string) Result {
	res := e.result(domain, "backup")
	if !e.Cfg.Enabled(domain) {
		return failed(res, fmt.Errorf("domain %s is not enabled in the config", domain))
	}
	boxes, err := e.Locator.Mailboxes(domain)
	if err != nil {
		return failed(res, err)
	}
	if len(boxes) == 0 {
		return failed(res, errors.New("no mailboxes found"))
	}
	kp, _, err := keys.Ensure(e.Cfg.KeysDir, domain)
	if err != nil {
		return failed(res, err)
	}
	r, _, err := e.runnerWithKey(domain, kp)
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
	sum, err := r.Backup(ctx, paths, []string{"backup"}, excludes)
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

func (e Env) Forget(ctx context.Context, domain string) Result {
	res := e.result(domain, "forget")
	r, dst, err := e.Runner(domain)
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
	if err := r.Forget(ctx, "backup", e.Cfg.Retention, true); err != nil {
		return failed(res, err)
	}
	res.OK = true
	return res
}

func (e Env) Check(ctx context.Context, domain, subset string) Result {
	res := e.result(domain, "check")
	r, _, err := e.Runner(domain)
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
		if st[r.Domain] == nil {
			st[r.Domain] = map[string]Result{}
		}
		st[r.Domain][r.Action] = r
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

package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/keys"
)

// PanelConfigTimeout limits server.config_command; a variable so tests
// can shorten it.
var PanelConfigTimeout = 30 * time.Minute

// maxCommandStderr is how much of config_command's stderr is kept.
const maxCommandStderr = 64 << 10

// stagingPrefix starts the name of every panel configuration file in the
// staging directory.
const stagingPrefix = "panel-config-"

// ServerSubscription is the panel configuration's repository: named and
// owned by config.ServerName, on the default destination, with the
// default retention.
func ServerSubscription() inventory.Subscription {
	return inventory.Subscription{ID: config.ServerName, Name: config.ServerName}
}

// StagingDir is where config_command writes the panel configuration
// before it is backed up; Prepare creates it with mode 0700.
func (e Env) StagingDir() string { return filepath.Join(e.Cfg.StateDir, "staging") }

// BackupServer backs up the server's own units: panel-config, when the
// inventory's server lists it. unit, when not empty, must be panel-config.
func (e Env) BackupServer(ctx context.Context, unit string) []Result {
	if unit != "" && unit != PanelConfigTag {
		res := e.result(config.ServerName, unit, "backup")
		res.OK = true
		res.Note = unit + " is not a unit of " + config.ServerName + "; nothing to back up"
		return []Result{res}
	}
	if !e.Inv.Server.Has(PanelConfigTag) {
		res := e.result(config.ServerName, PanelConfigTag, "backup")
		res.OK = true
		res.Note = "the inventory's server does not list panel-config; nothing to back up"
		return []Result{res}
	}
	return []Result{e.BackupPanelConfig(ctx)}
}

// BackupPanelConfig runs server.config_command, which writes the panel's
// configuration into a root-only staging file, backs the file up into the
// _server repository tagged panel-config, and deletes it, also when
// anything fails. With encrypt_with_repo_key, the command gets the
// _server repository's key as PLESK_BACKUP_PASSWORD.
func (e Env) BackupPanelConfig(ctx context.Context) Result {
	res := e.result(config.ServerName, PanelConfigTag, "backup")
	srv := e.Inv.Server
	if !srv.Has(PanelConfigTag) {
		res.OK = true
		res.Note = "the inventory's server does not list panel-config; nothing to back up"
		return res
	}
	sub := ServerSubscription()
	staging, err := e.checkStaging()
	if err != nil {
		return failed(res, err)
	}
	r, err := e.openRepo(ctx, sub)
	if err != nil {
		return failed(res, err)
	}
	env := []string{"PATH=" + os.Getenv("PATH")}
	var secret string
	if srv.Encrypt() {
		kp, err := keys.Path(e.Cfg.KeysDir, sub.Name)
		if err != nil {
			return failed(res, err)
		}
		b, err := os.ReadFile(kp)
		if err != nil {
			return failed(res, err)
		}
		secret = strings.TrimSpace(string(b))
		env = append(env, "PLESK_BACKUP_PASSWORD="+secret)
	}
	out := filepath.Join(staging, fmt.Sprintf("%s%s.%s", stagingPrefix, e.Now().UTC().Format("20060102T150405Z"), srv.ConfigExt))
	// Whatever happens below, the unencrypted file does not outlive the run.
	defer os.Remove(out)
	argv := make([]string, len(srv.ConfigCommand))
	for i, a := range srv.ConfigCommand {
		argv[i] = strings.ReplaceAll(a, "{output}", out)
	}
	if err := runConfigCommand(ctx, argv, env, secret); err != nil {
		return failed(res, err)
	}
	fi, err := os.Lstat(out)
	switch {
	case err != nil:
		return failed(res, fmt.Errorf("config_command exited 0 but wrote no %s: %w", out, err))
	case !fi.Mode().IsRegular():
		return failed(res, fmt.Errorf("config_command exited 0 but %s is not a regular file", out))
	case fi.Size() == 0:
		return failed(res, fmt.Errorf("config_command exited 0 but %s is empty", out))
	}
	sum, err := r.Backup(ctx, []string{out}, []string{PanelConfigTag}, nil)
	if err != nil {
		// A partial backup of a single file is a failed one.
		return failed(res, err)
	}
	res.OK = true
	res.Snapshot = sum.SnapshotID
	res.FilesNew = sum.FilesNew
	res.DataAdded = sum.DataAddedPacked
	res.Seconds = sum.TotalDuration
	return res
}

// checkStaging requires the staging directory to be a real directory with
// mode 0700 and, when relical runs as root, owned by root, and removes
// panel configuration files a killed run left behind.
func (e Env) checkStaging() (string, error) {
	dir := e.StagingDir()
	fi, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("staging directory: %w", err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0o700 {
		return "", fmt.Errorf("staging directory %s must be a directory with mode 0700, not a symlink", dir)
	}
	if asRoot() {
		if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return "", fmt.Errorf("staging directory %s is not owned by root; chown root %s", dir, dir)
		}
	}
	return dir, e.CleanStaging()
}

// CleanStaging removes panel configuration files that a killed run left
// in the staging directory (a SIGKILL skips the deferred removal). Call
// it only while holding the run lock, so that it never removes the file
// of a backup in progress.
func (e Env) CleanStaging() error {
	old, err := filepath.Glob(filepath.Join(e.StagingDir(), stagingPrefix+"*"))
	if err != nil {
		return err
	}
	for _, p := range old {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing a staging file left by an earlier run: %w", err)
		}
	}
	return nil
}

// runConfigCommand runs argv with env only (secret, when not empty, is
// replaced by *** in the stderr it reports), in its own process group, so
// that a timeout or an interrupted run kills the whole group (the panel's
// backup tool starts children of its own).
func runConfigCommand(ctx context.Context, argv, env []string, secret string) error {
	ctx, cancel := context.WithTimeout(ctx, PanelConfigTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = env
	stderr := &tailBuffer{max: maxCommandStderr}
	cmd.Stdout, cmd.Stderr = stderr, stderr
	err := cmd.Run()
	if cmd.Process != nil {
		// Also after a normal exit: a child left in the group could still
		// be writing the staging file, or write it again after it is deleted.
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	msg := strings.TrimSpace(stderr.String())
	if secret != "" {
		// The tail goes into the result, state and the report: never the
		// key, should the command print its PLESK_BACKUP_PASSWORD.
		msg = strings.ReplaceAll(msg, secret, "***")
	}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return fmt.Errorf("config_command: timed out after %s and was killed: %s", PanelConfigTimeout, msg)
	case ctx.Err() != nil:
		return fmt.Errorf("config_command: interrupted (%v): %s", ctx.Err(), msg)
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("config_command: exit %d: %s", ee.ExitCode(), msg)
		}
		return fmt.Errorf("config_command: %w", err)
	}
	return nil
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	buf bytes.Buffer
	max int
}

func (w *tailBuffer) Write(p []byte) (int, error) {
	w.buf.Write(p)
	if extra := w.buf.Len() - w.max; extra > 0 {
		w.buf.Next(extra)
	}
	return len(p), nil
}

func (w *tailBuffer) String() string { return w.buf.String() }

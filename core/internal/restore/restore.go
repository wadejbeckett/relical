// Package restore restores a mailbox (or one IMAP folder of it) from a
// snapshot into a new folder "Restored <date> <id>" through doveadm import.
// It never overwrites existing mail.
package restore

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

type Request struct {
	Domain   string
	Mailbox  string
	Snapshot string // "latest", or 8–64 hex characters
	Folder   string // IMAP folder name; empty restores the whole mailbox
}

type Result struct {
	Domain     string `json:"domain"`
	Mailbox    string `json:"mailbox"`
	Snapshot   string `json:"snapshot"`
	RestoredTo string `json:"restored_to"`
	Messages   int    `json:"messages"`
}

var snapshotRE = regexp.MustCompile(`^(latest|[0-9a-f]{8,64})$`)

func pick(snaps []restic.Snapshot, want string) (restic.Snapshot, error) {
	if len(snaps) == 0 {
		return restic.Snapshot{}, errors.New("no backup snapshots")
	}
	if want == "latest" {
		latest := snaps[0]
		for _, s := range snaps[1:] {
			if s.Time.After(latest.Time) {
				latest = s
			}
		}
		return latest, nil
	}
	var found []restic.Snapshot
	for _, s := range snaps {
		if strings.HasPrefix(s.ID, want) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return restic.Snapshot{}, fmt.Errorf("snapshot %s not found", want)
	case 1:
		return found[0], nil
	}
	return restic.Snapshot{}, fmt.Errorf("snapshot id %s is ambiguous", want)
}

func doveadm(ctx context.Context, env job.Env, args ...string) ([]byte, error) {
	out, _, err := doveadmRun(ctx, env, args...)
	return out, err
}

// doveadmRun also returns stderr, for the callers that must read it.
func doveadmRun(ctx context.Context, env job.Env, args ...string) (out, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, env.Cfg.DoveadmBin, args...)
	// On cancellation ask doveadm to stop cleanly; kill it only if it has not exited a minute later.
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 60 * time.Second
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env.ExtraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var o, e bytes.Buffer
	cmd.Stdout, cmd.Stderr = &o, &e
	if err := cmd.Run(); err != nil {
		return nil, e.Bytes(), fmt.Errorf("doveadm %s: %w: %s", args[0], err, strings.TrimSpace(e.String()))
	}
	return o.Bytes(), e.Bytes(), nil
}

// doveadmErrorRE matches doveadm's error lines, which look like
// "doveadm(user@example.test)<pid><session>: Error: message" or "Error: message".
var doveadmErrorRE = regexp.MustCompile(`(^|: )Error: `)

// errorLinesMentioning returns the doveadm error lines in stderr that contain substr.
func errorLinesMentioning(stderr []byte, substr string) []string {
	var lines []string
	for _, l := range strings.Split(string(stderr), "\n") {
		if doveadmErrorRE.MatchString(l) && strings.Contains(l, substr) {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

// exitMailboxMissing is doveadm's exit code for a mailbox that does not exist.
const exitMailboxMissing = 68

func validFolder(f string) bool {
	for _, r := range f {
		if unicode.IsControl(r) {
			return false
		}
	}
	return len(f) <= 255
}

func Run(ctx context.Context, env job.Env, req Request) (Result, error) {
	res := Result{Domain: req.Domain, Mailbox: req.Mailbox}
	if !snapshotRE.MatchString(req.Snapshot) {
		return res, fmt.Errorf("snapshot must be latest or 8-64 hex characters, got %q", req.Snapshot)
	}
	if !validFolder(req.Folder) {
		return res, errors.New("folder name contains control characters")
	}
	box, err := env.Locator.Find(req.Domain, req.Mailbox)
	if err != nil {
		return res, err
	}
	r, _, err := env.Runner(req.Domain)
	if err != nil {
		return res, err
	}
	snaps, err := r.Snapshots(ctx, "backup")
	if err != nil {
		return res, err
	}
	snap, err := pick(snaps, req.Snapshot)
	if err != nil {
		return res, err
	}
	res.Snapshot = snap.ID
	target := fmt.Sprintf("Restored %s %s", snap.Time.UTC().Format("2006-01-02"), snap.Short())
	res.RestoredTo = target

	list, err := doveadm(ctx, env, "mailbox", "list", "-u", box.Login)
	if err != nil {
		return res, err
	}
	sc := bufio.NewScanner(bytes.NewReader(list))
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), target) {
			return res, fmt.Errorf("folder %q already exists in %s (it may be from an earlier, possibly incomplete restore of this snapshot); delete or rename it to restore this snapshot again", target, box.Login)
		}
	}

	stage, err := os.MkdirTemp(filepath.Join(env.Cfg.StateDir, "restore"), "run-")
	if err != nil {
		return res, err
	}
	defer os.RemoveAll(stage)
	if err := r.Restore(ctx, snap.ID, box.Path, stage); err != nil {
		return res, err
	}
	// doveadm drops to the mail user, which must be able to read the staging copy.
	err = filepath.WalkDir(stage, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, box.UID, box.GID)
	})
	if err != nil {
		return res, err
	}
	src := "maildir:" + filepath.Join(stage, box.Path) + ":INDEX=MEMORY"
	args := []string{"import", "-s", "-u", box.Login, src, target}
	if req.Folder == "" {
		args = append(args, "all")
	} else {
		args = append(args, "mailbox", req.Folder)
	}
	_, impErr, err := doveadmRun(ctx, env, args...)
	if err == nil {
		// doveadm import exits 0 when it cannot read the source and only logs an
		// error naming it. Other Error lines (for example Dovecot repairing a
		// corrupt cache in the live mailbox) do not mean the import failed.
		if lines := errorLinesMentioning(impErr, stage); len(lines) > 0 {
			err = fmt.Errorf("doveadm import reported errors reading the restored copy despite exit 0: %s", strings.Join(lines, "; "))
		}
	}
	if err != nil {
		// The target did not exist before this run, so whatever is there now is ours to remove.
		// The import may have failed because ctx was cancelled, so clean up on a fresh, bounded context.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, derr := doveadm(cctx, env, "mailbox", "delete", "-r", "-s", "-u", box.Login, target)
		var ee *exec.ExitError
		if errors.As(derr, &ee) && ee.ExitCode() == exitMailboxMissing {
			derr = nil // the import failed before creating the folder: already clean
		}
		if derr != nil {
			return res, fmt.Errorf("%v; removing the partial folder %s also failed: %v", err, target, derr)
		}
		return res, err
	}
	status, err := doveadm(ctx, env, "-f", "flow", "mailbox", "status", "-u", box.Login, "messages", target+"*")
	if err != nil {
		return res, err
	}
	named := false
	sc = bufio.NewScanner(bytes.NewReader(status))
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), target) {
			named = true
		}
	}
	if !named {
		return res, fmt.Errorf("doveadm import reported success but no %q folder exists for %s", target, box.Login)
	}
	for _, f := range strings.Fields(string(status)) {
		if n, ok := strings.CutPrefix(f, "messages="); ok {
			v, _ := strconv.Atoi(n)
			res.Messages += v
		}
	}
	return res, nil
}

// Package restic runs the restic binary and parses its JSON output.
package restic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
)

// restic exit codes (restic 0.19 scripting docs).
const (
	ExitPartial       = 3  // backup saved a snapshot but could not read some files
	ExitNoRepository  = 10 // repository does not exist
	ExitLocked        = 11
	ExitWrongPassword = 12
)

type ExitError struct {
	Command   string
	Code      int
	Stderr    string
	Cancelled error // the context error when the run was cancelled
}

func (e *ExitError) Error() string {
	lines := strings.Split(strings.TrimSpace(e.Stderr), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
	}
	if e.Cancelled != nil {
		return fmt.Sprintf("restic %s: cancelled (%v): exit %d: %s", e.Command, e.Cancelled, e.Code, strings.Join(lines, " | "))
	}
	return fmt.Sprintf("restic %s: exit %d: %s", e.Command, e.Code, strings.Join(lines, " | "))
}

// ExitCode returns restic's exit code from err, or -1.
func ExitCode(err error) int {
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

type Runner struct {
	Bin            string
	Repository     string
	PasswordFile   string
	Host           string
	CacheDir       string
	Env            map[string]string
	LimitUploadKiB int
	LowPriority    bool
	SftpArgs       string // passed as -o sftp.args=... when set
}

type BackupSummary struct {
	FilesNew            uint64  `json:"files_new"`
	FilesChanged        uint64  `json:"files_changed"`
	FilesUnmodified     uint64  `json:"files_unmodified"`
	DataAdded           uint64  `json:"data_added"`
	DataAddedPacked     uint64  `json:"data_added_packed"`
	TotalFilesProcessed uint64  `json:"total_files_processed"`
	TotalBytesProcessed uint64  `json:"total_bytes_processed"`
	TotalDuration       float64 `json:"total_duration"`
	SnapshotID          string  `json:"snapshot_id"`
}

type Snapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Paths    []string  `json:"paths"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags"`
}

// Short is the 8-character form (restic's short_id field is deprecated).
func (s Snapshot) Short() string {
	if len(s.ID) < 8 {
		return s.ID
	}
	return s.ID[:8]
}

// RetryLock is how long backup, check and forget wait for a repository
// lock held by another live restic process before giving up.
const RetryLock = "30m"

// retriesLock lists the commands that get --retry-lock.
var retriesLock = map[string]bool{"backup": true, "check": true, "forget": true}

// command builds the exec.Cmd. The environment is built from scratch so
// that RESTIC_* variables from the caller never leak in. Cancelling ctx
// sends SIGTERM, on which restic removes its own lock; restic is killed
// only if it has not exited 60 seconds later.
func (r Runner) command(ctx context.Context, args ...string) *exec.Cmd {
	full := []string{}
	if r.SftpArgs != "" {
		// restic parses each -o value as CSV: quoted, a comma in the
		// value (such as -o Ciphers=a,b) is kept instead of cutting it off.
		full = append(full, "-o", `"sftp.args=`+strings.ReplaceAll(r.SftpArgs, `"`, `""`)+`"`)
	}
	if r.LimitUploadKiB > 0 {
		full = append(full, "--limit-upload", strconv.Itoa(r.LimitUploadKiB))
	}
	if len(args) > 0 && retriesLock[args[0]] {
		full = append(full, "--retry-lock", RetryLock)
	}
	full = append(full, args...)
	name := r.Bin
	if r.LowPriority {
		full = append([]string{"-n", "19", "ionice", "-c", "3", r.Bin}, full...)
		name = "nice"
	}
	cmd := exec.CommandContext(ctx, name, full...)
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 60 * time.Second
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"RESTIC_REPOSITORY=" + r.Repository,
		"RESTIC_PASSWORD_FILE=" + r.PasswordFile,
		"RESTIC_HOST=" + r.Host,
		"RESTIC_CACHE_DIR=" + r.CacheDir,
	}
	for k, v := range r.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	return cmd
}

func (r Runner) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := r.command(ctx, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		var xe *exec.ExitError
		if errors.As(err, &xe) {
			return stdout.Bytes(), &ExitError{Command: args[0], Code: xe.ExitCode(), Stderr: stderr.String(), Cancelled: ctx.Err()}
		}
		return stdout.Bytes(), err
	}
	return stdout.Bytes(), nil
}

// Exists reports whether the repository exists. A wrong password is an
// error, never "does not exist": a lost key must not lead to a new init.
func (r Runner) Exists(ctx context.Context) (bool, error) {
	_, err := r.run(ctx, "cat", "config")
	if err == nil {
		return true, nil
	}
	if ExitCode(err) == ExitNoRepository {
		return false, nil
	}
	return false, err
}

func (r Runner) Init(ctx context.Context) error {
	_, err := r.run(ctx, "init", "--repository-version", "2")
	return err
}

// Backup returns the summary. On exit 3 (partial) it returns the summary
// and the ExitError together, because the snapshot was saved. extra are
// further backup flags, such as --exclude-caches.
func (r Runner) Backup(ctx context.Context, paths, tags, excludes []string, extra ...string) (BackupSummary, error) {
	args := []string{"backup", "--json", "--one-file-system"}
	args = append(args, extra...)
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	for _, e := range excludes {
		args = append(args, "--exclude", e)
	}
	args = append(args, paths...)
	out, err := r.run(ctx, args...)
	sum, perr := parseSummary(out)
	if err != nil {
		if ExitCode(err) == ExitPartial && perr == nil {
			return sum, err
		}
		return BackupSummary{}, err
	}
	return sum, perr
}

// BackupStdin runs command and saves its standard output as the single
// file filename (restic backup --stdin-from-command), so the data never
// touches the disk unencrypted. restic 0.19 fails, and saves no
// snapshot, when the command exits non-zero; the command's stderr is
// part of restic's and so of the ExitError.
func (r Runner) BackupStdin(ctx context.Context, command []string, filename string, tags []string) (BackupSummary, error) {
	if len(command) == 0 {
		return BackupSummary{}, errors.New("restic backup: no command for --stdin-from-command")
	}
	args := []string{"backup", "--json", "--stdin-from-command", "--stdin-filename", filename}
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	args = append(args, "--")
	args = append(args, command...)
	out, err := r.run(ctx, args...)
	if err != nil {
		return BackupSummary{}, err
	}
	return parseSummary(out)
}

func parseSummary(out []byte) (BackupSummary, error) {
	var s BackupSummary
	found := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var m struct {
			MessageType string `json:"message_type"`
		}
		line := sc.Bytes()
		if json.Unmarshal(line, &m) != nil || m.MessageType != "summary" {
			continue
		}
		if err := json.Unmarshal(line, &s); err != nil {
			return s, err
		}
		found = true
	}
	if err := sc.Err(); err != nil {
		return s, err
	}
	if !found || s.SnapshotID == "" {
		return s, errors.New("restic backup: no summary with a snapshot id in the output")
	}
	return s, nil
}

func (r Runner) Snapshots(ctx context.Context, tag string) ([]Snapshot, error) {
	args := []string{"snapshots", "--json"}
	if tag != "" {
		args = append(args, "--tag", tag)
	}
	out, err := r.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var s []Snapshot
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, fmt.Errorf("restic snapshots: %w", err)
	}
	return s, nil
}

// Forget applies keep to every group of snapshots (groupBy, for example
// "tags", so mail, files and each database are kept apart) and prunes
// when prune is set. It considers every snapshot in the repository.
func (r Runner) Forget(ctx context.Context, groupBy string, keep config.Retention, prune bool) error {
	args := []string{"forget", "--group-by", groupBy,
		"--keep-daily", strconv.Itoa(keep.Daily),
		"--keep-weekly", strconv.Itoa(keep.Weekly),
		"--keep-monthly", strconv.Itoa(keep.Monthly)}
	if prune {
		args = append(args, "--prune")
	}
	_, err := r.run(ctx, args...)
	return err
}

// Stats returns the bytes the repository stores (restic stats --mode
// raw-data, total_size: after deduplication and compression).
func (r Runner) Stats(ctx context.Context) (uint64, error) {
	out, err := r.run(ctx, "stats", "--mode", "raw-data", "--json")
	if err != nil {
		return 0, err
	}
	var s struct {
		TotalSize *uint64 `json:"total_size"`
	}
	if err := json.Unmarshal(out, &s); err != nil {
		return 0, fmt.Errorf("restic stats: %w", err)
	}
	if s.TotalSize == nil {
		return 0, errors.New("restic stats: no total_size in the output")
	}
	return *s.TotalSize, nil
}

func (r Runner) Check(ctx context.Context, readDataSubset string) error {
	args := []string{"check"}
	if readDataSubset != "" {
		args = append(args, "--read-data-subset", readDataSubset)
	}
	_, err := r.run(ctx, args...)
	return err
}

// Restore restores snapshotID under target, limited to the include
// patterns when any are given. restic exits 0 when the patterns match
// nothing, so callers check what was restored (or use Ls first).
func (r Runner) Restore(ctx context.Context, snapshotID, target string, include []string) error {
	args := []string{"restore", snapshotID, "--target", target}
	for _, i := range include {
		args = append(args, "--include", i)
	}
	_, err := r.run(ctx, args...)
	return err
}

// Node is one entry of restic ls --json (restic 0.19: one JSON object per
// line, the snapshot first, then message_type "node" per entry).
type Node struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"` // file, dir, symlink, dev, chardev, fifo, socket
	Path  string    `json:"path"`
	Size  uint64    `json:"size"`
	MTime time.Time `json:"mtime"`
}

// Ls lists path in snapshotID: for a directory, the directory itself and
// its direct children (restic ls without --recursive); for a file, the
// file. A path not in the snapshot gives no nodes and no error. At most
// max nodes are returned; truncated is true when there were more, and
// restic is then stopped without reading the rest.
func (r Runner) Ls(ctx context.Context, snapshotID, path string, max int) (nodes []Node, truncated bool, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := r.command(ctx, "ls", "--json", snapshotID, path)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, false, err
	}
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	var perr error
	for sc.Scan() {
		var m struct {
			MessageType string `json:"message_type"`
		}
		if json.Unmarshal(sc.Bytes(), &m) != nil || m.MessageType != "node" {
			continue
		}
		if len(nodes) == max {
			truncated = true
			break
		}
		var n Node
		if perr = json.Unmarshal(sc.Bytes(), &n); perr != nil {
			perr = fmt.Errorf("restic ls: %w", perr)
			break
		}
		nodes = append(nodes, n)
	}
	if perr == nil && !truncated {
		perr = sc.Err()
	}
	if truncated || perr != nil {
		cancel() // stop restic; its exit status no longer matters
		io.Copy(io.Discard, out)
		cmd.Wait()
		if perr != nil {
			return nil, false, perr
		}
		return nodes, true, nil
	}
	if err := cmd.Wait(); err != nil {
		return nil, false, r.exitError(ctx, "ls", err, stderr.String())
	}
	return nodes, false, nil
}

// Dump writes the file path of snapshotID to w as restic reads it, so it
// is never staged anywhere else on disk.
func (r Runner) Dump(ctx context.Context, snapshotID, path string, w io.Writer) error {
	cmd := r.command(ctx, "dump", snapshotID, path)
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = w, &stderr
	if err := cmd.Run(); err != nil {
		return r.exitError(ctx, "dump", err, stderr.String())
	}
	return nil
}

func (r Runner) exitError(ctx context.Context, command string, err error, stderr string) error {
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		return &ExitError{Command: command, Code: xe.ExitCode(), Stderr: stderr, Cancelled: ctx.Err()}
	}
	return err
}

// Unlock removes stale locks only (never --remove-all), such as those
// left by a restic process that was killed before it could clean up.
func (r Runner) Unlock(ctx context.Context) error {
	_, err := r.run(ctx, "unlock")
	return err
}

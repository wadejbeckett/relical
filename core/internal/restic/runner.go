// Package restic runs the restic binary and parses its JSON output.
package restic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
	Command string
	Code    int
	Stderr  string
}

func (e *ExitError) Error() string {
	lines := strings.Split(strings.TrimSpace(e.Stderr), "\n")
	if len(lines) > 5 {
		lines = lines[len(lines)-5:]
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

// command builds the exec.Cmd. The environment is built from scratch so
// that RESTIC_* variables from the caller never leak in.
func (r Runner) command(ctx context.Context, args ...string) *exec.Cmd {
	full := []string{}
	if r.LimitUploadKiB > 0 {
		full = append(full, "--limit-upload", strconv.Itoa(r.LimitUploadKiB))
	}
	full = append(full, args...)
	name := r.Bin
	if r.LowPriority {
		full = append([]string{"-n", "19", "ionice", "-c", "3", r.Bin}, full...)
		name = "nice"
	}
	cmd := exec.CommandContext(ctx, name, full...)
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
			return stdout.Bytes(), &ExitError{Command: args[0], Code: xe.ExitCode(), Stderr: stderr.String()}
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
// and the ExitError together, because the snapshot was saved.
func (r Runner) Backup(ctx context.Context, paths, tags, excludes []string) (BackupSummary, error) {
	args := []string{"backup", "--json", "--one-file-system"}
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

func (r Runner) Forget(ctx context.Context, tag string, keep config.Retention, prune bool) error {
	args := []string{"forget", "--tag", tag, "--group-by", "host,tags",
		"--keep-daily", strconv.Itoa(keep.Daily),
		"--keep-weekly", strconv.Itoa(keep.Weekly),
		"--keep-monthly", strconv.Itoa(keep.Monthly)}
	if prune {
		args = append(args, "--prune")
	}
	_, err := r.run(ctx, args...)
	return err
}

func (r Runner) Check(ctx context.Context, readDataSubset string) error {
	args := []string{"check"}
	if readDataSubset != "" {
		args = append(args, "--read-data-subset", readDataSubset)
	}
	_, err := r.run(ctx, args...)
	return err
}

func (r Runner) Restore(ctx context.Context, snapshotID, include, target string) error {
	_, err := r.run(ctx, "restore", snapshotID, "--target", target, "--include", include)
	return err
}

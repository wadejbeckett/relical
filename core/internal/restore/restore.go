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
	cmd := exec.CommandContext(ctx, env.Cfg.DoveadmBin, args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	for k, v := range env.ExtraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("doveadm %s: %v: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

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
			return res, fmt.Errorf("folder %q already exists in %s; this snapshot was already restored", target, box.Login)
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
	if _, err := doveadm(ctx, env, args...); err != nil {
		return res, err
	}
	status, err := doveadm(ctx, env, "-f", "flow", "mailbox", "status", "-u", box.Login, "messages", target+"*")
	if err != nil {
		return res, err
	}
	for _, f := range strings.Fields(string(status)) {
		if n, ok := strings.CutPrefix(f, "messages="); ok {
			v, _ := strconv.Atoi(n)
			res.Messages += v
		}
	}
	return res, nil
}

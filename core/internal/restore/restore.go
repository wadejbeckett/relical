// Package restore restores a mailbox (or one IMAP folder of it) from a
// snapshot into a new folder "Restored <date> <id>" (after the configured
// mail_folder_prefix) through doveadm import.
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
	"unicode/utf16"

	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

type Request struct {
	// Subscription owns the repository. When empty, it is the
	// subscription whose mail_domains include Domain.
	Subscription string
	Domain       string
	Mailbox      string
	Snapshot     string // "latest", or 8–64 hex characters
	Folder       string // IMAP folder name; empty restores the whole mailbox
}

type Result struct {
	Subscription string `json:"subscription"`
	Unit         string `json:"unit"`
	Domain       string `json:"domain"`
	Mailbox      string `json:"mailbox"`
	Snapshot     string `json:"snapshot"`
	RestoredTo   string `json:"restored_to"`
	Messages     int    `json:"messages"`
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

// subscriptionFor resolves the subscription and checks that it owns the
// mail domain, so a restore can only read that subscription's repository.
func subscriptionFor(env job.Env, req Request) (inventory.Subscription, error) {
	if req.Subscription == "" {
		if s, ok := env.Inv.ByMailDomain(req.Domain); ok {
			return s, nil
		}
		return inventory.Subscription{}, fmt.Errorf("no subscription in the inventory has mail domain %q", req.Domain)
	}
	s, ok := env.Inv.Find(req.Subscription)
	if !ok {
		return inventory.Subscription{}, fmt.Errorf("subscription %s is not in the inventory", req.Subscription)
	}
	for _, d := range s.MailDomains {
		if d == req.Domain {
			return s, nil
		}
	}
	return inventory.Subscription{}, fmt.Errorf("mail domain %q is not one of subscription %s's mail domains", req.Domain, s.Name)
}

func Run(ctx context.Context, env job.Env, req Request) (Result, error) {
	res := Result{Subscription: req.Subscription, Unit: job.MailTag, Domain: req.Domain, Mailbox: req.Mailbox}
	// Best effort: a failure here never stops this restore.
	_ = CleanStale(env.Cfg, env.Now())
	if !snapshotRE.MatchString(req.Snapshot) {
		return res, fmt.Errorf("snapshot must be latest or 8-64 hex characters, got %q", req.Snapshot)
	}
	if !validFolder(req.Folder) {
		return res, errors.New("folder name contains control characters")
	}
	sub, err := subscriptionFor(env, req)
	if err != nil {
		return res, err
	}
	res.Subscription = sub.Name
	box, err := env.Locator.Find(req.Domain, req.Mailbox)
	if err != nil {
		return res, err
	}
	r, _, err := env.Runner(sub)
	if err != nil {
		return res, err
	}
	snaps, err := r.Snapshots(ctx, job.MailTag)
	if err != nil {
		return res, err
	}
	snap, err := pick(snaps, req.Snapshot)
	if err != nil {
		return res, err
	}
	res.Snapshot = snap.ID
	// The folder sits in the inbox namespace: Plesk's Dovecot refuses a
	// name without its "INBOX." prefix.
	target := env.Cfg.MailFolderPrefix + fmt.Sprintf("Restored %s %s", snap.Time.UTC().Format("2006-01-02"), snap.Short())
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
	if err := sc.Err(); err != nil {
		// Fail closed: an unread line could name the target folder.
		return res, fmt.Errorf("reading doveadm mailbox list for %s: %w", box.Login, err)
	}

	// The mailbox is written twice: restored into staging under
	// <state_dir>/restore, then imported by doveadm next to the live mail.
	size, entries, err := r.RestoreSize(ctx, snap.ID, box.Path)
	if err != nil {
		return res, fmt.Errorf("estimating the size of the restore: %w", err)
	}
	if err := checkSpace(size, entries, filepath.Join(env.Cfg.StateDir, "restore"), box.Path); err != nil {
		return res, err
	}
	stage, done, err := newStage(filepath.Join(env.Cfg.StateDir, "restore"), "run-")
	if err != nil {
		return res, err
	}
	defer done()
	if err := r.Restore(ctx, snap.ID, stage, []string{job.GlobEscape(box.Path)}); err != nil {
		return res, err
	}
	// restic restore exits 0 when --include matches nothing in the snapshot.
	if _, err := os.Lstat(filepath.Join(stage, box.Path)); errors.Is(err, fs.ErrNotExist) {
		return res, fmt.Errorf("mailbox %s is not in snapshot %s", box.Login, snap.Short())
	} else if err != nil {
		return res, err
	}
	// doveadm drops to the mail user, which must be able to read the staging copy.
	if err := chownStage(stage, box.UID, box.GID); err != nil {
		return res, err
	}
	var folder string
	if req.Folder != "" {
		folder = sourceFolder(req.Folder, env.Cfg.MailFolderPrefix)
		if !stagedFolderExists(filepath.Join(stage, box.Path), folder, env.Cfg.MailFolderPrefix) {
			return res, fmt.Errorf("folder %q is not in snapshot %s of %s", req.Folder, snap.Short(), box.Login)
		}
	}
	// A plain mail_driver:mail_path location: Dovecot 2.4 (Plesk 18.0.81)
	// refuses parameters such as :INDEX=MEMORY in it. Dovecot then keeps
	// its index files in the staging copy, which is deleted afterwards.
	src := "maildir:" + filepath.Join(stage, box.Path)
	// "--" ends doveadm's options: the folder is the customer's, and one
	// starting with - must be read as a name, never as an option.
	args := []string{"import", "-s", "-u", box.Login, "--", src, target}
	if req.Folder == "" {
		args = append(args, "all")
	} else {
		args = append(args, "mailbox", folder)
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
		_, derr := doveadm(cctx, env, "mailbox", "delete", "-r", "-s", "-u", box.Login, "--", target)
		var ee *exec.ExitError
		if errors.As(derr, &ee) && ee.ExitCode() == exitMailboxMissing {
			derr = nil // the import failed before creating the folder: already clean
		}
		if derr != nil {
			return res, fmt.Errorf("%v; removing the partial folder %s also failed: %v", err, target, derr)
		}
		return res, err
	}
	status, err := doveadm(ctx, env, "-f", "flow", "mailbox", "status", "-u", box.Login, "--", "messages", target+"*")
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
	if err := sc.Err(); err != nil {
		return res, fmt.Errorf("reading doveadm mailbox status for %s: %w", box.Login, err)
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

// sourceFolder is the name of folder in the staged copy. doveadm import
// opens that copy with Dovecot's default namespace, so its folders carry no
// prefix ("Sent", where the live mailbox on Plesk says "INBOX.Sent"); a
// folder given with the live prefix loses it. "INBOX" stays "INBOX".
func sourceFolder(folder, prefix string) string {
	if prefix != "" && strings.HasPrefix(folder, prefix) && len(folder) > len(prefix) {
		return folder[len(prefix):]
	}
	return folder
}

// fchownat and fchown change owners in chownStage; variables so that a
// test can record each call (and act between calls) without root.
var (
	fchownat = syscall.Fchownat
	fchown   = syscall.Fchown
)

// chownStage hands the mail staging tree at stage to uid:gid, so that
// doveadm, running as the mail user, can read it. Once a directory is the
// mail user's, they can rename entries in it and put symlinks in their
// place. So the walk works through directory descriptors (openat with
// O_DIRECTORY|O_NOFOLLOW, then fchownat with AT_SYMLINK_NOFOLLOW for
// what is not a directory) and changes children before their parent, the
// stage root last: until the walk is done, every directory above the one
// being changed is still root's mode 0700 staging, which the mail user
// cannot enter. A directory swapped for a symlink is never followed; the
// symlink itself is changed.
func chownStage(stage string, uid, gid int) error {
	fd, err := syscall.Open(stage, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("mail staging %s: %w", stage, err)
	}
	defer syscall.Close(fd)
	if err := chownChildren(fd, stage, uid, gid); err != nil {
		return err
	}
	if err := fchown(fd, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", stage, err)
	}
	return nil
}

// chownChildren changes the owner of everything in the directory open at
// dirfd (path is for messages only), deepest first.
func chownChildren(dirfd int, path string, uid, gid int) error {
	dup, err := syscall.Dup(dirfd)
	if err != nil {
		return err
	}
	d := os.NewFile(uintptr(dup), path)
	names, err := d.Readdirnames(-1)
	d.Close()
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	for _, name := range names {
		p := filepath.Join(path, name)
		cfd, err := syscall.Openat(dirfd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		switch {
		case err == nil:
			err = chownChildren(cfd, p, uid, gid)
			if err == nil {
				err = fchown(cfd, uid, gid)
			}
			syscall.Close(cfd)
		case errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP):
			// Not a directory, or a symlink: change the entry itself.
			err = fchownat(dirfd, name, uid, gid, unix_AT_SYMLINK_NOFOLLOW)
		}
		if err != nil {
			return fmt.Errorf("chown %s: %w", p, err)
		}
	}
	return nil
}

// stagedFolderExists reports whether folder (a name in the staged copy's
// default namespace, see sourceFolder) is in the restored Maildir at
// maildir. In the Maildir++ layout INBOX is the Maildir itself and every
// other folder is a directory "." + its name, with "." between levels and
// non-ASCII names in modified UTF-7 (RFC 3501); a prefix ending in "/"
// means the live namespace separates levels with "/".
func stagedFolderExists(maildir, folder, prefix string) bool {
	if strings.EqualFold(folder, "INBOX") {
		return true
	}
	names := []string{folder}
	if strings.HasSuffix(prefix, "/") {
		names = append(names, strings.ReplaceAll(folder, "/", "."))
	}
	for _, n := range names {
		for _, enc := range []string{n, encodeMUTF7(n)} {
			if fi, err := os.Lstat(filepath.Join(maildir, "."+enc)); err == nil && fi.IsDir() {
				return true
			}
		}
	}
	return false
}

// encodeMUTF7 encodes s as IMAP modified UTF-7 (RFC 3501 5.1.3), as
// Dovecot names non-ASCII folders on disk: printable ASCII stays, "&"
// becomes "&-", anything else is UTF-16BE in modified base64 between "&"
// and "-".
func encodeMUTF7(s string) string {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+,"
	var out strings.Builder
	var run []rune
	flush := func() {
		if len(run) == 0 {
			return
		}
		var b []byte
		for _, u := range utf16.Encode(run) {
			b = append(b, byte(u>>8), byte(u))
		}
		out.WriteByte('&')
		for i := 0; i < len(b); i += 3 {
			var n uint32
			k := len(b) - i
			if k > 3 {
				k = 3
			}
			for j := 0; j < 3; j++ {
				n <<= 8
				if j < k {
					n |= uint32(b[i+j])
				}
			}
			for j := 0; j < k+1; j++ {
				out.WriteByte(b64[(n>>(18-6*j))&63])
			}
		}
		out.WriteByte('-')
		run = nil
	}
	for _, r := range s {
		switch {
		case r == '&':
			flush()
			out.WriteString("&-")
		case r >= 0x20 && r <= 0x7e:
			flush()
			out.WriteRune(r)
		default:
			run = append(run, r)
		}
	}
	flush()
	return out.String()
}

package restore

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

// File and database restores write into the subscription's webspace root
// (home), which its system user can write to. Everything below works
// relative to directory file descriptors opened without following
// symlinks, so the user cannot redirect a write elsewhere by swapping a
// path component for a symlink while the restore runs; the files are
// prepared in a root-only staging directory first.

// RestoredDirName is the folder a file or database restore creates in home.
func RestoredDirName(snap restic.Snapshot) string {
	return fmt.Sprintf("restored-%s-%s", snap.Time.UTC().Format("2006-01-02"), snap.Short())
}

// lookupUser is os/user.Lookup; a variable so that tests can stand in a
// system user without root.
var lookupUser = user.Lookup

// validRelPath checks a path given relative to home (or to the snapshot's
// base for ls): relative, clean, no .., no leading -, no control characters.
func validRelPath(p string) error {
	switch {
	case p == "":
		return nil
	case len(p) > 4096:
		return errors.New("path is too long")
	case strings.HasPrefix(p, "/"):
		return fmt.Errorf("path %q must be relative to the webspace root", p)
	case strings.HasPrefix(p, "-"):
		return fmt.Errorf("path %q must not start with -", p)
	case filepath.Clean(p) != p || p == ".":
		return fmt.Errorf("path %q is not a clean relative path", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fmt.Errorf("path %q must not contain ..", p)
		}
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return fmt.Errorf("path %q contains control characters", p)
		}
	}
	return nil
}

// webspace is a subscription's home, checked and held open.
type webspace struct {
	home     string
	fd       int
	uid, gid int
}

func (w *webspace) Close() { syscall.Close(w.fd) }

// openWebspace checks that the subscription can receive a restore: a
// system user whose uid and gid match the inventory, and a home that is
// <files root>/<name>, a real directory (not a symlink) owned by that uid.
// It returns home opened with O_NOFOLLOW.
func openWebspace(cfg *config.Config, sub inventory.Subscription) (*webspace, error) {
	if sub.SysUser == "" || sub.UID <= 0 || sub.GID <= 0 || sub.Home == "" {
		return nil, fmt.Errorf("subscription %s has no system user, uid, gid or home in the inventory, so there is nowhere to restore to", sub.Name)
	}
	u, err := lookupUser(sub.SysUser)
	if err != nil {
		return nil, fmt.Errorf("system user %s of subscription %s: %w", sub.SysUser, sub.Name, err)
	}
	if u.Uid != strconv.Itoa(sub.UID) || u.Gid != strconv.Itoa(sub.GID) {
		return nil, fmt.Errorf("system user %s has uid %s and gid %s, but the inventory gives %d and %d for subscription %s",
			sub.SysUser, u.Uid, u.Gid, sub.UID, sub.GID, sub.Name)
	}
	under := false
	for _, r := range cfg.Roots.Files {
		if sub.Home == r+"/"+sub.Name {
			under = true
		}
	}
	if !config.CleanAbs(sub.Home) || !under {
		return nil, fmt.Errorf("home %s of subscription %s is not <files root>/%s", sub.Home, sub.Name, sub.Name)
	}
	if fi, err := os.Lstat(sub.Home); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("home %s of subscription %s is a symlink; refusing to restore into it", sub.Home, sub.Name)
	}
	// O_NOFOLLOW keeps the check true if home is swapped for a symlink
	// now (Linux then reports ELOOP, or ENOTDIR with O_DIRECTORY).
	fd, err := syscall.Open(sub.Home, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	switch {
	case errors.Is(err, syscall.ELOOP):
		return nil, fmt.Errorf("home %s of subscription %s is a symlink; refusing to restore into it", sub.Home, sub.Name)
	case errors.Is(err, syscall.ENOTDIR):
		return nil, fmt.Errorf("home %s of subscription %s is not a directory (or is a symlink)", sub.Home, sub.Name)
	case err != nil:
		return nil, fmt.Errorf("home %s of subscription %s: %w", sub.Home, sub.Name, err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		syscall.Close(fd)
		return nil, err
	}
	if int(st.Uid) != sub.UID {
		syscall.Close(fd)
		return nil, fmt.Errorf("home %s is owned by uid %d, not by %s (uid %d); refusing to restore into it", sub.Home, st.Uid, sub.SysUser, sub.UID)
	}
	return &webspace{home: sub.Home, fd: fd, uid: sub.UID, gid: sub.GID}, nil
}

// exists reports whether name exists in home (a symlink counts). It is a
// courtesy check before any work; mkdir is what never reuses an entry.
func (w *webspace) exists(name string) (bool, error) {
	_, err := os.Lstat(filepath.Join(w.home, name))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// errTargetExists is the refusal to restore over an earlier restore.
func errTargetExists(path string) error {
	return fmt.Errorf("%s already exists (it may be from an earlier, possibly incomplete restore of this snapshot); delete or rename it to restore this snapshot again", path)
}

// mkdir creates name in home, root-owned with mode 0700 until finish
// hands it over, and returns it open. It never reuses an existing entry.
func (w *webspace) mkdir(name string) (int, error) {
	if err := syscall.Mkdirat(w.fd, name, 0o700); err != nil {
		if errors.Is(err, syscall.EEXIST) {
			return -1, errTargetExists(filepath.Join(w.home, name))
		}
		return -1, fmt.Errorf("creating %s: %w", filepath.Join(w.home, name), err)
	}
	fd, err := syscall.Openat(w.fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("opening %s: %w", filepath.Join(w.home, name), err)
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil || int(st.Uid) != os.Geteuid() {
		syscall.Close(fd)
		return -1, fmt.Errorf("%s changed while it was being created", filepath.Join(w.home, name))
	}
	return fd, nil
}

// finish hands the restored folder to the subscription's user, mode 0700.
func (w *webspace) finish(dirfd int) error {
	if err := syscall.Fchown(dirfd, w.uid, w.gid); err != nil {
		return err
	}
	return syscall.Fchmod(dirfd, 0o700)
}

// discard removes a restored folder this run created and could not
// finish. os.RemoveAll opens home by path (home was checked, and its
// parent belongs to root) and the rest without following symlinks.
func (w *webspace) discard(name string) error {
	return os.RemoveAll(filepath.Join(w.home, name))
}

// unix_AT_SYMLINK_NOFOLLOW is AT_SYMLINK_NOFOLLOW (the syscall package
// names it only on some architectures).
const unix_AT_SYMLINK_NOFOLLOW = 0x100

// renameat is syscall.Renameat; a variable so that tests can simulate a
// staging directory on another file system (EXDEV).
var renameat = syscall.Renameat

// moveInto moves every entry of the staging directory src into dirfd.
// When src is on another file system it copies instead (copyTree).
func moveInto(src string, dirfd int) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		err := renameat(unixAT_FDCWD, from, dirfd, e.Name())
		if errors.Is(err, syscall.EXDEV) {
			err = copyTree(from, dirfd, e.Name())
		}
		if err != nil {
			return fmt.Errorf("moving %s into place: %w", e.Name(), err)
		}
	}
	return nil
}

// unixAT_FDCWD is AT_FDCWD.
const unixAT_FDCWD = -100

// copyTree copies the staging entry src to name in dirfd, never following
// a symlink on the destination side and never replacing anything there.
// Modes and owners (already set in staging) are kept; times are not.
// Devices, fifos and sockets are skipped.
func copyTree(src string, dirfd int, name string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	st, _ := fi.Sys().(*syscall.Stat_t)
	if st == nil {
		return fmt.Errorf("%s: no owner information", src)
	}
	uid, gid := int(st.Uid), int(st.Gid)
	// Set-id bits are dropped, as the chown in staging drops them on the
	// renamed path.
	perm := uint32(fi.Mode().Perm())
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		// The syscall package has no symlinkat; /proc/self/fd/N is dirfd's
		// own directory, whatever happens to its path meanwhile.
		if err := os.Symlink(target, fmt.Sprintf("/proc/self/fd/%d/%s", dirfd, name)); err != nil {
			return err
		}
		return syscall.Fchownat(dirfd, name, uid, gid, unix_AT_SYMLINK_NOFOLLOW)
	case fi.IsDir():
		if err := syscall.Mkdirat(dirfd, name, 0o700); err != nil {
			return err
		}
		fd, err := syscall.Openat(dirfd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer syscall.Close(fd)
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), fd, e.Name()); err != nil {
				return err
			}
		}
		// Owner before mode: chown clears set-id bits.
		if err := syscall.Fchown(fd, uid, gid); err != nil {
			return err
		}
		return syscall.Fchmod(fd, perm)
	case fi.Mode().IsRegular():
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		fd, err := syscall.Openat(dirfd, name, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
		if err != nil {
			return err
		}
		out := os.NewFile(uintptr(fd), name)
		_, err = io.Copy(out, in)
		if err == nil {
			err = out.Chown(uid, gid)
		}
		if err == nil {
			err = syscall.Fchmod(fd, perm)
		}
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}
	return nil // device, fifo or socket: not restored into a webspace
}

// chownTree hands the staging tree at root to uid:gid (Lchown, so a
// symlink itself changes owner, never its target) and counts its regular
// files and their bytes.
func chownTree(root string, uid, gid int) (files int, bytes int64, err error) {
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			files++
			bytes += fi.Size()
		}
		return os.Lchown(p, uid, gid)
	})
	return files, bytes, err
}

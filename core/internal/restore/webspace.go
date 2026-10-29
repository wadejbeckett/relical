package restore

import (
	"context"
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
	"unsafe"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/restic"
)

// File and database restores write into the subscription's webspace root
// (home), which its system user can write to and rename entries in. So
// the finished restored-<date>-<id> folder is built completely in a
// root-only staging directory on home's file system (<files root>/.relical,
// see job.RestoreStagingName), handed to the user there, and moved into
// home with one renameat2(RENAME_NOREPLACE) relative to home's file
// descriptor: the folder appears whole or not at all, never replaces an
// entry and never follows a symlink. Only when that rename is not
// possible (another file system, or a kernel without renameat2) is the
// folder created in home and filled there, and then only after checking
// that what was opened is the empty, root-only directory just made.

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
	root     string // the files root home is in
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
	root := ""
	for _, r := range cfg.Roots.Files {
		if sub.Home == r+"/"+sub.Name {
			root = r
		}
	}
	if !config.CleanAbs(sub.Home) || root == "" {
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
	return &webspace{home: sub.Home, root: root, fd: fd, uid: sub.UID, gid: sub.GID}, nil
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

// targetExists is errTargetExists for name in home. A folder there that
// is still root's is what a restore killed on the fallback path (home on
// another file system) leaves: the customer cannot remove it, so the
// error says to ask the server's administrator.
func (w *webspace) targetExists(name string) error {
	path := filepath.Join(w.home, name)
	var st syscall.Stat_t
	if fstatat(w.fd, name, &st) == nil && int(st.Uid) == os.Geteuid() && int(st.Uid) != w.uid {
		return errPartialTarget(path)
	}
	return errTargetExists(path)
}

// errTargetAppeared is the refusal when the restored folder's name was
// free before the restore ran but taken by the time the finished folder
// was moved into home: the restore ran in full, so it is worded apart
// from the up-front refusals (errTargetExists, errPartialTarget), which
// panels count as refused before anything ran.
func errTargetAppeared(path string) error {
	return fmt.Errorf("restored folder %s appeared in the webspace while the restore ran; nothing was replaced; ask the server's administrator", path)
}

func errPartialTarget(path string) error {
	return fmt.Errorf("%s already exists and is still owned by root: it is left from a restore of this snapshot that was interrupted; ask the server's administrator to remove it, then restore again", path)
}

// stage creates a new root-only directory for one restore in the files
// root's restore staging directory, which is created when missing and
// refused unless it is root's with mode 0700, and locks it (lockStage).
// The caller runs done, which removes it and then releases the lock.
func (w *webspace) stage(prefix string) (dir string, done func(), err error) {
	parent := job.RestoreStagingDir(w.root)
	if err := job.EnsureRestoreStaging(parent); err != nil {
		return "", nil, err
	}
	return newStage(parent, prefix+"-")
}

// newStage makes a staging directory in parent and locks it; done removes
// it while still holding the lock, then releases it.
func newStage(parent, pattern string) (dir string, done func(), err error) {
	dir, err = os.MkdirTemp(parent, pattern)
	if err != nil {
		return "", nil, err
	}
	release, err := lockStage(dir)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	return dir, func() { os.RemoveAll(dir); release() }, nil
}

// place moves the finished folder stage/name, already the user's with
// mode 0700, into home as name. It never replaces an entry.
func (w *webspace) place(stage, name string) error {
	sfd, err := syscall.Open(stage, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(sfd)
	err = renameat2(sfd, name, w.fd, name, renameNoReplace)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.EEXIST):
		return errTargetAppeared(filepath.Join(w.home, name))
	case !errors.Is(err, syscall.EXDEV) && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOSYS):
		return fmt.Errorf("moving %s into %s: %w", name, w.home, err)
	}
	// Another file system, or no renameat2: fill a new folder in home.
	dirfd, err := w.mkdirEmpty(name)
	if err != nil {
		return err
	}
	defer syscall.Close(dirfd)
	err = moveInto(filepath.Join(stage, name), dirfd)
	if err == nil {
		err = syscall.Fchown(dirfd, w.uid, w.gid)
	}
	if err == nil {
		err = syscall.Fchmod(dirfd, 0o700)
	}
	if err != nil {
		if derr := w.discard(dirfd, name); derr != nil {
			return fmt.Errorf("%v; removing the partial %s also failed: %v", err, filepath.Join(w.home, name), derr)
		}
	}
	return err
}

// afterMkdir runs between mkdirEmpty's mkdirat and openat; tests use it to
// swap the new folder for another directory.
var afterMkdir = func(home, name string) {}

// mkdirEmpty creates name in home and returns it open. The user can rename
// entries in home, so the directory opened by name may not be the one
// just made: it is accepted only if it is empty, root's (the effective
// user's) and mode 0700 exactly, which is all the new folder can be. An
// impostor that passes holds nothing that could be altered.
func (w *webspace) mkdirEmpty(name string) (int, error) {
	path := filepath.Join(w.home, name)
	if err := syscall.Mkdirat(w.fd, name, 0o700); err != nil {
		if errors.Is(err, syscall.EEXIST) {
			return -1, errTargetAppeared(path)
		}
		return -1, fmt.Errorf("creating %s: %w", path, err)
	}
	afterMkdir(w.home, name)
	fd, err := syscall.Openat(w.fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("opening %s: %w", path, err)
	}
	changed := fmt.Errorf("%s changed while it was being created; nothing was restored into it", path)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil || int(st.Uid) != os.Geteuid() || st.Mode&0o7777 != 0o700 || st.Nlink != 2 {
		syscall.Close(fd)
		return -1, changed
	}
	f := os.NewFile(uintptr(fd), path)
	names, err := f.Readdirnames(1)
	if err != io.EOF || len(names) != 0 {
		f.Close()
		return -1, changed
	}
	// Readdirnames moved the offset; a new descriptor keeps the file's
	// state simple for the caller.
	nfd, err := syscall.Dup(fd)
	f.Close()
	if err != nil {
		return -1, err
	}
	return nfd, nil
}

// discard empties the folder this run created at dirfd through the
// descriptor, then removes name from home if it is still that folder.
func (w *webspace) discard(dirfd int, name string) error {
	self := fmt.Sprintf("/proc/self/fd/%d", dirfd)
	entries, err := os.ReadDir(self)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(self, e.Name())); err != nil {
			return err
		}
	}
	var mine, there syscall.Stat_t
	if err := syscall.Fstat(dirfd, &mine); err != nil {
		return err
	}
	if err := fstatat(w.fd, name, &there); err != nil || there.Ino != mine.Ino || there.Dev != mine.Dev {
		return nil // renamed away by the user: theirs now, and empty
	}
	return unlinkatDir(w.fd, name)
}

// oPath is O_PATH (the same value on amd64 and arm64).
const oPath = 0x200000

// fstatat stats name in dirfd without following a symlink (an O_PATH
// open, as the syscall package does not export fstatat everywhere).
func fstatat(dirfd int, name string, st *syscall.Stat_t) error {
	fd, err := syscall.Openat(dirfd, name, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer syscall.Close(fd)
	return syscall.Fstat(fd, st)
}

// unlinkatDir is unlinkat(2) with AT_REMOVEDIR: it removes an empty
// directory and nothing else.
func unlinkatDir(dirfd int, name string) error {
	p, err := syscall.BytePtrFromString(name)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall(syscall.SYS_UNLINKAT, uintptr(dirfd), uintptr(unsafe.Pointer(p)), 0x200)
	if errno != 0 {
		return errno
	}
	return nil
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

// MaxDropped caps the entries prepareTree lists as dropped; the rest are
// counted in one last entry.
const MaxDropped = 100

// prepareTree hands the staging tree at root to uid:gid before it goes
// into home: owners change with Lchown (a symlink itself, never its
// target), set-user-id and set-group-id bits are cleared from files and
// directories, and device nodes, fifos and sockets are left out (as on
// the copy path; a Plesk chroot webspace holds some under dev/ and
// var/run/). It counts the regular files and their bytes, and lists the
// entries left out relative to root (at most MaxDropped, then one
// "... and N more").
func prepareTree(root string, uid, gid int) (files int, bytes int64, dropped []string, err error) {
	more := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if t := d.Type(); t&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket) != 0 {
			rel, _ := filepath.Rel(root, p)
			if len(dropped) < MaxDropped {
				dropped = append(dropped, rel)
			} else {
				more++
			}
			return os.Remove(p)
		}
		if d.Type().IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			files++
			bytes += fi.Size()
		}
		if err := os.Lchown(p, uid, gid); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		// After the chown, which clears some of these bits but never a
		// directory's set-group-id. The staging tree is root's alone, so p
		// cannot have been swapped for a symlink.
		var st syscall.Stat_t
		if err := syscall.Lstat(p, &st); err != nil {
			return err
		}
		if st.Mode&(syscall.S_ISUID|syscall.S_ISGID) != 0 {
			return syscall.Chmod(p, st.Mode&0o1777)
		}
		return nil
	})
	if more > 0 {
		dropped = append(dropped, fmt.Sprintf("... and %d more", more))
	}
	return files, bytes, dropped, err
}

// statfs is syscall.Statfs; a variable so that tests can stand in a full
// file system.
var statfs = syscall.Statfs

// The reserve kept free on top of a restore's size and 10% margin, on
// each file system the restore writes to: SpaceReservePercent of the file
// system, at least MinSpaceReserve, so that a restore never fills a file
// system that the server's sites, mail and databases also need. Free
// inodes are checked the same way (InodeReservePercent of them).
const (
	SpaceReservePercent        = 5
	MinSpaceReserve     uint64 = 10 << 30
	InodeReservePercent        = 5
)

// ensureSpace refuses a restore of path (empty for the whole snapshot)
// when the restore staging file system, which is home's, has too little
// free space or too few free inodes (see checkSpace).
func (w *webspace) ensureSpace(ctx context.Context, r restic.Runner, snapshotID, path string) error {
	size, entries, err := r.RestoreSize(ctx, snapshotID, path)
	if err != nil {
		return fmt.Errorf("estimating the size of the restore: %w", err)
	}
	dir := job.RestoreStagingDir(w.root)
	if err := job.EnsureRestoreStaging(dir); err != nil {
		return err
	}
	return checkSpace(size, entries, dir)
}

// stat is syscall.Stat; a variable so that tests can put two directories
// on one file system or on two.
var stat = syscall.Stat

// checkSpace refuses a restore of size bytes in entries entries unless
// each file system that receives a copy has, for every copy it receives,
// the size plus 10% free, plus the reserve (the larger of
// SpaceReservePercent of the file system and MinSpaceReserve), and as
// many free inodes plus 10% plus InodeReservePercent of them (not checked
// on file systems that report no inode count, such as btrfs). dirs lists
// one directory per copy written; two on the same file system (by st_dev)
// need room for both copies there. Disk quotas are not checked.
func checkSpace(size, entries uint64, dirs ...string) error {
	type fsys struct {
		dir    string
		copies uint64
	}
	var order []uint64
	byDev := map[uint64]*fsys{}
	for _, d := range dirs {
		var st syscall.Stat_t
		if err := stat(d, &st); err != nil {
			return fmt.Errorf("free space of %s: %w", d, err)
		}
		dev := uint64(st.Dev)
		if byDev[dev] == nil {
			byDev[dev] = &fsys{dir: d}
			order = append(order, dev)
		}
		byDev[dev].copies++
	}
	for _, dev := range order {
		f := byDev[dev]
		var st syscall.Statfs_t
		if err := statfs(f.dir, &st); err != nil {
			return fmt.Errorf("free space of %s: %w", f.dir, err)
		}
		// Block counts are in units of the fragment size.
		unit := uint64(st.Frsize)
		if unit == 0 {
			unit = uint64(st.Bsize)
		}
		reserve := st.Blocks * unit * SpaceReservePercent / 100
		if reserve < MinSpaceReserve {
			reserve = MinSpaceReserve
		}
		free := st.Bavail * unit
		need := f.copies*(size+size/10) + reserve
		if free < need {
			return fmt.Errorf("not enough free space for this restore in %s (need %s, free %s)", f.dir, humanBytes(need), humanBytes(free))
		}
		if st.Files > 0 {
			needInodes := f.copies*(entries+entries/10) + st.Files*InodeReservePercent/100
			if st.Ffree < needInodes {
				return fmt.Errorf("not enough free inodes for this restore in %s (need %d, free %d)", f.dir, needInodes, st.Ffree)
			}
		}
	}
	return nil
}

// humanBytes formats n in binary units, for messages.
func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

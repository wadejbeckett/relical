package job

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// RestoreStagingName is the directory in each files root where file and
// database restores build the finished restored-<date>-<id> folder
// before moving it into the subscription's home in one rename. It is on
// the same file system as the homes (they are its siblings), owned by
// root with mode 0700, and outside every home, so no subscription's files
// backup ever includes it.
const RestoreStagingName = ".relical"

// RestoreStagingDir is the restore staging directory of a files root.
func RestoreStagingDir(filesRoot string) string {
	return filepath.Join(filesRoot, RestoreStagingName)
}

// EnsureRestoreStaging creates dir with mode 0700 when it is missing, and
// refuses it unless it is a directory (not a symlink) owned by the
// effective user (root in production) with mode 0700 exactly.
func EnsureRestoreStaging(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("restore staging %s: %w", dir, err)
	}
	return CheckRestoreStaging(dir)
}

// CheckRestoreStaging checks an existing restore staging directory (see
// EnsureRestoreStaging) without creating it.
func CheckRestoreStaging(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("restore staging %s: %w", dir, err)
	}
	st, _ := fi.Sys().(*syscall.Stat_t)
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("restore staging %s is a symlink; remove it", dir)
	case !fi.IsDir():
		return fmt.Errorf("restore staging %s is not a directory; remove it", dir)
	case st == nil || int(st.Uid) != os.Geteuid():
		return fmt.Errorf("restore staging %s is not owned by uid %d; remove it", dir, os.Geteuid())
	case fi.Mode().Perm() != 0o700 || fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0:
		return fmt.Errorf("restore staging %s has mode %v, not 0700; remove it", dir, fi.Mode())
	}
	return nil
}

// prepareRestoreStaging creates the restore staging directory in every
// files root that exists, when running as root. It does not check an
// existing one: a restore does that before using it, and a backup must
// not fail over a restore's directory.
func (e Env) prepareRestoreStaging() error {
	if os.Geteuid() != 0 {
		return nil
	}
	for _, root := range e.Cfg.Roots.Files {
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue // a server without webspaces under this root
		}
		dir := RestoreStagingDir(root)
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("restore staging %s: %w", dir, err)
		}
	}
	return nil
}

package restore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/job"
)

// StaleAfter is the age after which a restore's staging is taken to be
// left by a killed run and removed, unless a restore still holds its lock
// (see lockStage): a staging directory's mtime stops changing once restic
// has created its first entry, so age alone cannot tell a restore that
// has run for more than a day from one that was killed.
const StaleAfter = 24 * time.Hour

// CleanStale removes what killed restores left behind, older than
// StaleAfter: in <state_dir>/restore, staging directories (files-*, from
// earlier builds, and run-*, mail) and temporary .*.part files; in each
// files root's restore staging directory (job.RestoreStagingName),
// everything. It runs at the start of every restore and after the run
// lock in backup --all. A restore staging directory that fails its checks
// is left alone; the restore that needs it reports why.
func CleanStale(cfg *config.Config, now time.Time) error {
	var errs []error
	clean := func(dir string, match func(string) bool) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				errs = append(errs, err)
			}
			return
		}
		for _, e := range entries {
			if !match(e.Name()) {
				continue
			}
			fi, err := e.Info()
			if err != nil || now.Sub(fi.ModTime()) < StaleAfter {
				continue
			}
			p := filepath.Join(dir, e.Name())
			release, busy := tryLock(p)
			if busy {
				continue // a restore still running, however long it takes
			}
			if err := os.RemoveAll(p); err != nil {
				errs = append(errs, err)
			}
			release()
		}
	}
	clean(filepath.Join(cfg.StateDir, "restore"), func(n string) bool {
		return strings.HasPrefix(n, "files-") || strings.HasPrefix(n, "run-") ||
			(strings.HasPrefix(n, ".") && strings.HasSuffix(n, ".part"))
	})
	for _, root := range cfg.Roots.Files {
		dir := job.RestoreStagingDir(root)
		if job.CheckRestoreStaging(dir) != nil {
			continue
		}
		clean(dir, func(string) bool { return true })
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("removing stale restore staging: %w", err)
	}
	return nil
}

// lockStage takes an exclusive flock on the staging directory dir and
// returns its release. Every restore holds the lock on its own staging
// for as long as it runs; CleanStale leaves a locked one alone.
func lockStage(dir string) (func(), error) {
	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("locking %s: %w", dir, err)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		return nil, fmt.Errorf("locking %s: %w", dir, err)
	}
	return func() { syscall.Close(fd) }, nil
}

// tryLock takes the lock of a staging directory left behind, if nobody
// holds it. busy is true when a live restore holds it. An entry that is
// not a directory (a .part file) has no lock.
func tryLock(p string) (release func(), busy bool) {
	fd, err := syscall.Open(p, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return func() {}, false
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		syscall.Close(fd)
		return func() {}, errors.Is(err, syscall.EWOULDBLOCK)
	}
	return func() { syscall.Close(fd) }, false
}

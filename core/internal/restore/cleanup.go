package restore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/job"
)

// StaleAfter is the age after which a restore's staging is taken to be
// left by a killed run and removed.
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
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				errs = append(errs, err)
			}
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

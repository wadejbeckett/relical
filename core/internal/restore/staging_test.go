package restore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/job"
)

// swapOnMkdir makes afterMkdir replace the folder just created in home
// with decoy, a directory the effective user owns that holds a file, as a
// customer racing the restore would.
func swapOnMkdir(t *testing.T, decoy string) {
	old := afterMkdir
	afterMkdir = func(home, name string) {
		os.Rename(filepath.Join(home, name), filepath.Join(home, "junk"))
		os.Rename(decoy, filepath.Join(home, name))
	}
	t.Cleanup(func() { afterMkdir = old })
}

func TestRestoreFilesRefusesSwappedTarget(t *testing.T) {
	for _, unit := range []string{"files", "db"} {
		for _, kernelErr := range []syscall.Errno{syscall.ENOSYS, syscall.EXDEV} {
			t.Run(unit+" "+kernelErr.Error(), func(t *testing.T) {
				e, _, _ := webSetup(t)
				noRenameat2(t, kernelErr)
				h := home(e)
				decoy := filepath.Join(h, "etc")
				os.Mkdir(decoy, 0o700)
				os.WriteFile(filepath.Join(decoy, "passwd"), []byte("live\n"), 0o644)
				swapOnMkdir(t, decoy)
				var err error
				var target string
				if unit == "files" {
					target = filepath.Join(h, "restored-2026-10-20-aaaaaaaa")
					_, err = RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest", Path: "httpdocs"})
				} else {
					target = filepath.Join(h, "restored-2026-10-21-bbbbbbbb")
					_, err = RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "latest"})
				}
				if err == nil || !strings.Contains(err.Error(), "changed while it was being created") {
					t.Fatalf("err = %v", err)
				}
				entries, _ := os.ReadDir(target)
				if len(entries) != 1 || entries[0].Name() != "passwd" {
					t.Errorf("the substituted directory was altered: %v", entries)
				}
				if b, _ := os.ReadFile(filepath.Join(target, "passwd")); string(b) != "live\n" {
					t.Errorf("content altered: %q", b)
				}
				if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o700 {
					t.Errorf("mode altered: %v", fi.Mode())
				}
				noStaging(t, e)
			})
		}
	}
}

func TestRestoreMovesTheFinishedFolderInOneStep(t *testing.T) {
	for _, unit := range []string{"files", "db"} {
		t.Run(unit, func(t *testing.T) {
			e, _, _ := webSetup(t)
			h := home(e)
			calls := 0
			old := renameat2
			renameat2 = func(ofd int, oname string, nfd int, nname string, flags uint) error {
				calls++
				if flags != renameNoReplace {
					t.Errorf("renameat2 flags %d", flags)
				}
				// Before the move: nothing in home yet; the staged folder
				// complete, the user's and 0700.
				if _, err := os.Lstat(filepath.Join(h, nname)); err == nil {
					t.Errorf("%s visible in home before the move", nname)
				}
				var files int
				filepath.Walk(filepath.Join("/proc/self/fd", strconv.Itoa(ofd), oname), func(p string, fi os.FileInfo, err error) error {
					if err == nil && fi.Mode().IsRegular() {
						files++
					}
					return nil
				})
				if want := map[string]int{"files": 2, "db": 1}[unit]; files != want {
					t.Errorf("staged folder holds %d files, want %d", files, want)
				}
				return old(ofd, oname, nfd, nname, flags)
			}
			t.Cleanup(func() { renameat2 = old })
			mkdirs := 0
			oldHook := afterMkdir
			afterMkdir = func(string, string) { mkdirs++ }
			t.Cleanup(func() { afterMkdir = oldHook })
			var err error
			if unit == "files" {
				_, err = RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest", Path: "httpdocs"})
			} else {
				_, err = RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "latest"})
			}
			if err != nil || calls != 1 || mkdirs != 0 {
				t.Fatalf("err %v, renameat2 calls %d, mkdirs in home %d", err, calls, mkdirs)
			}
			noStaging(t, e)
		})
	}
}

func TestRestoreFilesTargetAppearsMidRun(t *testing.T) {
	e, env, _ := webSetup(t)
	target := filepath.Join(home(e), "restored-2026-10-20-aaaaaaaa")
	env["FAKE_RESTORE_MKDIR"] = target
	_, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest", Path: "httpdocs"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v", err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 0 {
		t.Errorf("the customer's directory was altered: %v %v", entries, err)
	}
	noStaging(t, e)
}

func TestPrepareTreeDropsSetIDAndSpecialFiles(t *testing.T) {
	root := t.TempDir()
	tree := filepath.Join(root, "t")
	os.MkdirAll(filepath.Join(tree, "d"), 0o755)
	os.WriteFile(filepath.Join(tree, "suid"), []byte("x"), 0o755)
	if err := os.Chmod(filepath.Join(tree, "suid"), 0o755|os.ModeSetuid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(tree, "d"), 0o755|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(tree, "d", "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	files, bytes, dropped, err := prepareTree(tree, os.Getuid(), os.Getgid())
	if err != nil || files != 1 || bytes != 1 {
		t.Fatalf("%d %d %v", files, bytes, err)
	}
	if len(dropped) != 1 || dropped[0] != "d/fifo" {
		t.Errorf("dropped = %q", dropped)
	}
	for _, p := range []string{"suid", "d"} {
		fi, _ := os.Lstat(filepath.Join(tree, p))
		if fi.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 || fi.Mode().Perm() != 0o755 {
			t.Errorf("%s: %v", p, fi.Mode())
		}
	}
	if _, err := os.Lstat(filepath.Join(tree, "d", "fifo")); !os.IsNotExist(err) {
		t.Errorf("fifo kept: %v", err)
	}
	// Device nodes need root to make; the e2e covers that they are dropped.
}

func TestPrepareTreeCapsTheDroppedList(t *testing.T) {
	tree := t.TempDir()
	for i := 0; i < MaxDropped+5; i++ {
		if err := syscall.Mkfifo(filepath.Join(tree, fmt.Sprintf("f%03d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, dropped, err := prepareTree(tree, os.Getuid(), os.Getgid())
	if err != nil || len(dropped) != MaxDropped+1 || dropped[MaxDropped] != "... and 5 more" {
		t.Fatalf("%d %q %v", len(dropped), dropped[len(dropped)-1], err)
	}
	if entries, _ := os.ReadDir(tree); len(entries) != 0 {
		t.Errorf("%d fifos kept", len(entries))
	}
}

func TestRestoreRefusesWithoutFreeSpace(t *testing.T) {
	e, _, log := webSetup(t)
	old := statfs
	statfs = func(path string, st *syscall.Statfs_t) error {
		st.Bsize, st.Bavail = 4096, 1000 // about 4 MB free
		return nil
	}
	t.Cleanup(func() { statfs = old })
	_, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "not enough free space for this restore in "+job.RestoreStagingDir(e.Cfg.Roots.Files[0])+" (need 256.0 MiB, free 3.9 MiB)") {
		t.Fatalf("files: %v", err)
	}
	_, err = RunDB(context.Background(), e, DBRequest{Subscription: "example.test", Database: "example_wp", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("db: %v", err)
	}
	l := log()
	if strings.Contains(l, "restore ") || strings.Contains(l, "dump ") {
		t.Errorf("restic restored or dumped:\n%s", l)
	}
	if !strings.Contains(l, "stats --mode restore-size --json "+filesSnap) || !strings.Contains(l, "ls --json --recursive "+dbSnap+" /example_wp.sql") {
		t.Errorf("size estimate:\n%s", l)
	}
}

func TestRestoreStagingIsChecked(t *testing.T) {
	for name, setup := range map[string]func(dir string){
		"symlink":   func(dir string) { os.Symlink(t.TempDir(), dir) },
		"loose":     func(dir string) { os.Mkdir(dir, 0o755) },
		"not a dir": func(dir string) { os.WriteFile(dir, nil, 0o600) },
	} {
		t.Run(name, func(t *testing.T) {
			e, _, log := webSetup(t)
			setup(job.RestoreStagingDir(e.Cfg.Roots.Files[0]))
			_, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest"})
			if err == nil || !strings.Contains(err.Error(), "restore staging") {
				t.Fatalf("err = %v", err)
			}
			if strings.Contains(log(), "restore ") {
				t.Error("restic restore ran")
			}
		})
	}
}

func TestStaleRestoreStagingIsCleaned(t *testing.T) {
	e, _, _ := webSetup(t)
	stage := job.RestoreStagingDir(e.Cfg.Roots.Files[0])
	os.Mkdir(stage, 0o700)
	restoreDir := filepath.Join(e.Cfg.StateDir, "restore")
	old := time.Now().Add(-25 * time.Hour)
	paths := map[string]bool{ // path: stale
		filepath.Join(stage, "example.test-aaaaaaaa-123"):            true,
		filepath.Join(stage, "example.test-aaaaaaaa-456"):            false,
		filepath.Join(restoreDir, "files-example.test-20261001"):     true,
		filepath.Join(restoreDir, "run-123"):                         true,
		filepath.Join(restoreDir, ".panel-config-x.tar.abcdef.part"): true,
		filepath.Join(restoreDir, "panel-config-2026-10-01-x.tar"):   true, // a restore's result: kept
	}
	for p, stale := range paths {
		os.MkdirAll(p, 0o700)
		if stale {
			os.Chtimes(p, old, old)
		}
	}
	if err := CleanStale(e.Cfg, time.Now()); err != nil {
		t.Fatal(err)
	}
	for p, stale := range paths {
		_, err := os.Lstat(p)
		gone := os.IsNotExist(err)
		if want := stale && !strings.Contains(p, "/panel-config-"); gone != want {
			t.Errorf("%s: removed %v, want %v", p, gone, want)
		}
	}
}

// TestCleanStaleSkipsALiveRestore: a staging directory older than
// StaleAfter whose lock a running restore holds is kept; one left by a
// killed restore (no lock) is removed.
func TestCleanStaleSkipsALiveRestore(t *testing.T) {
	e, _, _ := webSetup(t)
	old := time.Now().Add(-48 * time.Hour)
	var live, dead []string
	for _, parent := range []string{job.RestoreStagingDir(e.Cfg.Roots.Files[0]), filepath.Join(e.Cfg.StateDir, "restore")} {
		os.MkdirAll(parent, 0o700)
		pattern := map[bool]string{true: "run-", false: "example.test-aaaaaaaa-"}[strings.HasSuffix(parent, "restore")]
		l, done, err := newStage(parent, pattern)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(done)
		d, err := os.MkdirTemp(parent, pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{l, d} {
			os.Chtimes(p, old, old)
		}
		live, dead = append(live, l), append(dead, d)
	}
	if err := CleanStale(e.Cfg, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range live {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("a live restore's staging was removed: %s", p)
		}
	}
	for _, p := range dead {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("stale staging kept: %s", p)
		}
	}
}

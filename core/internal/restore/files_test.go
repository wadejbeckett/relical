package restore

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/locator"
	"github.com/wadejbeckett/relical/core/internal/testutil"
)

const (
	filesSnap = "aaaaaaaa11111111aaaaaaaa11111111aaaaaaaa11111111aaaaaaaa11111111"
	dbSnap    = "bbbbbbbb22222222bbbbbbbb22222222bbbbbbbb22222222bbbbbbbb22222222"
	panelSnap = "cccccccc33333333cccccccc33333333cccccccc33333333cccccccc33333333"
)

// webSetup is a subscription with a webspace owned by the test's own user
// (standing in for the subscription's system user), and a repository
// holding a files, a db and a mail snapshot.
func webSetup(t *testing.T) (job.Env, map[string]string, func() string) {
	t.Helper()
	root := t.TempDir()
	vhosts := filepath.Join(root, "vhosts")
	home := filepath.Join(vhosts, "example.test")
	os.MkdirAll(filepath.Join(home, "httpdocs"), 0o750)
	keysDir := filepath.Join(root, "keys")
	os.Mkdir(keysDir, 0o700)
	for _, n := range []string{"example.test", config.ServerName} {
		os.WriteFile(filepath.Join(keysDir, n+".key"), []byte("k\n"), 0o600)
	}
	bin, env, log := testutil.FakeRestic(t)
	env["FAKE_RESTORE_KIND"] = "files"
	os.WriteFile(env["FAKE_SNAPSHOTS_OUT"], []byte(`[`+
		`{"id":"`+filesSnap+`","time":"2026-10-20T02:00:00Z","paths":["`+home+`"],"hostname":"h","tags":["files"]},`+
		`{"id":"`+dbSnap+`","time":"2026-10-21T02:00:00Z","paths":["/example_wp.sql"],"hostname":"h","tags":["db","db:example_wp"]},`+
		`{"id":"`+panelSnap+`","time":"2026-10-22T02:00:00Z","paths":["`+root+`/state/staging/panel-config-20261022T020000Z.tar"],"hostname":"h","tags":["panel-config"]},`+
		`{"id":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","time":"2026-10-23T02:00:00Z","paths":["/m"],"hostname":"h","tags":["mail"]}]`), 0o600)
	cfg := &config.Config{
		ServerID: "h", ResticBin: bin, DoveadmBin: testutil.FakeDoveadm(t),
		StateDir: filepath.Join(root, "state"), KeysDir: keysDir,
		Roots:              config.Roots{Files: []string{vhosts}, Mail: []string{filepath.Join(root, "mail")}},
		MailboxPattern:     root + "/mail/{domain}/{mailbox}/Maildir",
		DefaultDestination: "d", Destinations: map[string]config.Destination{"d": {Repository: "/repo/{subscription}", Prune: "local"}},
	}
	uid, gid := os.Getuid(), os.Getgid()
	stubUser(t, "exampleuser", uid, gid)
	enc := true
	inv := inventory.Inventory{Version: 2, Subscriptions: []inventory.Subscription{
		{ID: "17", Name: "example.test", SysUser: "exampleuser", UID: uid, GID: gid, Home: home,
			Features: []string{"files", "db"}, Files: []string{home},
			Databases: []inventory.Database{{Type: "mysql", Name: "example_wp"}}},
	}, Server: &inventory.Server{Features: []string{"panel-config"}, ConfigExt: "tar", EncryptWithRepoKey: &enc}}
	e := job.Env{Cfg: cfg, Inv: inv, Locator: locator.Pattern(cfg.MailboxPattern), Now: time.Now, ExtraEnv: env}
	if err := e.Prepare(); err != nil {
		t.Fatal(err)
	}
	return e, env, log
}

func stubUser(t *testing.T, name string, uid, gid int) {
	t.Helper()
	old := lookupUser
	lookupUser = func(n string) (*user.User, error) {
		if n != name {
			return nil, user.UnknownUserError(n)
		}
		return &user.User{Username: n, Uid: strconv.Itoa(uid), Gid: strconv.Itoa(gid)}, nil
	}
	t.Cleanup(func() { lookupUser = old })
}

func home(e job.Env) string { return e.Inv.Subscriptions[0].Home }

func TestRestoreFilesHappyPath(t *testing.T) {
	e, _, log := webSetup(t)
	res, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest", Path: "httpdocs"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home(e), "restored-2026-10-20-aaaaaaaa")
	if res.RestoredTo != want || res.Files != 2 || res.Bytes != 11 || res.Snapshot != filesSnap {
		t.Errorf("%+v", res)
	}
	b, err := os.ReadFile(filepath.Join(want, "httpdocs", "sub", "deep.txt"))
	if err != nil || string(b) != "deep\n" {
		t.Fatalf("%q %v", b, err)
	}
	fi, _ := os.Stat(want)
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("restored folder mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Join(want, "httpdocs", "index.html")); fi.Mode().Perm() != 0o640 {
		t.Errorf("file mode not kept: %v", fi.Mode())
	}
	l := log()
	if !strings.Contains(l, "ls --json "+filesSnap+" "+home(e)+"/httpdocs") ||
		!strings.Contains(l, "--include "+home(e)+"/httpdocs") {
		t.Errorf("restic args:\n%s", l)
	}
	noStaging(t, e)
	if fi, err := os.Stat(job.RestoreStagingDir(e.Cfg.Roots.Files[0])); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("restore staging: %v %v", fi, err)
	}
}

func TestRestoreFilesWholeWebspace(t *testing.T) {
	e, _, log := webSetup(t)
	res, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "aaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	// The fake restores two files directly under the include, here home.
	if _, err := os.Stat(filepath.Join(res.RestoredTo, "index.html")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log(), "--include "+home(e)+"\n") {
		t.Errorf("%s", log())
	}
}

func TestRestoreFilesRefusesExistingTarget(t *testing.T) {
	e, _, log := webSetup(t)
	target := filepath.Join(home(e), "restored-2026-10-20-aaaaaaaa")
	for _, make := range []func() error{
		func() error { return os.Mkdir(target, 0o755) },
		func() error { return os.Symlink("/etc", target) },
	} {
		os.Remove(target)
		if err := make(); err != nil {
			t.Fatal(err)
		}
		_, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest"})
		if err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("err = %v", err)
		}
		if strings.Contains(log(), "restore ") {
			t.Fatal("ran restic restore although the target exists")
		}
	}
	// A second restore of the same snapshot is refused too.
	os.Remove(target)
	if _, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest"}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second restore: %v", err)
	}
}

func TestRestoreFilesTargetInsideWebspace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, e *job.Env)
		path  string
		want  string
	}{
		{name: "dotdot", path: "../..", want: "must not contain .."},
		{name: "dotdot inside", path: "httpdocs/../../x", want: "clean relative path"},
		{name: "dotdot clean", path: "../other.example.test", want: "must not contain .."},
		{name: "absolute", path: "/etc", want: "relative"},
		{name: "option", path: "-x", want: "must not start with -"},
		{name: "control", path: "a\nb", want: "control characters"},
		{name: "too long", path: strings.Repeat("a/", 2048) + "a", want: "too long"},
		{name: "symlinked home", want: "is a symlink", setup: func(t *testing.T, e *job.Env) {
			h := home(*e)
			os.Rename(h, h+".real")
			os.Symlink(h+".real", h)
		}},
		{name: "home not a directory", want: "not a directory", setup: func(t *testing.T, e *job.Env) {
			h := home(*e)
			os.RemoveAll(h)
			os.WriteFile(h, nil, 0o600)
		}},
		{name: "home owned by another uid", want: "owned by uid", setup: func(t *testing.T, e *job.Env) {
			s := &e.Inv.Subscriptions[0]
			s.UID++
			stubUser(t, "exampleuser", s.UID, s.GID)
		}},
		{name: "inventory uid differs from the system user", want: "but the inventory gives", setup: func(t *testing.T, e *job.Env) {
			e.Inv.Subscriptions[0].UID++
		}},
		{name: "unknown system user", want: "unknown user", setup: func(t *testing.T, e *job.Env) {
			e.Inv.Subscriptions[0].SysUser = "nobodyhere"
		}},
		{name: "home outside the files roots", want: "is not <files root>", setup: func(t *testing.T, e *job.Env) {
			e.Cfg.Roots.Files = []string{"/var/www/vhosts"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, log := webSetup(t)
			if tc.setup != nil {
				tc.setup(t, &e)
			}
			_, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest", Path: tc.path})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if l := log(); l != "" {
				t.Errorf("restic ran:\n%s", l)
			}
		})
	}
}

func TestRestoreFilesRefusesPathNotInSnapshot(t *testing.T) {
	e, env, log := webSetup(t)
	env["FAKE_LS_EMPTY"] = "1"
	_, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest", Path: "nope"})
	if err == nil || !strings.Contains(err.Error(), "not in snapshot") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(log(), "restore ") {
		t.Error("ran restic restore for a path not in the snapshot")
	}
	if _, err := os.Lstat(filepath.Join(home(e), "restored-2026-10-20-aaaaaaaa")); err == nil {
		t.Error("created the restored folder")
	}
}

func TestRestoreFilesRefusesServerAndUnknown(t *testing.T) {
	e, _, _ := webSetup(t)
	for _, s := range []string{config.ServerName, "nope.example.test"} {
		if _, err := RunFiles(context.Background(), e, FilesRequest{Subscription: s, Snapshot: "latest"}); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

// noRenameat2 makes renameat2 fail with err (EXDEV: home on another file
// system than the staging; ENOSYS: an old kernel), forcing the mkdir path.
func noRenameat2(t *testing.T, err error) {
	old := renameat2
	renameat2 = func(int, string, int, string, uint) error { return err }
	t.Cleanup(func() { renameat2 = old })
}

func TestRestoreFilesAcrossFileSystems(t *testing.T) {
	e, _, _ := webSetup(t)
	noRenameat2(t, syscall.EXDEV)
	old := renameat
	renameat = func(int, string, int, string) error { return syscall.EXDEV }
	t.Cleanup(func() { renameat = old })
	res, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest", Path: "httpdocs"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(res.RestoredTo, "httpdocs", "sub", "deep.txt"))
	if err != nil || string(b) != "deep\n" {
		t.Fatalf("%q %v", b, err)
	}
	if fi, _ := os.Stat(filepath.Join(res.RestoredTo, "httpdocs", "index.html")); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode not kept: %v", fi.Mode())
	}
	if fi, _ := os.Stat(res.RestoredTo); fi.Mode().Perm() != 0o700 {
		t.Errorf("restored folder mode %v", fi.Mode())
	}
	noStaging(t, e)
}

// noStaging fails when a restore left staging behind.
func noStaging(t *testing.T, e job.Env) {
	t.Helper()
	for _, d := range []string{filepath.Join(e.Cfg.StateDir, "restore"), job.RestoreStagingDir(e.Cfg.Roots.Files[0])} {
		if entries, _ := os.ReadDir(d); len(entries) != 0 {
			t.Errorf("staging left behind in %s: %v", d, entries)
		}
	}
}

func TestCopyTreeKeepsSymlinksAndRefusesExisting(t *testing.T) {
	src := t.TempDir()
	os.Mkdir(filepath.Join(src, "d"), 0o750)
	os.Symlink("../../etc/passwd", filepath.Join(src, "d", "link"))
	dst := t.TempDir()
	fd, err := syscall.Open(dst, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Close(fd)
	if err := copyTree(filepath.Join(src, "d"), fd, "d"); err != nil {
		t.Fatal(err)
	}
	if l, err := os.Readlink(filepath.Join(dst, "d", "link")); err != nil || l != "../../etc/passwd" {
		t.Errorf("%q %v", l, err)
	}
	if err := copyTree(filepath.Join(src, "d"), fd, "d"); !errors.Is(err, syscall.EEXIST) {
		t.Errorf("copy over an existing entry: %v", err)
	}
}

// A restored folder that is still root's (left by a restore killed on the
// fallback path) cannot be removed by the customer: the error says to ask
// the administrator. One the customer owns keeps "delete or rename it".
func TestPartialTargetNamesTheAdministrator(t *testing.T) {
	e, _, _ := webSetup(t)
	target := filepath.Join(home(e), "restored-2026-10-20-aaaaaaaa")
	os.Mkdir(target, 0o700)
	_, err := RunFiles(context.Background(), e, FilesRequest{Subscription: "example.test", Snapshot: "latest"})
	if err == nil || !strings.Contains(err.Error(), "delete or rename it") {
		t.Fatalf("the user's own folder: %v", err)
	}
	// In the test the effective user is also the subscription's user; as
	// another uid, the same folder is root's partial one.
	s := &e.Inv.Subscriptions[0]
	ws := &webspace{home: s.Home, uid: s.UID + 1}
	fd, _ := syscall.Open(s.Home, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	defer syscall.Close(fd)
	ws.fd = fd
	if err := ws.targetExists("restored-2026-10-20-aaaaaaaa"); !strings.Contains(err.Error(), "ask the server's administrator to remove it") ||
		!strings.Contains(err.Error(), "already exists") {
		t.Errorf("%v", err)
	}
}

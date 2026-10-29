package inventory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/testutil"
)

func cfgWith(subs string) *config.Config {
	return &config.Config{
		Version:            2,
		Roots:              config.Roots{Files: []string{"/var/www/vhosts"}, Mail: []string{"/var/qmail/mailnames"}},
		DefaultDestination: "d",
		Destinations:       map[string]config.Destination{"d": {Repository: "/repo/{subscription}", Prune: "local"}, "other": {Repository: "/other/{subscription}", Prune: "local"}},
		Subscriptions:      json.RawMessage(subs),
	}
}

// sub returns one valid subscription as JSON, with fields overridden.
func sub(over map[string]any) string {
	s := map[string]any{
		"id": "17", "name": "example.test", "renamed_from": "", "sys_user": "exampleuser", "uid": 10001, "gid": 1004,
		"home": "/var/www/vhosts/example.test", "features": []string{"mail", "files", "db"},
		"mail_domains": []string{"example.test", "shop.example.test"}, "files": []string{"/var/www/vhosts/example.test"},
		"file_excludes": []string{}, "databases": []map[string]string{{"type": "mysql", "name": "example_wp"}},
		"destination": "", "retention": nil,
	}
	// A subscription given another name gets its own webspace, unless the
	// test sets one.
	if n, ok := over["name"].(string); ok {
		if _, ok := over["home"]; !ok {
			s["home"] = "/var/www/vhosts/" + n
		}
		if _, ok := over["files"]; !ok {
			s["files"] = []string{"/var/www/vhosts/" + n}
		}
	}
	for k, v := range over {
		if v == nil {
			delete(s, k)
		} else {
			s[k] = v
		}
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func list(subs ...string) string { return "[" + strings.Join(subs, ",") + "]" }

func TestLoadStaticAppliesDefaults(t *testing.T) {
	inv, err := Load(context.Background(), cfgWith(list(sub(map[string]any{"databases": nil, "features": nil}))))
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Subscriptions) != 1 || inv.Version != 2 || inv.Server != nil || len(inv.Renamed) != 0 {
		t.Fatalf("%+v", inv)
	}
	s := inv.Subscriptions[0]
	if strings.Join(s.FileExcludes, " ") != "logs/** statistics/logs/** tmp/** .cagefs/**" {
		t.Errorf("excludes = %q", s.FileExcludes)
	}
	if s.Databases == nil || s.Features == nil {
		t.Errorf("nil lists would print as null: %+v", s)
	}
	b, _ := json.Marshal(inv)
	if !strings.Contains(string(b), `"renamed":[]`) || !strings.Contains(string(b), `"server":null`) {
		t.Errorf("%s", b)
	}
}

func TestInventoryRejectsBadPaths(t *testing.T) {
	cases := map[string]map[string]any{
		"files relative":       {"files": []string{"var/www/vhosts/example.test"}},
		"files dotdot":         {"files": []string{"/var/www/vhosts/../../etc"}},
		"files unclean":        {"files": []string{"/var/www/vhosts/example.test/"}},
		"files outside root":   {"files": []string{"/etc"}},
		"files root itself":    {"files": []string{"/var/www/vhosts"}},
		"files sibling prefix": {"files": []string{"/var/www/vhostsevil/x"}},
		"home outside root":    {"home": "/home/exampleuser"},
		"home dotdot":          {"home": "/var/www/vhosts/example.test/.."},
		"home empty":           {"home": ""},
		"exclude absolute":     {"file_excludes": []string{"/etc/**"}},
		"exclude dotdot":       {"file_excludes": []string{"../**"}},
		"bad name":             {"name": "../etc"},
		"upper-case name":      {"name": "Example.test"},
		"reserved name":        {"name": "_server"},
		"bad mail domain":      {"mail_domains": []string{"a/b.test"}},
		"bad sys_user":         {"sys_user": "root;rm"},
		"zero uid":             {"uid": 0},
		"negative gid":         {"gid": -1},
		"bad database":         {"databases": []map[string]string{{"type": "mysql", "name": "a b"}}},
		"long database":        {"databases": []map[string]string{{"type": "mysql", "name": strings.Repeat("a", 65)}}},
		"bad feature":          {"features": []string{"mail", "everything"}},
		"twice feature":        {"features": []string{"mail", "mail"}},
		"unknown destination":  {"destination": "nope"},
		"bad renamed_from":     {"renamed_from": "../old"},
		"self renamed_from":    {"renamed_from": "example.test"},
		"bad retention":        {"retention": map[string]int{"daily": 0}},
		"bad id":               {"id": "17/../x"},
		"unknown field":        {"homedir": "/x"},
	}
	for name, over := range cases {
		_, err := Load(context.Background(), cfgWith(list(sub(over))))
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	_, err := Load(context.Background(), cfgWith(list(sub(map[string]any{"files": []string{"/etc"}}))))
	if err == nil || !strings.Contains(err.Error(), "subscription example.test: files[0]") {
		t.Errorf("error does not name the subscription and field: %v", err)
	}
	_, err = Load(context.Background(), cfgWith(list(sub(map[string]any{"name": "_server"}))))
	if err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("_server: %v", err)
	}
	if _, err := Load(context.Background(), cfgWith(list(sub(map[string]any{"destination": "other", "databases": []map[string]string{{"type": "pgsql", "name": "x$y-1"}}})))); err != nil {
		t.Errorf("valid override and database refused: %v", err)
	}
}

func TestInventoryRejectsDuplicates(t *testing.T) {
	for name, subs := range map[string]string{
		"name":        list(sub(nil), sub(map[string]any{"id": "18"})),
		"id":          list(sub(nil), sub(map[string]any{"name": "other.example.test", "mail_domains": []string{}})),
		"mail domain": list(sub(nil), sub(map[string]any{"id": "18", "name": "other.example.test", "mail_domains": []string{"shop.example.test"}})),
		"database":    list(sub(map[string]any{"databases": []map[string]string{{"type": "mysql", "name": "a"}, {"type": "mysql", "name": "a"}}})),
	} {
		if _, err := Load(context.Background(), cfgWith(subs)); err == nil {
			t.Errorf("duplicate %s accepted", name)
		}
	}
}

func TestInventoryRenameIsReported(t *testing.T) {
	inv, err := Load(context.Background(), cfgWith(list(sub(map[string]any{"name": "new.example.test", "renamed_from": "old.example.test", "mail_domains": []string{"new.example.test"}}))))
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Renamed) != 1 || inv.Renamed[0].Subscription != "new.example.test" || inv.Renamed[0].RenamedFrom != "old.example.test" ||
		!strings.Contains(inv.Renamed[0].Note, "new repository") {
		t.Fatalf("%+v", inv.Renamed)
	}
	// The repository follows the new name; the old one is not reused.
	_, d, _ := cfgWith("[]").DestinationFor(inv.Subscriptions[0].Destination, inv.Subscriptions[0].Name)
	if d.Repository != "/repo/new.example.test" {
		t.Errorf("repository = %s", d.Repository)
	}
	// renamed_from naming a subscription that still exists is a contradiction.
	_, err = Load(context.Background(), cfgWith(list(
		sub(map[string]any{"name": "new.example.test", "renamed_from": "example.test", "mail_domains": []string{}}),
		sub(map[string]any{"id": "18"}))))
	if err == nil || !strings.Contains(err.Error(), "still a subscription") {
		t.Errorf("err = %v", err)
	}
}

const validServer = `{"features": ["panel-config"], "config_command": ["/usr/sbin/plesk", "bin", "pleskbackup", "-output-file", "{output}"], "config_ext": "tar"}`

func TestServerValidation(t *testing.T) {
	cfg := cfgWith("[]")
	cfg.Server = json.RawMessage(validServer)
	inv, err := Load(context.Background(), cfg)
	if err != nil || inv.Server == nil || inv.Server.ConfigExt != "tar" || !inv.Server.Encrypt() || inv.Server.EncryptWithRepoKey == nil {
		t.Fatalf("%+v %v", inv.Server, err)
	}
	cfg.Server = json.RawMessage(strings.Replace(validServer, `"config_ext"`, `"encrypt_with_repo_key": false, "config_ext"`, 1))
	if inv, err := Load(context.Background(), cfg); err != nil || inv.Server.Encrypt() {
		t.Fatalf("encrypt_with_repo_key false: %+v %v", inv.Server, err)
	}
	for name, s := range map[string]string{
		"relative command": strings.Replace(validServer, `"/usr/sbin/plesk"`, `"plesk"`, 1),
		"no output":        strings.Replace(validServer, `"{output}"`, `"/tmp/x"`, 1),
		"output twice":     strings.Replace(validServer, `"-output-file"`, `"{output}"`, 1),
		"empty command":    `{"features": ["panel-config"], "config_command": [], "config_ext": "tar"}`,
		"bad ext":          strings.Replace(validServer, `"tar"`, `"tar.gz"`, 1),
		"bad feature":      strings.Replace(validServer, `"panel-config"`, `"mail"`, 1),
		"unknown field":    strings.Replace(validServer, `"config_ext"`, `"cfg": 1, "config_ext"`, 1),
	} {
		cfg.Server = json.RawMessage(s)
		if _, err := Load(context.Background(), cfg); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func cmdCfg(argv ...string) *config.Config {
	c := cfgWith("")
	c.Subscriptions = nil
	c.InventoryCommand = argv
	return c
}

func TestLoadFromCommand(t *testing.T) {
	out := `{"version": 2, "subscriptions": ` + list(sub(nil)) + `, "server": ` + validServer + `}`
	inv, err := Load(context.Background(), cmdCfg(testutil.FakeInventory(t, out)))
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := inv.Find("example.test"); !ok || s.UID != 10001 || inv.Server == nil {
		t.Fatalf("%+v", inv)
	}
	if s, ok := inv.ByMailDomain("shop.example.test"); !ok || s.Name != "example.test" {
		t.Errorf("ByMailDomain: %+v %v", s, ok)
	}
}

func TestLoadFromCommandPassesArguments(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args")
	bin := testutil.FakeInventoryScript(t, `echo "$*" > '`+out+`'; echo '{"version": 2, "subscriptions": []}'`)
	if _, err := Load(context.Background(), cmdCfg(bin, "--scope", "all")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(out); strings.TrimSpace(string(b)) != "--scope all" {
		t.Errorf("args = %q", b)
	}
}

func TestLoadFromCommandFailureReportsExitAndStderr(t *testing.T) {
	_, err := Load(context.Background(), cmdCfg(testutil.FakeInventoryFailing(t)))
	if err == nil || !strings.Contains(err.Error(), "exit 1") || !strings.Contains(err.Error(), "panel database unreachable") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadFromCommandRefusesBadOutput(t *testing.T) {
	for name, out := range map[string]string{
		"not json":         `hello`,
		"no version":       `{"subscriptions": []}`,
		"version 1":        `{"version": 1, "subscriptions": []}`,
		"no subscriptions": `{"version": 2}`,
		"unknown field":    `{"version": 2, "subscriptions": [], "domains": []}`,
		"trailing data":    `{"version": 2, "subscriptions": []} {}`,
		"trailing ]":       `{"version": 2, "subscriptions": []}]`,
		"trailing }":       `{"version": 2, "subscriptions": []}}`,
		"no id":            `{"version": 2, "subscriptions": ` + list(sub(map[string]any{"id": ""})) + `}`,
		"bad subscription": `{"version": 2, "subscriptions": ` + list(sub(map[string]any{"home": "/etc"})) + `}`,
	} {
		if _, err := Load(context.Background(), cmdCfg(testutil.FakeInventory(t, out))); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestLoadFromCommandServerInBothPlacesRefused(t *testing.T) {
	cfg := cmdCfg(testutil.FakeInventory(t, `{"version": 2, "subscriptions": [], "server": `+validServer+`}`))
	cfg.Server = json.RawMessage(validServer)
	if _, err := Load(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("err = %v", err)
	}
	// The config's server applies when the command gives none.
	cfg = cmdCfg(testutil.FakeInventory(t, `{"version": 2, "subscriptions": []}`))
	cfg.Server = json.RawMessage(validServer)
	if inv, err := Load(context.Background(), cfg); err != nil || inv.Server == nil {
		t.Fatalf("%+v %v", inv, err)
	}
}

func TestLoadFromCommandOutputCap(t *testing.T) {
	old := MaxOutput
	MaxOutput = 1024
	defer func() { MaxOutput = old }()
	bin := testutil.FakeInventoryScript(t, `head -c 100000 /dev/zero`)
	if _, err := Load(context.Background(), cmdCfg(bin)); err == nil || !strings.Contains(err.Error(), "exceeds 1024 bytes") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadFromCommandTimeout(t *testing.T) {
	old := CommandTimeout
	CommandTimeout = 200 * time.Millisecond
	defer func() { CommandTimeout = old }()
	start := time.Now()
	_, err := Load(context.Background(), cmdCfg(testutil.FakeInventoryScript(t, `exec sleep 30`)))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestExamplePackagingInventoryLoads(t *testing.T) {
	b, err := os.ReadFile("../../../packaging/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, b, 0o600)
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	inv, err := Load(context.Background(), cfg)
	if err != nil || len(inv.Subscriptions) != 1 || inv.Server == nil {
		t.Fatalf("%+v %v", inv, err)
	}
}

func TestMailOnlySubscriptionNeedsNoWebspace(t *testing.T) {
	mailOnly := map[string]any{"features": []string{"mail"}, "sys_user": "", "uid": 0, "gid": 0, "home": "", "files": []string{}, "databases": []map[string]string{}}
	inv, err := Load(context.Background(), cfgWith(list(sub(mailOnly))))
	if err != nil || inv.Subscriptions[0].Home != "" {
		t.Fatalf("mail-only refused: %+v %v", inv, err)
	}
	// Values that are set are still validated.
	for k, v := range map[string]any{"sys_user": "Bad User", "uid": -1, "home": "/etc", "files": []string{"/etc"}} {
		over := map[string]any{}
		for kk, vv := range mailOnly {
			over[kk] = vv
		}
		over[k] = v
		if _, err := Load(context.Background(), cfgWith(list(sub(over)))); err == nil {
			t.Errorf("mail-only with %s=%v accepted", k, v)
		}
	}
	// With files or db the webspace fields are required.
	for _, features := range [][]string{{"mail", "files"}, {"db"}} {
		for k, v := range map[string]any{"sys_user": "", "uid": 0, "gid": 0, "home": ""} {
			over := map[string]any{"features": features, k: v}
			_, err := Load(context.Background(), cfgWith(list(sub(over))))
			if err == nil || !strings.Contains(err.Error(), k) {
				t.Errorf("features %v with empty %s: %v", features, k, err)
			}
		}
	}
	if _, err := Load(context.Background(), cfgWith(list(sub(map[string]any{"features": []string{"files"}, "files": []string{}})))); err == nil {
		t.Error("files feature without files accepted")
	}
	if _, err := Load(context.Background(), cfgWith(list(sub(map[string]any{"features": []string{"db"}, "files": []string{}})))); err != nil {
		t.Errorf("db-only without files refused: %v", err)
	}
}

func TestSkippedIsKeptAndNeverFails(t *testing.T) {
	out := `{"version": 2, "subscriptions": [], "skipped": ["a.example.test", {"name": "b.example.test", "reason": "suspended", "extra": 1}, {"name": 7}, 42]}`
	inv, err := Load(context.Background(), cmdCfg(testutil.FakeInventory(t, out)))
	if err != nil {
		t.Fatal(err)
	}
	want := []Skip{{Name: "a.example.test"}, {Name: "b.example.test", Reason: "suspended"}, {Name: "7"}, {Name: "42"}}
	if len(inv.Skipped) != len(want) {
		t.Fatalf("%+v", inv.Skipped)
	}
	for i := range want {
		if inv.Skipped[i] != want[i] {
			t.Errorf("skipped[%d] = %+v, want %+v", i, inv.Skipped[i], want[i])
		}
	}
	inv, err = Load(context.Background(), cmdCfg(testutil.FakeInventory(t, `{"version": 2, "subscriptions": [], "skipped": "oops"}`)))
	if err != nil || len(inv.Skipped) != 1 {
		t.Fatalf("%+v %v", inv.Skipped, err)
	}
	inv, err = Load(context.Background(), cmdCfg(testutil.FakeInventory(t, `{"version": 2, "subscriptions": []}`)))
	if b, _ := json.Marshal(inv); err != nil || !strings.Contains(string(b), `"skipped":[]`) {
		t.Fatalf("%s %v", b, err)
	}
}

func TestStaticInventoryMayOmitID(t *testing.T) {
	if _, err := Load(context.Background(), cfgWith(list(sub(map[string]any{"id": ""})))); err != nil {
		t.Fatal(err)
	}
	_, err := Load(context.Background(), cmdCfg(testutil.FakeInventory(t, `{"version": 2, "subscriptions": `+list(sub(map[string]any{"id": nil}))+`}`)))
	if err == nil || !strings.Contains(err.Error(), "subscription example.test: id: is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestInventoryRejectsCrossSubscriptionPaths(t *testing.T) {
	b := func(over map[string]any) string {
		o := map[string]any{"id": "18", "name": "b.example.test", "mail_domains": []string{}, "databases": []map[string]string{{"type": "mysql", "name": "b_wp"}}}
		for k, v := range over {
			o[k] = v
		}
		return sub(o)
	}
	for name, c := range map[string]struct{ subs, why string }{
		"files of another webspace": {list(sub(map[string]any{"files": []string{"/var/www/vhosts/b.example.test"}}), b(nil)), "must be the subscription's home or lie under it"},
		"files outside home":        {list(sub(map[string]any{"files": []string{"/var/www/vhosts/example.test", "/var/www/vhosts/other"}})), "files[1]"},
		"files without home":        {list(sub(map[string]any{"features": []string{"mail"}, "home": "", "files": []string{"/var/www/vhosts/example.test"}})), "files[0]"},
		"home not named after it":   {list(sub(map[string]any{"home": "/var/www/vhosts/other.example.test", "files": []string{"/var/www/vhosts/other.example.test"}})), "must be <files root>/example.test"},
		"plesk system tree":         {list(sub(map[string]any{"home": "/var/www/vhosts/system", "files": []string{"/var/www/vhosts/system"}})), "must be <files root>/example.test"},
		"mail of another's name":    {list(sub(map[string]any{"mail_domains": []string{"example.test", "b.example.test"}}), b(nil)), "is another subscription's name"},
		"same renamed_from":         {list(sub(map[string]any{"renamed_from": "old.example.test"}), b(map[string]any{"renamed_from": "old.example.test"})), "is also given by"},
	} {
		if _, err := Load(context.Background(), cfgWith(c.subs)); err == nil || !strings.Contains(err.Error(), c.why) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// With nested files roots, a home can lie inside another's.
	cfg := cfgWith(list(sub(nil), b(map[string]any{"home": "/var/www/vhosts/example.test/b.example.test", "files": []string{"/var/www/vhosts/example.test/b.example.test"}})))
	cfg.Roots.Files = append(cfg.Roots.Files, "/var/www/vhosts/example.test")
	if _, err := Load(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "are equal or nested") {
		t.Errorf("nested home: %v", err)
	}
	// Two separate webspaces with their own files, mail and subpaths are fine.
	ok := list(sub(map[string]any{"files": []string{"/var/www/vhosts/example.test/httpdocs", "/var/www/vhosts/example.test/private"}}),
		b(map[string]any{"mail_domains": []string{"b.example.test"}}))
	if _, err := Load(context.Background(), cfgWith(ok)); err != nil {
		t.Errorf("valid pair refused: %v", err)
	}
}

func TestInventoryRejectsForeignAndSystemDatabases(t *testing.T) {
	b := sub(map[string]any{"id": "18", "name": "b.example.test", "mail_domains": []string{}, "databases": []map[string]string{{"type": "mysql", "name": "b_wp"}}})
	db := func(name string) map[string]any {
		return map[string]any{"databases": []map[string]string{{"type": "mysql", "name": name}}}
	}
	_, err := Load(context.Background(), cfgWith(list(sub(db("b_wp")), b)))
	if err == nil || !strings.Contains(err.Error(), "database b_wp (mysql) is also listed by example.test") {
		t.Errorf("database of another subscription: %v", err)
	}
	for _, name := range []string{"mysql", "information_schema", "performance_schema", "sys", "psa", "PSA", "apsc", "horde",
		"roundcubemail", "atmail", "sitebuilder5", "phpmyadmin", "phpmyadmin_abc123", "phpMyAdmin"} {
		_, err := Load(context.Background(), cfgWith(list(sub(db(name)))))
		if err == nil || !strings.Contains(err.Error(), "is a server or panel database") {
			t.Errorf("system database %s: %v", name, err)
		}
	}
	// A leading - or $ would reach mysqldump's argv as (part of) an option.
	for _, name := range []string{"-rx", "-Tx", "--all", "$x"} {
		_, err := Load(context.Background(), cfgWith(list(sub(db(name)))))
		if err == nil || !strings.Contains(err.Error(), "starting with a letter, digit or _") {
			t.Errorf("database name %s: %v", name, err)
		}
	}
	// Names that only contain a reserved word are customers' own.
	for _, name := range []string{"psa_shop", "my_sys", "wp_mysql", "horde2"} {
		if _, err := Load(context.Background(), cfgWith(list(sub(db(name))))); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	// The same name with another type is another database.
	other := sub(map[string]any{"id": "18", "name": "b.example.test", "mail_domains": []string{}, "databases": []map[string]string{{"type": "postgresql", "name": "example_wp"}}})
	if _, err := Load(context.Background(), cfgWith(list(sub(nil), other))); err != nil {
		t.Errorf("same name, other type refused: %v", err)
	}
}

func TestLoadFromCommandTimeoutKillsChildren(t *testing.T) {
	old := CommandTimeout
	CommandTimeout = 300 * time.Millisecond
	defer func() { CommandTimeout = old }()
	pidFile := filepath.Join(t.TempDir(), "pid")
	// The grandchild holds no pipe, so only the process-group kill ends it.
	bin := testutil.FakeInventoryScript(t, `sleep 30 >/dev/null 2>&1 & echo $! > '`+pidFile+`'; wait`)
	if _, err := Load(context.Background(), cmdCfg(bin)); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 50; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("grandchild %d survived the timeout", pid)
}

func TestLoadFromCommandInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err := Load(ctx, cmdCfg(testutil.FakeInventoryScript(t, `exec sleep 30`)))
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("err = %v", err)
	}
}

package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// sharedRules is panels/plesk/tests/fixtures/core-rules.json: the names the
// core and the Plesk extension's CoreRules must both accept or refuse. The
// extension's CoreRulesTest reads the same file, so the two copies of the
// rules cannot drift; the core is canonical.
type sharedRules struct {
	About             string       `json:"about"`
	SubscriptionNames acceptRefuse `json:"subscription_names"`
	SysUsers          acceptRefuse `json:"sys_users"`
	Databases         acceptRefuse `json:"databases"`
	Homes             struct {
		FilesRoots []string   `json:"files_roots"`
		Accept     []homeCase `json:"accept"`
		Refuse     []homeCase `json:"refuse"`
	} `json:"homes"`
}

type acceptRefuse struct {
	Accept []string `json:"accept"`
	Refuse []string `json:"refuse"`
}

type homeCase struct {
	Name string `json:"name"`
	Home string `json:"home"`
}

func readSharedRules(t *testing.T) sharedRules {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "..", "panels", "plesk", "tests", "fixtures", "core-rules.json"))
	if err != nil {
		t.Fatalf("shared rules fixture: %v", err)
	}
	var r sharedRules
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		t.Fatalf("shared rules fixture: %v", err)
	}
	if len(r.SubscriptionNames.Accept) == 0 || len(r.SysUsers.Accept) == 0 || len(r.Databases.Accept) == 0 || len(r.Homes.Accept) == 0 || len(r.Homes.FilesRoots) == 0 {
		t.Fatal("shared rules fixture is missing a section")
	}
	return r
}

// accepted runs one subscription through Load and reports whether it was
// kept, with the reason when it was skipped.
func accepted(t *testing.T, roots []string, over map[string]any) (bool, string) {
	t.Helper()
	cfg := cfgWith(list(sub(over)))
	if roots != nil {
		cfg.Roots.Files = roots
	}
	inv, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(inv.Subscriptions) == 1 {
		return true, ""
	}
	if len(inv.Skipped) == 1 {
		return false, inv.Skipped[0].Reason
	}
	t.Fatalf("Load kept %d and skipped %d subscriptions, want one of either", len(inv.Subscriptions), len(inv.Skipped))
	return false, ""
}

func TestSharedRulesFixture(t *testing.T) {
	r := readSharedRules(t)
	check := func(t *testing.T, what, value string, want bool, over map[string]any, roots []string) {
		t.Helper()
		got, why := accepted(t, roots, over)
		if got != want {
			if want {
				t.Errorf("%s %q: core refuses it (%s), fixture says accept", what, value, why)
			} else {
				t.Errorf("%s %q: core accepts it, fixture says refuse", what, value)
			}
		}
	}

	t.Run("subscription_names", func(t *testing.T) {
		// A mail-only subscription: no webspace, so only the name is tested.
		over := func(n string) map[string]any {
			return map[string]any{"name": n, "features": []string{"mail"}, "sys_user": "", "uid": 0, "gid": 0,
				"home": nil, "files": nil, "mail_domains": []string{}, "databases": nil}
		}
		for _, n := range r.SubscriptionNames.Accept {
			check(t, "subscription name", n, true, over(n), nil)
		}
		for _, n := range r.SubscriptionNames.Refuse {
			check(t, "subscription name", n, false, over(n), nil)
		}
	})

	t.Run("sys_users", func(t *testing.T) {
		for _, u := range r.SysUsers.Accept {
			check(t, "sys_user", u, true, map[string]any{"sys_user": u}, nil)
		}
		for _, u := range r.SysUsers.Refuse {
			check(t, "sys_user", u, false, map[string]any{"sys_user": u}, nil)
		}
	})

	t.Run("databases", func(t *testing.T) {
		over := func(d string) map[string]any {
			return map[string]any{"databases": []map[string]string{{"type": "mysql", "name": d}}}
		}
		for _, d := range r.Databases.Accept {
			check(t, "database", d, true, over(d), nil)
			if !ValidDatabaseName(d) {
				t.Errorf("database %q: ValidDatabaseName refuses it, fixture says accept", d)
			}
		}
		for _, d := range r.Databases.Refuse {
			check(t, "database", d, false, over(d), nil)
			if ValidDatabaseName(d) {
				t.Errorf("database %q: ValidDatabaseName accepts it, fixture says refuse", d)
			}
		}
	})

	t.Run("homes", func(t *testing.T) {
		// db needs a webspace but no files list, so only home is tested.
		over := func(c homeCase) map[string]any {
			return map[string]any{"name": c.Name, "home": c.Home, "features": []string{"db"}, "files": nil,
				"mail_domains": []string{}}
		}
		for _, c := range r.Homes.Accept {
			check(t, "home of "+c.Name, c.Home, true, over(c), r.Homes.FilesRoots)
		}
		for _, c := range r.Homes.Refuse {
			check(t, "home of "+c.Name, c.Home, false, over(c), r.Homes.FilesRoots)
		}
	})
}

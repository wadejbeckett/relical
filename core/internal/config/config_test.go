package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validJSON = `{
  "server_id": "host.example.test",
  "state_dir": "/var/lib/relical",
  "keys_dir": "/etc/relical/keys",
  "mailbox_pattern": "/var/qmail/mailnames/{domain}/{mailbox}/Maildir",
  "domains": ["example.test", "other.example.test"],
  "default_destination": "cloud",
  "domain_destinations": {"other.example.test": "sftp"},
  "destinations": {
    "cloud": {"repository": "rest:https://cloud.example.test:8000/host/{domain}/", "env": {"RESTIC_REST_USERNAME": "host"}, "prune": "remote"},
    "sftp":  {"repository": "sftp:backup@box.example.test:/relical/{domain}", "prune": "local"}
  }
}`

func write(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAppliesDefaults(t *testing.T) {
	c, err := Load(write(t, validJSON, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	if c.ResticBin != "restic" || c.DoveadmBin != "doveadm" {
		t.Errorf("binaries = %q, %q", c.ResticBin, c.DoveadmBin)
	}
	if c.Retention != (Retention{Daily: 14, Weekly: 8, Monthly: 12}) {
		t.Errorf("retention = %+v", c.Retention)
	}
}

func TestLoadRefusesReadableByOthers(t *testing.T) {
	_, err := Load(write(t, validJSON, 0o640))
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRefusesUnknownField(t *testing.T) {
	body := strings.Replace(validJSON, `"server_id"`, `"servr_id": "x", "server_id"`, 1)
	if _, err := Load(write(t, body, 0o600)); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestLoadRejectsBadDomain(t *testing.T) {
	for _, d := range []string{"../etc", "Example.test", "example", "a/b.test", ""} {
		body := strings.Replace(validJSON, `"example.test", "other`, `"`+d+`", "other`, 1)
		if _, err := Load(write(t, body, 0o600)); err == nil {
			t.Errorf("domain %q accepted", d)
		}
	}
}

func TestLoadRejectsRepositoryWithoutDomain(t *testing.T) {
	body := strings.Replace(validJSON, `/relical/{domain}`, `/relical/all`, 1)
	if _, err := Load(write(t, body, 0o600)); err == nil {
		t.Fatal("repository without {domain} accepted")
	}
}

func TestLoadRejectsBadPrune(t *testing.T) {
	body := strings.Replace(validJSON, `"prune": "local"`, `"prune": "sometimes"`, 1)
	if _, err := Load(write(t, body, 0o600)); err == nil {
		t.Fatal("bad prune accepted")
	}
}

func TestDestinationFor(t *testing.T) {
	c, err := Load(write(t, validJSON, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	name, d, err := c.DestinationFor("example.test")
	if err != nil || name != "cloud" || d.Repository != "rest:https://cloud.example.test:8000/host/example.test/" || d.Prune != "remote" {
		t.Errorf("default: %s %+v %v", name, d, err)
	}
	name, d, _ = c.DestinationFor("other.example.test")
	if name != "sftp" || d.Repository != "sftp:backup@box.example.test:/relical/other.example.test" {
		t.Errorf("override: %s %+v", name, d)
	}
	if _, _, err := c.DestinationFor("../x"); err == nil {
		t.Error("invalid domain accepted")
	}
	if !c.Enabled("example.test") || c.Enabled("nope.example.test") {
		t.Error("Enabled wrong")
	}
}

func TestExamplePackagingConfigLoads(t *testing.T) {
	b, err := os.ReadFile("../../../packaging/config.example.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(write(t, string(b), 0o600))
	if err != nil {
		t.Fatalf("packaging/config.example.json does not load: %v", err)
	}
	if c.DefaultDestination != "archive" || len(c.Domains) != 1 {
		t.Errorf("%+v", c)
	}
}

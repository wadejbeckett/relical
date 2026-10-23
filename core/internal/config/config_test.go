package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validJSON = `{
  "version": 2,
  "server_id": "host.example.test",
  "state_dir": "/var/lib/relical",
  "keys_dir": "/etc/relical/keys",
  "inventory_command": ["/usr/local/sbin/inventory", "--json"],
  "default_destination": "cloud",
  "destinations": {
    "cloud": {"repository": "rest:https://cloud.example.test:8000/host/{subscription}/", "env": {"RESTIC_REST_USERNAME": "host"}, "prune": "remote"},
    "sftp":  {"repository": "sftp:backup@box.example.test:/relical/{subscription}", "prune": "local"}
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
	if c.ResticBin != "restic" || c.DoveadmBin != "doveadm" || c.MysqldumpBin != "mysqldump" {
		t.Errorf("binaries = %q, %q, %q", c.ResticBin, c.DoveadmBin, c.MysqldumpBin)
	}
	if c.Retention != (Retention{Daily: 14, Weekly: 8, Monthly: 12}) {
		t.Errorf("retention = %+v", c.Retention)
	}
	if strings.Join(c.Roots.Files, ",") != "/var/www/vhosts" || strings.Join(c.Roots.Mail, ",") != "/var/qmail/mailnames" {
		t.Errorf("roots = %+v", c.Roots)
	}
	if c.MailboxPattern != "/var/qmail/mailnames/{domain}/{mailbox}/Maildir" {
		t.Errorf("mailbox_pattern = %q", c.MailboxPattern)
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

func TestLoadRefusesVersionOneFields(t *testing.T) {
	for _, f := range []string{`"domains": ["example.test"],`, `"domain_destinations": {},`} {
		body := strings.Replace(validJSON, `"server_id"`, f+` "server_id"`, 1)
		if _, err := Load(write(t, body, 0o600)); err == nil {
			t.Errorf("%s accepted", f)
		}
	}
}

func TestLoadRequiresVersion2(t *testing.T) {
	body := strings.Replace(validJSON, `"version": 2,`, ``, 1)
	if _, err := Load(write(t, body, 0o600)); err == nil || !strings.Contains(err.Error(), `"version": 2 is required`) {
		t.Errorf("missing version: %v", err)
	}
	body = strings.Replace(validJSON, `"version": 2,`, `"version": 3,`, 1)
	if _, err := Load(write(t, body, 0o600)); err == nil || !strings.Contains(err.Error(), "unsupported config version 3") {
		t.Errorf("version 3: %v", err)
	}
}

func TestConfigRefusesDomainPlaceholder(t *testing.T) {
	body := strings.Replace(validJSON, `/relical/{subscription}`, `/relical/{domain}`, 1)
	_, err := Load(write(t, body, 0o600))
	if err == nil || !strings.Contains(err.Error(), "replace {domain} with {subscription}") || !strings.Contains(err.Error(), "destination sftp") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadRejectsRepositoryWithoutSubscription(t *testing.T) {
	for _, repo := range []string{`/relical/all`, `/relical/{subscription}/{subscription}`} {
		body := strings.Replace(validJSON, `/relical/{subscription}`, repo, 1)
		if _, err := Load(write(t, body, 0o600)); err == nil || !strings.Contains(err.Error(), "{subscription} once") {
			t.Errorf("repository %s: %v", repo, err)
		}
	}
}

func TestConfigRefusesStaticAndCommand(t *testing.T) {
	both := strings.Replace(validJSON, `"default_destination"`, `"subscriptions": [], "default_destination"`, 1)
	if _, err := Load(write(t, both, 0o600)); err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("both: %v", err)
	}
	neither := strings.Replace(validJSON, `"inventory_command": ["/usr/local/sbin/inventory", "--json"],`, ``, 1)
	if _, err := Load(write(t, neither, 0o600)); err == nil || !strings.Contains(err.Error(), "give either") {
		t.Errorf("neither: %v", err)
	}
	null := strings.Replace(neither, `"default_destination"`, `"subscriptions": null, "default_destination"`, 1)
	if _, err := Load(write(t, null, 0o600)); err == nil {
		t.Error("subscriptions: null counted as a static inventory")
	}
	static := strings.Replace(neither, `"default_destination"`, `"subscriptions": [], "default_destination"`, 1)
	if _, err := Load(write(t, static, 0o600)); err != nil {
		t.Errorf("static only: %v", err)
	}
	relative := strings.Replace(validJSON, `"/usr/local/sbin/inventory"`, `"inventory"`, 1)
	if _, err := Load(write(t, relative, 0o600)); err == nil {
		t.Error("relative inventory_command accepted")
	}
}

func TestLoadRejectsBadRootsAndPattern(t *testing.T) {
	for _, extra := range []string{
		`"roots": {"files": ["/"]},`,
		`"roots": {"mail": ["var/qmail"]},`,
		`"roots": {"files": ["/var/www/../etc"]},`,
		`"mailbox_pattern": "/srv/mail/{domain}/{mailbox}/Maildir",`,
		`"mailbox_pattern": "/var/qmail/mailnames/../x/{domain}/{mailbox}",`,
		`"mailbox_pattern": "/var/qmail/mailnames/{domain}/Maildir",`,
		`"mysql": {"defaults_file": "mysql.cnf"},`,
	} {
		body := strings.Replace(validJSON, `"server_id"`, extra+` "server_id"`, 1)
		if _, err := Load(write(t, body, 0o600)); err == nil {
			t.Errorf("%s accepted", extra)
		}
	}
	ok := strings.Replace(validJSON, `"server_id"`, `"roots": {"mail": ["/srv/mail"]}, "mailbox_pattern": "/srv/mail/{domain}/{mailbox}/Maildir", "server_id"`, 1)
	if _, err := Load(write(t, ok, 0o600)); err != nil {
		t.Errorf("pattern under a custom mail root: %v", err)
	}
}

func TestSftpArgs(t *testing.T) {
	ok := strings.Replace(validJSON, `"prune": "local"}`, `"prune": "local", "sftp_args": "-p 23 -i /etc/relical/ssh/id_ed25519"}`, 1)
	c, err := Load(write(t, ok, 0o600))
	if err != nil || c.Destinations["sftp"].SftpArgs != "-p 23 -i /etc/relical/ssh/id_ed25519" {
		t.Fatalf("%v", err)
	}
	notSftp := strings.Replace(validJSON, `"prune": "remote"}`, `"prune": "remote", "sftp_args": "-p 23"}`, 1)
	if _, err := Load(write(t, notSftp, 0o600)); err == nil || !strings.Contains(err.Error(), "only for sftp:") {
		t.Errorf("sftp_args on a rest: repository: %v", err)
	}
	quote := strings.Replace(validJSON, `"prune": "local"}`, `"prune": "local", "sftp_args": "-o ProxyCommand=\"x y\""}`, 1)
	if _, err := Load(write(t, quote, 0o600)); err == nil || !strings.Contains(err.Error(), "must not contain \"") {
		t.Errorf("quote: %v", err)
	}
	comma := strings.Replace(validJSON, `"prune": "local"}`, `"prune": "local", "sftp_args": "-p 23 -o Ciphers=aes128-ctr,aes256-ctr"}`, 1)
	if _, err := Load(write(t, comma, 0o600)); err != nil {
		t.Errorf("comma refused (the runner quotes it): %v", err)
	}
	for args, bad := range map[string]bool{"-p 23": true, "-p23": true, "-o Port=23": true, "-oPort=23": true, "-i /k -o StrictHostKeyChecking=yes": false} {
		body := strings.Replace(validJSON, `"sftp:backup@box.example.test:/relical/{subscription}", "prune": "local"}`,
			`"sftp://backup@box.example.test:2222//relical/{subscription}", "prune": "local", "sftp_args": "`+args+`"}`, 1)
		_, err := Load(write(t, body, 0o600))
		if bad != (err != nil) || (bad && !strings.Contains(err.Error(), "has a port")) {
			t.Errorf("URL with port, sftp_args %q: %v", args, err)
		}
	}
	// Without a port in the URL, -p is how the port is given.
	noPort := strings.Replace(validJSON, `"sftp:backup@box.example.test:/relical/{subscription}", "prune": "local"}`,
		`"sftp://backup@box.example.test//relical/{subscription}", "prune": "local", "sftp_args": "-p 23"}`, 1)
	if _, err := Load(write(t, noPort, 0o600)); err != nil {
		t.Errorf("sftp:// without port: %v", err)
	}
	newline := strings.Replace(validJSON, `"prune": "local"}`, `"prune": "local", "sftp_args": "-p 23\n-o ProxyCommand=x"}`, 1)
	if _, err := Load(write(t, newline, 0o600)); err == nil || !strings.Contains(err.Error(), "one line") {
		t.Errorf("newline: %v", err)
	}
}

func TestSubscriptionIsAWholeComponent(t *testing.T) {
	for repo, ok := range map[string]bool{
		`/relical/b.{subscription}`: false,
		`/relical/{subscription}.d`: false,
		`/relical/{subscription}/`:  true,
		`/relical/{subscription}/x`: true,
		`/relical/x/{subscription}`: true,
		`relical-{subscription}`:    false,
	} {
		body := strings.Replace(validJSON, `/relical/{subscription}`, repo, 1)
		_, err := Load(write(t, body, 0o600))
		if ok != (err == nil) {
			t.Errorf("repository %s: %v", repo, err)
		}
	}
	body := strings.Replace(validJSON, `sftp:backup@box.example.test:/relical/{subscription}`, `sftp:backup@box.example.test:{subscription}`, 1)
	if _, err := Load(write(t, body, 0o600)); err != nil {
		t.Errorf("after a colon: %v", err)
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
	name, d, err := c.DestinationFor("", "example.test")
	if err != nil || name != "cloud" || d.Repository != "rest:https://cloud.example.test:8000/host/example.test/" || d.Prune != "remote" {
		t.Errorf("default: %s %+v %v", name, d, err)
	}
	name, d, _ = c.DestinationFor("sftp", "other.example.test")
	if name != "sftp" || d.Repository != "sftp:backup@box.example.test:/relical/other.example.test" {
		t.Errorf("override: %s %+v", name, d)
	}
	if _, d, err := c.DestinationFor("", ServerName); err != nil || d.Repository != "rest:https://cloud.example.test:8000/host/_server/" {
		t.Errorf("server: %+v %v", d, err)
	}
	if _, _, err := c.DestinationFor("", "../x"); err == nil {
		t.Error("invalid name accepted")
	}
	if _, _, err := c.DestinationFor("nope", "example.test"); err == nil {
		t.Error("undefined destination accepted")
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
	if c.DefaultDestination != "archive" || !c.HasStaticInventory() || !c.HasServer() {
		t.Errorf("%+v", c)
	}
}

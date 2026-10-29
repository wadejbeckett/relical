package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupInitsNewRepoAndExcludesTmp(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_CAT_EXIT"] = "10"
	r := e.BackupMail(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || r.Snapshot == "" || r.Mailboxes != 2 {
		t.Fatalf("%+v", r)
	}
	l := log()
	if !strings.Contains(l, "init --repository-version 2") {
		t.Errorf("no init:\n%s", l)
	}
	alice := filepath.Join(filepath.Dir(e.Cfg.StateDir), "mail/example.test/alice/Maildir")
	for _, w := range []string{"--tag mail", "--exclude " + alice + "/tmp/*", "--exclude " + alice + "/.*/tmp/*", "--exclude **/dovecot.index*"} {
		if !strings.Contains(l, w) {
			t.Errorf("missing %q in\n%s", w, l)
		}
	}
	if _, err := os.Stat(filepath.Join(e.Cfg.KeysDir, "example.test.key")); err != nil {
		t.Error("key not created")
	}
}

func TestBackupWrongPasswordDoesNotInit(t *testing.T) {
	e, env, log := setup(t, "local")
	env["FAKE_CAT_EXIT"] = "12"
	r := e.BackupMail(context.Background(), mustSub(t, e, "example.test"))
	if r.OK || !strings.Contains(r.Error, "exit 12") {
		t.Fatalf("%+v", r)
	}
	if strings.Contains(log(), "init") || strings.Contains(log(), "backup --json") {
		t.Fatalf("ran init or backup after a wrong password:\n%s", log())
	}
}

func TestBackupPartialIsOKWithNote(t *testing.T) {
	e, env, _ := setup(t, "local")
	env["FAKE_BACKUP_EXIT"] = "3"
	r := e.BackupMail(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || r.Snapshot == "" || r.Note == "" || !strings.Contains(r.Note, "exit 3") {
		t.Fatalf("%+v", r)
	}
}

func TestBackupPartialWithoutSummaryFails(t *testing.T) {
	e, env, _ := setup(t, "local")
	env["FAKE_BACKUP_EXIT"] = "3"
	if err := os.WriteFile(env["FAKE_BACKUP_OUT"], []byte(`{"message_type":"status"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.BackupMail(context.Background(), mustSub(t, e, "example.test"))
	if r.OK || r.Snapshot != "" || r.Error == "" {
		t.Fatalf("%+v", r)
	}
}

func TestNoFeaturesNeverCallsRestic(t *testing.T) {
	e, _, log := setup(t, "local")
	sub := mustSub(t, e, "example.test")
	sub.Features = []string{}
	rs := []Result{e.BackupMail(context.Background(), sub), e.BackupFiles(context.Background(), sub), e.Forget(context.Background(), sub), e.Check(context.Background(), sub, "")}
	rs = append(rs, e.BackupDatabases(context.Background(), sub)...)
	rs = append(rs, e.BackupSubscription(context.Background(), sub, "")...)
	for _, r := range rs {
		if !r.OK || r.Note == "" {
			t.Errorf("%+v", r)
		}
	}
	if log() != "" {
		t.Fatalf("restic ran:\n%s", log())
	}
}

func TestBackupCoversEveryMailDomain(t *testing.T) {
	e, env, log := setup(t, "local")
	shop := filepath.Join(filepath.Dir(e.Cfg.StateDir), "mail", "shop.example.test", "carol", "Maildir")
	os.MkdirAll(filepath.Join(shop, "cur"), 0o700)
	r := e.BackupMail(context.Background(), mustSub(t, e, "example.test"))
	if !r.OK || r.Mailboxes != 3 || r.Subscription != "example.test" || r.Unit != "mail" {
		t.Fatalf("%+v", r)
	}
	if !strings.Contains(log(), shop) || !strings.Contains(repoEnv(env), "RESTIC_REPOSITORY=/repo/example.test") {
		t.Errorf("%s\n%s", log(), repoEnv(env))
	}
}

func TestBackupMissingMailDomainDirIsSkipped(t *testing.T) {
	e, _, _ := setup(t, "local")
	// shop.example.test has no mail directory in setup.
	if r := e.BackupMail(context.Background(), mustSub(t, e, "example.test")); !r.OK || r.Mailboxes != 2 {
		t.Fatalf("%+v", r)
	}
}

// A subscription with mail on but no mailbox (a website-only customer on
// a panel that switches mail on by default) is OK with a note, every
// night, and restic never runs for it.
func TestBackupMailWithoutMailboxesIsANote(t *testing.T) {
	e, _, log := setup(t, "local")
	sub := mustSub(t, e, "example.test")
	sub.MailDomains = []string{"shop.example.test"} // no mail directory at all
	os.MkdirAll(filepath.Join(filepath.Dir(e.Cfg.StateDir), "mail", "empty.example.test"), 0o700)
	for _, domains := range [][]string{{"shop.example.test"}, {"empty.example.test"}, {}} {
		sub.MailDomains = domains
		r := e.BackupMail(context.Background(), sub)
		if !r.OK || r.Note != NoMailboxesNote || r.Error != "" || r.Snapshot != "" {
			t.Fatalf("%v: %+v", domains, r)
		}
	}
	if log() != "" {
		t.Errorf("restic ran:\n%s", log())
	}
}

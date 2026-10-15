package locator

import (
	"os"
	"path/filepath"
	"testing"
)

func tree(t *testing.T) (root string, p Pattern) {
	t.Helper()
	root = t.TempDir()
	for _, box := range []string{"alice", "bob.smith", "carol"} {
		if err := os.MkdirAll(filepath.Join(root, "example.test", box, "Maildir", "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// carol has no Maildir subdirectory: not a mailbox.
	os.RemoveAll(filepath.Join(root, "example.test", "carol", "Maildir"))
	// Plesk keeps non-mailbox entries beside mailboxes.
	os.MkdirAll(filepath.Join(root, "example.test", "@attachments"), 0o700)
	os.MkdirAll(filepath.Join(root, "example.test", ".spamassassin"), 0o700)
	return root, Pattern(root + "/{domain}/{mailbox}/Maildir")
}

func TestMailboxes(t *testing.T) {
	root, p := tree(t)
	got, err := p.Mailboxes("example.test")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "alice" || got[1].Name != "bob.smith" {
		t.Fatalf("got %+v", got)
	}
	if got[0].Login != "alice@example.test" || got[0].Path != filepath.Join(root, "example.test/alice/Maildir") {
		t.Errorf("alice = %+v", got[0])
	}
	if got[0].UID != os.Getuid() || got[0].GID != os.Getgid() {
		t.Errorf("owner = %d:%d", got[0].UID, got[0].GID)
	}
}

func TestMailboxesSkipsSymlinks(t *testing.T) {
	root, p := tree(t)
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(outside, "Maildir"), 0o700)
	os.Symlink(outside, filepath.Join(root, "example.test", "mallory"))
	// A real mailbox dir whose Maildir is a symlink is skipped too.
	os.MkdirAll(filepath.Join(root, "example.test", "trent"), 0o700)
	os.Symlink(filepath.Join(outside, "Maildir"), filepath.Join(root, "example.test", "trent", "Maildir"))
	got, _ := p.Mailboxes("example.test")
	for _, m := range got {
		if m.Name == "mallory" || m.Name == "trent" {
			t.Fatalf("followed a symlink: %+v", m)
		}
	}
}

func TestMailboxesRejectsBadDomain(t *testing.T) {
	_, p := tree(t)
	if _, err := p.Mailboxes("../example.test"); err == nil {
		t.Fatal("bad domain accepted")
	}
}

func TestMailboxesMissingDomainIsError(t *testing.T) {
	_, p := tree(t)
	if _, err := p.Mailboxes("missing.test"); err == nil {
		t.Fatal("missing domain dir gave no error")
	}
}

func TestFind(t *testing.T) {
	_, p := tree(t)
	if m, err := p.Find("example.test", "bob.smith"); err != nil || m.Name != "bob.smith" {
		t.Fatalf("%+v %v", m, err)
	}
	for _, bad := range []string{"nobody", "..", "a/b", ""} {
		if _, err := p.Find("example.test", bad); err == nil {
			t.Errorf("Find(%q) succeeded", bad)
		}
	}
}

func TestPatternMustUseWholeComponent(t *testing.T) {
	root, _ := tree(t)
	p := Pattern(root + "/{domain}/x{mailbox}/Maildir")
	if _, err := p.Mailboxes("example.test"); err == nil {
		t.Fatal("partial-component pattern accepted")
	}
}

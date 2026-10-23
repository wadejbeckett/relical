package job

import (
	"context"
	"strings"
	"testing"
)

func filesSub(t *testing.T, e Env) Env {
	t.Helper()
	e.Inv.Subscriptions[0].Features = []string{"files"}
	return e
}

func TestBackupFilesArgs(t *testing.T) {
	e, _, log := setup(t, "local")
	e = filesSub(t, e)
	sub := mustSub(t, e, "example.test")
	sub.Files = []string{"/var/www/vhosts/example.test", "/var/www/vhosts/odd[1]*.example.test"}
	sub.FileExcludes = []string{"logs/**", "tmp/**"}
	r := e.BackupFiles(context.Background(), sub)
	if !r.OK || r.Unit != "files" || r.Snapshot == "" || r.FilesNew != 3 {
		t.Fatalf("%+v", r)
	}
	l := logLines(log())
	got := l[len(l)-1]
	want := "backup --json --one-file-system --exclude-caches --tag files" +
		" --exclude /var/www/vhosts/example.test/logs/** --exclude /var/www/vhosts/example.test/tmp/**" +
		` --exclude /var/www/vhosts/odd\[1]\*.example.test/logs/** --exclude /var/www/vhosts/odd\[1]\*.example.test/tmp/**` +
		" /var/www/vhosts/example.test /var/www/vhosts/odd[1]*.example.test"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestBackupFilesPartialIsOKWithNote(t *testing.T) {
	e, env, _ := setup(t, "local")
	env["FAKE_BACKUP_EXIT"] = "3"
	r := e.BackupFiles(context.Background(), mustSub(t, filesSub(t, e), "example.test"))
	if !r.OK || r.Snapshot == "" || !strings.Contains(r.Note, "exit 3") {
		t.Fatalf("%+v", r)
	}
}

func TestBackupFilesFailure(t *testing.T) {
	e, env, _ := setup(t, "local")
	env["FAKE_BACKUP_EXIT"] = "1"
	r := e.BackupFiles(context.Background(), mustSub(t, filesSub(t, e), "example.test"))
	if r.OK || !strings.Contains(r.Error, "exit 1") {
		t.Fatalf("%+v", r)
	}
}

func TestBackupFilesWithoutPathsFailsCleanly(t *testing.T) {
	e, _, log := setup(t, "local")
	sub := mustSub(t, filesSub(t, e), "example.test")
	sub.Files = nil
	r := e.BackupFiles(context.Background(), sub)
	if r.OK || !strings.Contains(r.Error, "no files paths") || log() != "" {
		t.Fatalf("%+v\n%s", r, log())
	}
}

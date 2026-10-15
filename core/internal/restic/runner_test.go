package restic

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/testutil"
)

func fake(t *testing.T) (Runner, map[string]string, func() string) {
	bin, env, log := testutil.FakeRestic(t)
	return Runner{Bin: bin, Repository: "/repo/example.test", PasswordFile: "/keys/example.test.key",
		Host: "host.example.test", CacheDir: "/cache", Env: env}, env, log
}

func TestBackupParsesSummaryAndBuildsArgs(t *testing.T) {
	t.Setenv("RESTIC_PASSWORD", "must-not-leak")
	r, env, log := fake(t)
	sum, err := r.Backup(context.Background(), []string{"/m/a/Maildir", "/m/b/Maildir"}, []string{"backup"}, []string{"**/dovecot.index*"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SnapshotID[:8] != "01234567" || sum.FilesNew != 3 || sum.DataAddedPacked != 1024 || sum.TotalDuration != 1.5 {
		t.Errorf("summary = %+v", sum)
	}
	got := log()
	want := "--retry-lock 30m backup --json --one-file-system --tag backup --exclude **/dovecot.index* /m/a/Maildir /m/b/Maildir"
	if !strings.Contains(got, want) {
		t.Errorf("args:\n%s\nwant %s", got, want)
	}
	if strings.Contains(got, "LEAKED") {
		t.Error("parent RESTIC_PASSWORD leaked into restic's environment")
	}
	envLog, _ := os.ReadFile(env["FAKE_LOG"] + ".env")
	for _, w := range []string{"RESTIC_REPOSITORY=/repo/example.test", "RESTIC_PASSWORD_FILE=/keys/example.test.key", "RESTIC_HOST=host.example.test", "RESTIC_CACHE_DIR=/cache"} {
		if !strings.Contains(string(envLog), w) {
			t.Errorf("env missing %s:\n%s", w, envLog)
		}
	}
}

func TestBackupPartialReturnsSummaryAndExit3(t *testing.T) {
	r, env, _ := fake(t)
	env["FAKE_BACKUP_EXIT"] = "3"
	sum, err := r.Backup(context.Background(), []string{"/m"}, nil, nil)
	if ExitCode(err) != ExitPartial || sum.SnapshotID == "" {
		t.Fatalf("sum=%+v err=%v", sum, err)
	}
}

func TestBackupWithoutSummaryFails(t *testing.T) {
	r, env, _ := fake(t)
	os.WriteFile(env["FAKE_BACKUP_OUT"], []byte(`{"message_type":"status"}`+"\n"), 0o600)
	if _, err := r.Backup(context.Background(), []string{"/m"}, nil, nil); err == nil {
		t.Fatal("missing summary accepted")
	}
}

func TestExistsMapsExitCodes(t *testing.T) {
	r, env, _ := fake(t)
	if ok, err := r.Exists(context.Background()); !ok || err != nil {
		t.Errorf("exit 0: %v %v", ok, err)
	}
	env["FAKE_CAT_EXIT"] = "10"
	if ok, err := r.Exists(context.Background()); ok || err != nil {
		t.Errorf("exit 10: %v %v", ok, err)
	}
	env["FAKE_CAT_EXIT"] = "12"
	if _, err := r.Exists(context.Background()); ExitCode(err) != ExitWrongPassword {
		t.Errorf("exit 12: %v", err)
	}
}

func TestSnapshotsAndShortID(t *testing.T) {
	r, _, log := fake(t)
	s, err := r.Snapshots(context.Background(), "backup")
	if err != nil || len(s) != 2 || s[1].Short() != "fedcba98" || s[0].Time.Day() != 14 {
		t.Fatalf("%+v %v", s, err)
	}
	if !strings.Contains(log(), "snapshots --json --tag backup") {
		t.Error(log())
	}
}

func TestForgetArgs(t *testing.T) {
	r, _, log := fake(t)
	if err := r.Forget(context.Background(), "backup", config.Retention{Daily: 14, Weekly: 8, Monthly: 12}, true); err != nil {
		t.Fatal(err)
	}
	want := "--retry-lock 30m forget --tag backup --group-by host,tags --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune"
	if !strings.Contains(log(), want) {
		t.Errorf("%s\nwant %s", log(), want)
	}
}

func TestGlobalFlagsAndLowPriority(t *testing.T) {
	r, _, _ := fake(t)
	r.LimitUploadKiB = 5120
	r.LowPriority = true
	cmd := r.command(context.Background(), "check")
	got := strings.Join(cmd.Args, " ")
	want := "nice -n 19 ionice -c 3 " + r.Bin + " --limit-upload 5120 --retry-lock 30m check"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestCheckArgsRetryLock(t *testing.T) {
	r, _, log := fake(t)
	if err := r.Check(context.Background(), "5%"); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(log()), "--retry-lock 30m check --read-data-subset 5%"; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestUnlockRemovesOnlyStaleLocks(t *testing.T) {
	r, env, log := fake(t)
	if err := r.Unlock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(log()); got != "unlock" {
		t.Errorf("args = %q, want plain unlock (never --remove-all)", got)
	}
	env["FAKE_UNLOCK_EXIT"] = "1"
	if err := r.Unlock(context.Background()); ExitCode(err) != 1 {
		t.Errorf("unlock failure not reported: %v", err)
	}
}

func TestCommandCancelsGracefully(t *testing.T) {
	r, _, _ := fake(t)
	cmd := r.command(context.Background(), "check")
	if cmd.WaitDelay != 60*time.Second {
		t.Errorf("WaitDelay = %v, want 60s", cmd.WaitDelay)
	}
}

func TestCancelledRunSendsSIGTERMAndSaysCancelled(t *testing.T) {
	r, env, log := fake(t)
	env["FAKE_HANG"] = "1"
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := r.Check(ctx, "")
	if err == nil {
		t.Fatal("cancelled run reported success")
	}
	if !strings.Contains(err.Error(), "cancelled") || !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("error does not say the run was cancelled: %v", err)
	}
	if !strings.Contains(log(), "got SIGTERM") {
		t.Errorf("restic was not sent SIGTERM:\n%s", log())
	}
}

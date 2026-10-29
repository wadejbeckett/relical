package job

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
)

func logConfig(t *testing.T) *config.Config {
	dir := t.TempDir()
	return &config.Config{StateDir: dir, Destinations: map[string]config.Destination{
		"d": {Repository: "rest:https://u:hunter2pw@backup.example.test/{subscription}/", Env: map[string]string{"RESTIC_REST_PASSWORD": "s3cr\"et", "RESTIC_CACERT": "/etc/ca.pem"}},
	}}
}

func readLog(t *testing.T, p string) []LogLine {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []LogLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l LogLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("not a JSON line: %s", sc.Text())
		}
		out = append(out, l)
	}
	return out
}

func TestLogWritesLinesRedacted(t *testing.T) {
	cfg := logConfig(t)
	now := func() time.Time { return time.Date(2026, 10, 29, 1, 0, 0, 0, time.UTC) }
	l := OpenLog(cfg, "backup", []string{"backup", "--all"}, now)
	l.Result(Result{Subscription: "example.test", Unit: "mail", Action: "backup",
		Error: `Fatal: unable to open repository at rest:https://u:hunter2pw@backup.example.test/example.test/: password s3cr"et`})
	l.Error(errors.New("another relical run holds the lock"))
	l.Result(Result{Note: "rest:https://u:123#hunter2pw@h/x s3:https://hunter2pwtoken@h/y"})
	l.End(1)
	if l.Err != nil {
		t.Fatal(l.Err)
	}
	p := filepath.Join(cfg.StateDir, LogName)
	fi, err := os.Stat(p)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", fi.Mode(), err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "hunter2pw") || strings.Contains(string(raw), "s3cr") {
		t.Errorf("secret in the log:\n%s", raw)
	}
	lines := readLog(t, p)
	if len(lines) != 5 || lines[0].Event != "start" || lines[0].PID == 0 || lines[1].Event != "result" ||
		lines[2].Error != "another relical run holds the lock" || lines[4].Event != "end" || lines[4].Code == nil || *lines[4].Code != 1 {
		t.Fatalf("%s", raw)
	}
	for _, ln := range lines {
		if ln.Command != "backup" || ln.Time != "2026-10-29T01:00:00Z" {
			t.Errorf("%+v", ln)
		}
	}
	if !strings.Contains(string(raw), "https://***@backup.example.test") {
		t.Errorf("repository not kept in redacted form:\n%s", raw)
	}
}

func TestLogRotates(t *testing.T) {
	old := LogMaxBytes
	LogMaxBytes = 400
	defer func() { LogMaxBytes = old }()
	cfg := logConfig(t)
	p := filepath.Join(cfg.StateDir, LogName)
	os.WriteFile(p+".1", []byte("the generation before\n"), 0o600)
	l := OpenLog(cfg, "check", nil, time.Now)
	for i := 0; i < 5; i++ {
		l.Result(Result{Subscription: "example.test", Action: "check", OK: true})
	}
	if l.Err != nil {
		t.Fatal(l.Err)
	}
	cur, _ := os.Stat(p)
	prev, err := os.ReadFile(p + ".1")
	if err != nil || cur.Size() > LogMaxBytes || strings.Contains(string(prev), "the generation before") {
		t.Fatalf("current %d bytes, .1: %v %q", cur.Size(), err, prev)
	}
	if _, err := os.Stat(p + ".2"); !os.IsNotExist(err) {
		t.Errorf("only one generation is kept: %v", err)
	}
	if len(readLog(t, p))+len(readLog(t, p+".1")) < 3 {
		t.Error("lines lost")
	}
}

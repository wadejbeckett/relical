package job

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
)

// LogName is the run log in the state directory; LogMaxBytes is the size
// at which it is rotated to LogName+".1" (one generation is kept).
const LogName = "relical.log"

var LogMaxBytes int64 = 10 << 20 // a variable so that tests can rotate early

// Log appends JSON lines to <state_dir>/relical.log: a start line, one
// line per result and an end line for every backup, forget, check, adopt
// and restore. Repository strings and secret destination variables are
// redacted. Writing the log never fails a run; the first error is kept in
// Err for the caller to report.
type Log struct {
	path    string
	command string
	now     func() time.Time
	secrets []string
	Err     error
}

// LogLine is one line of the run log.
type LogLine struct {
	Time    string   `json:"time"`
	Event   string   `json:"event"` // start, result or end
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	PID     int      `json:"pid,omitempty"`
	Code    *int     `json:"code,omitempty"`
	Result  any      `json:"result,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// OpenLog starts the run log for command and writes its start line.
func OpenLog(cfg *config.Config, command string, args []string, now func() time.Time) *Log {
	l := &Log{path: filepath.Join(cfg.StateDir, LogName), command: command, now: now}
	for _, d := range cfg.Destinations {
		for k, v := range d.Env {
			if secretVar(k) && len(v) >= 4 {
				// As it appears inside a JSON string.
				q, _ := json.Marshal(v)
				l.secrets = append(l.secrets, string(q[1:len(q)-1]))
			}
		}
	}
	l.write(LogLine{Event: "start", Args: args, PID: os.Getpid()})
	return l
}

// secretVar reports whether a destination variable holds a secret.
func secretVar(name string) bool {
	n := strings.ToUpper(name)
	for _, s := range []string{"PASSWORD", "SECRET", "KEY", "TOKEN", "CREDENTIAL"} {
		if strings.Contains(n, s) {
			return true
		}
	}
	return false
}

// Result logs one result (a job.Result, or a restore's result).
func (l *Log) Result(v any) { l.write(LogLine{Event: "result", Result: v}) }

// Error logs an error that stopped the command before it had results.
func (l *Log) Error(err error) { l.write(LogLine{Event: "result", Error: err.Error()}) }

// End writes the end line with the exit code.
func (l *Log) End(code int) { l.write(LogLine{Event: "end", Code: &code}) }

// urlPassword matches the password in a URL's user information, as in a
// rest: or s3: repository (config.RedactRepository does the same for one
// repository string; the log redacts every one in a line).
var urlPassword = regexp.MustCompile(`(://[^/\s:@"]*):[^@\s/"]*@`)

func (l *Log) redact(s string) string {
	s = urlPassword.ReplaceAllString(s, "${1}:xxxxx@")
	for _, v := range l.secrets {
		s = strings.ReplaceAll(s, v, "xxxxx")
	}
	return s
}

func (l *Log) write(line LogLine) {
	line.Time = l.now().UTC().Format(time.RFC3339)
	line.Command = l.command
	b, err := json.Marshal(line)
	if err != nil {
		l.fail(err)
		return
	}
	b = append([]byte(l.redact(string(b))), '\n')
	if fi, err := os.Stat(l.path); err == nil && fi.Size()+int64(len(b)) > LogMaxBytes {
		if err := os.Rename(l.path, l.path+".1"); err != nil {
			l.fail(err)
		}
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		l.fail(err)
		return
	}
	defer f.Close()
	if err := f.Chmod(0o600); err != nil {
		l.fail(err)
		return
	}
	if _, err := f.Write(b); err != nil {
		l.fail(err)
	}
}

func (l *Log) fail(err error) {
	if l.Err == nil {
		l.Err = err
	}
}

// Command relical backs up and restores mail with restic. Every command
// prints JSON on stdout; panel adapters call it and never run restic.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/locator"
	"github.com/wadejbeckett/relical/core/internal/restore"
)

var version = "0.1.0-dev"

const usage = `usage: relical [-config PATH] COMMAND [flags]
commands: version, mailboxes, backup, forget, check, snapshots, restore, status`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func emit(w io.Writer, v any, err error) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err != nil {
		enc.Encode(map[string]string{"error": err.Error()})
		return 1
	}
	enc.Encode(v)
	return 0
}

// fakeEnvFromProcess passes FAKE_* variables to child processes when
// RELICAL_TEST_ENV=1. It exists only so the CLI tests can drive the fakes.
func fakeEnvFromProcess() map[string]string {
	if os.Getenv("RELICAL_TEST_ENV") != "1" {
		return nil
	}
	m := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "FAKE_") {
			m[k] = v
		}
	}
	return m
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	global := flag.NewFlagSet("relical", flag.ContinueOnError)
	global.SetOutput(stderr)
	cfgPath := global.String("config", "/etc/relical/config.json", "configuration file")
	if err := global.Parse(args); err != nil {
		return 2
	}
	rest := global.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	cmd, cmdArgs := rest[0], rest[1:]
	switch cmd {
	case "version", "mailboxes", "backup", "forget", "check", "snapshots", "restore", "status":
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if cmd == "version" {
		return emit(stdout, map[string]string{"version": version}, nil)
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	domain := fs.String("domain", "", "domain name")
	all := fs.Bool("all", false, "every enabled domain")
	mailbox := fs.String("mailbox", "", "mailbox local part (restore)")
	snapshot := fs.String("snapshot", "latest", "snapshot id or latest (restore)")
	folder := fs.String("folder", "", "IMAP folder to restore; default the whole mailbox")
	subset := fs.String("read-data-subset", "", "check: also read this share of the data, e.g. 5%")
	if err := fs.Parse(cmdArgs); err != nil {
		return 2
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emit(stdout, nil, err)
	}
	env := job.Env{Cfg: cfg, Locator: locator.Pattern(cfg.MailboxPattern), Now: time.Now, ExtraEnv: fakeEnvFromProcess()}
	if err := env.Prepare(); err != nil {
		return emit(stdout, nil, err)
	}

	switch cmd {
	case "mailboxes":
		boxes, err := env.Locator.Mailboxes(*domain)
		return emit(stdout, boxes, err)
	case "snapshots":
		r, _, err := env.Runner(*domain)
		if err != nil {
			return emit(stdout, nil, err)
		}
		snaps, err := r.Snapshots(ctx, "")
		return emit(stdout, snaps, err)
	case "status":
		st, err := job.ReadState(cfg.StateDir)
		return emit(stdout, st, err)
	case "restore":
		res, err := restore.Run(ctx, env, restore.Request{Domain: *domain, Mailbox: *mailbox, Snapshot: *snapshot, Folder: *folder})
		return emit(stdout, res, err)
	}

	// backup, forget, check
	if (*domain == "") == !*all {
		fmt.Fprintln(stderr, "give exactly one of --domain or --all")
		return 2
	}
	domains := cfg.Domains
	if *domain != "" {
		domains = []string{*domain}
	}
	unlock, err := job.Lock(cfg.StateDir)
	if err != nil {
		return emit(stdout, nil, err)
	}
	defer unlock()
	results := []job.Result{}
	for _, d := range domains {
		if ctx.Err() != nil {
			break
		}
		switch cmd {
		case "backup":
			results = append(results, env.Backup(ctx, d))
		case "forget":
			results = append(results, env.Forget(ctx, d))
		case "check":
			results = append(results, env.Check(ctx, d, *subset))
		}
	}
	if err := env.Record(results); err != nil {
		fmt.Fprintln(stderr, "warning: state not saved:", err)
	}
	code := emit(stdout, results, nil)
	for _, r := range results {
		if !r.OK {
			code = 1
		}
	}
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, "interrupted")
		return 1
	}
	return code
}

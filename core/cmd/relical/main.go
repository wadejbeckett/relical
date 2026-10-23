// Command relical backs up and restores hosting subscriptions with restic. Every command
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
	"github.com/wadejbeckett/relical/core/internal/inventory"
	"github.com/wadejbeckett/relical/core/internal/job"
	"github.com/wadejbeckett/relical/core/internal/locator"
	"github.com/wadejbeckett/relical/core/internal/restore"
)

var version = "0.1.0-dev"

const usage = `usage: relical [-config PATH] COMMAND [flags]
commands:
  version
  inventory
  mailboxes --subscription S
  backup    --subscription S | --all [--unit mail|files|db]
  forget    --subscription S | --all
  check     --subscription S | --all [--read-data-subset 5%]
  snapshots --subscription S [--unit mail|files|db|panel-config]
  restore   [--subscription S] [--unit mail] --domain D --mailbox M [--snapshot ID] [--folder F]
  status`

// commandFlags lists the flags each command accepts.
var commandFlags = map[string][]string{
	"version":   {},
	"inventory": {},
	"status":    {},
	"mailboxes": {"subscription"},
	"backup":    {"subscription", "all", "unit"},
	"forget":    {"subscription", "all"},
	"check":     {"subscription", "all", "read-data-subset"},
	"snapshots": {"subscription", "unit"},
	"restore":   {"subscription", "unit", "domain", "mailbox", "snapshot", "folder"},
}

// units are the snapshot units of the CLI contract; implemented says
// which ones this version can back up and which it can restore.
var (
	units       = map[string]bool{"mail": true, "files": true, "db": true, "panel-config": true}
	implemented = map[string]map[string]bool{
		"backup":  {"mail": true, "files": true, "db": true},
		"restore": {"mail": true},
	}
)

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

func usageError(stderr io.Writer, msg string) int {
	if msg != "" {
		fmt.Fprintln(stderr, msg)
	}
	fmt.Fprintln(stderr, usage)
	return 2
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
		return usageError(stderr, "")
	}
	cmd, cmdArgs := rest[0], rest[1:]
	allowed, ok := commandFlags[cmd]
	if !ok {
		return usageError(stderr, "unknown command "+cmd)
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	subName := fs.String("subscription", "", "subscription name (its main domain), or _server for the panel configuration")
	all := fs.Bool("all", false, "every subscription in the inventory")
	unit := fs.String("unit", "", "unit: mail, files, db or panel-config")
	domain := fs.String("domain", "", "mail domain (restore)")
	mailbox := fs.String("mailbox", "", "mailbox local part (restore)")
	snapshot := fs.String("snapshot", "latest", "snapshot id or latest (restore)")
	folder := fs.String("folder", "", "IMAP folder to restore; default the whole mailbox")
	subset := fs.String("read-data-subset", "", "check: also read this share of the data, e.g. 5%")
	if err := fs.Parse(cmdArgs); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		return usageError(stderr, fmt.Sprintf("%s: unexpected argument %q", cmd, fs.Arg(0)))
	}
	var bad []string
	fs.Visit(func(f *flag.Flag) {
		for _, a := range allowed {
			if f.Name == a {
				return
			}
		}
		bad = append(bad, "--"+f.Name)
	})
	if len(bad) > 0 {
		return usageError(stderr, fmt.Sprintf("%s does not take %s", cmd, strings.Join(bad, ", ")))
	}
	if *unit != "" && !units[*unit] {
		return usageError(stderr, fmt.Sprintf("unknown unit %q", *unit))
	}
	switch cmd {
	case "version":
		return emit(stdout, map[string]string{"version": version}, nil)
	case "backup", "forget", "check":
		if (*subName == "") == !*all {
			return usageError(stderr, "give exactly one of --subscription or --all")
		}
	case "mailboxes", "snapshots":
		if *subName == "" {
			return usageError(stderr, cmd+" needs --subscription")
		}
	case "restore":
		if *domain == "" || *mailbox == "" {
			return usageError(stderr, "restore needs --domain and --mailbox")
		}
		if *unit == "" {
			*unit = "mail"
		}
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return emit(stdout, nil, err)
	}
	if impl, ok := implemented[cmd]; ok && *unit != "" && !impl[*unit] {
		return emit(stdout, nil, fmt.Errorf("unit %s is not supported by this version of relical", *unit))
	}
	env := job.Env{Cfg: cfg, Locator: locator.Pattern(cfg.MailboxPattern), Now: time.Now, ExtraEnv: fakeEnvFromProcess()}
	if cmd != "status" {
		if env.Inv, err = inventory.Load(ctx, cfg); err != nil {
			return emit(stdout, nil, err)
		}
	}
	if cmd == "inventory" {
		return emit(stdout, env.Inv, nil)
	}
	if err := env.Prepare(); err != nil {
		return emit(stdout, nil, err)
	}

	switch cmd {
	case "mailboxes":
		sub, err := env.Subscription(*subName)
		if err != nil {
			return emit(stdout, nil, err)
		}
		boxes, err := env.Mailboxes(sub)
		return emit(stdout, boxes, err)
	case "snapshots":
		sub, err := env.Subscription(*subName)
		if err != nil {
			return emit(stdout, nil, err)
		}
		r, _, err := env.Runner(sub)
		if err != nil {
			return emit(stdout, nil, err)
		}
		snaps, err := r.Snapshots(ctx, *unit)
		return emit(stdout, snaps, err)
	case "status":
		st, err := job.ReadState(cfg.StateDir)
		return emit(stdout, st, err)
	case "restore":
		res, err := restore.Run(ctx, env, restore.Request{Subscription: *subName, Domain: *domain, Mailbox: *mailbox, Snapshot: *snapshot, Folder: *folder})
		return emit(stdout, res, err)
	}

	// backup, forget, check
	subs := env.Inv.Subscriptions
	if *subName != "" {
		sub, err := env.Subscription(*subName)
		if err != nil {
			return emit(stdout, nil, err)
		}
		subs = []inventory.Subscription{sub}
	}
	unlock, err := job.Lock(cfg.StateDir)
	if err != nil {
		return emit(stdout, nil, err)
	}
	defer unlock()
	results := []job.Result{}
	switch {
	case cmd == "backup" && *all:
		results = env.BackupAll(ctx, *unit)
	case cmd == "backup":
		results = append(results, env.BackupSubscription(ctx, subs[0], *unit)...)
	}
	for _, s := range subs {
		if ctx.Err() != nil || cmd == "backup" {
			break
		}
		switch cmd {
		case "forget":
			results = append(results, env.Forget(ctx, s))
		case "check":
			results = append(results, env.Check(ctx, s, *subset))
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

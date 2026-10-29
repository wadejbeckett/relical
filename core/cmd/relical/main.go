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

// version is set by the Makefile (-X main.version).
var version = "0.2.0-dev"

const usage = `usage: relical [-config PATH] COMMAND [flags]
commands:
  version
  inventory
  mailboxes --subscription S
  backup    --subscription S | --all [--unit mail|files|db|panel-config] [--no-forget]
  forget    --subscription S | --all
  check     --subscription S | --all [--read-data-subset 5%]
  snapshots --subscription S [--unit mail|files|db|panel-config]
  ls        --subscription S --snapshot ID [--path P]
  restore   [--subscription S] [--unit mail] --domain D --mailbox M [--snapshot ID] [--folder F]
  restore   --subscription S --unit files [--snapshot ID] [--path P]
  restore   --subscription S --unit db --database D [--snapshot ID]
  restore   --subscription _server --unit panel-config [--snapshot ID]
  status
  report
  adopt     --subscription S`

// commandFlags lists the flags each command accepts.
var commandFlags = map[string][]string{
	"version":   {},
	"inventory": {},
	"status":    {},
	"report":    {},
	"mailboxes": {"subscription"},
	"backup":    {"subscription", "all", "unit", "no-forget"},
	"forget":    {"subscription", "all"},
	"check":     {"subscription", "all", "read-data-subset"},
	"snapshots": {"subscription", "unit"},
	"ls":        {"subscription", "snapshot", "path"},
	"restore":   {"subscription", "unit", "domain", "mailbox", "snapshot", "folder", "path", "database"},
	"adopt":     {"subscription"},
}

// units are the snapshot units of the CLI contract; implemented says
// which ones this version can back up and which it can restore.
var (
	units       = map[string]bool{"mail": true, "files": true, "db": true, "panel-config": true}
	implemented = map[string]map[string]bool{
		"backup":  {"mail": true, "files": true, "db": true, "panel-config": true},
		"restore": {"mail": true, "files": true, "db": true, "panel-config": true},
	}
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func emitJSON(w io.Writer, v any, err error) int {
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

func run(ctx context.Context, args []string, stdout, stderr io.Writer) (code int) {
	// lg is the run log of a logged command (see logged); emit records
	// in it what the command prints.
	var lg *job.Log
	emit := func(w io.Writer, v any, err error) int {
		if lg != nil {
			logOutput(lg, v, err)
		}
		return emitJSON(w, v, err)
	}
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
	path := fs.String("path", "", "path relative to the webspace root (restore --unit files, ls)")
	database := fs.String("database", "", "database name (restore --unit db)")
	noForget := fs.Bool("no-forget", false, "backup --all: do not apply retention afterwards")
	if err := fs.Parse(cmdArgs); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		return usageError(stderr, fmt.Sprintf("%s: unexpected argument %q", cmd, fs.Arg(0)))
	}
	var bad []string
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		set[f.Name] = true
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
		if set["no-forget"] && !*all {
			return usageError(stderr, "--no-forget is for backup --all")
		}
	case "mailboxes", "snapshots", "adopt":
		if *subName == "" {
			return usageError(stderr, cmd+" needs --subscription")
		}
	case "ls":
		if *subName == "" || !set["snapshot"] {
			return usageError(stderr, "ls needs --subscription and --snapshot")
		}
	case "restore":
		if *unit == "" {
			*unit = "mail"
		}
		// The flags each restore unit needs and takes, besides
		// --subscription, --unit and --snapshot.
		need := map[string][]string{"mail": {"domain", "mailbox"}, "files": {}, "db": {"database"}, "panel-config": {}}[*unit]
		takes := map[string][]string{"mail": {"folder"}, "files": {"path"}}[*unit]
		for _, f := range need {
			if !set[f] || fs.Lookup(f).Value.String() == "" {
				return usageError(stderr, fmt.Sprintf("restore --unit %s needs --%s", *unit, f))
			}
		}
		for _, f := range []string{"domain", "mailbox", "folder", "path", "database"} {
			if set[f] && !contains(need, f) && !contains(takes, f) {
				return usageError(stderr, fmt.Sprintf("restore --unit %s does not take --%s", *unit, f))
			}
		}
		if *subName == "" && *unit != "mail" {
			return usageError(stderr, fmt.Sprintf("restore --unit %s needs --subscription", *unit))
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
	if logged[cmd] {
		// The state directory holds the log, so it is prepared first.
		if err := env.Prepare(); err != nil {
			return emit(stdout, nil, err)
		}
		lg = job.OpenLog(cfg, cmd, args, env.Now)
		defer func() {
			lg.End(code)
			if lg.Err != nil {
				fmt.Fprintln(stderr, "warning: the run log could not be written:", lg.Err)
			}
		}()
	}
	if cmd == "report" {
		return report(ctx, env, stdout)
	}
	if cmd != "status" {
		if env.Inv, err = inventory.Load(ctx, cfg); err != nil {
			return emit(stdout, nil, err)
		}
	}
	if cmd == "inventory" {
		return emit(stdout, env.Inv, nil)
	}
	if lg != nil && *all && len(env.Inv.Skipped) > 0 {
		lg.Skipped(env.Inv.Skipped)
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
	case "adopt":
		sub, err := env.Subscription(*subName)
		if err != nil {
			return emit(stdout, nil, err)
		}
		unlock, err := job.Lock(cfg.StateDir)
		if err != nil {
			return emit(stdout, nil, err)
		}
		defer unlock()
		if err := env.CleanStaging(); err != nil {
			return emit(stdout, nil, err)
		}
		res := env.Adopt(ctx, sub)
		if err := env.Record([]job.Result{res}); err != nil {
			fmt.Fprintln(stderr, "warning: state not saved:", err)
		}
		code = emit(stdout, []job.Result{res}, nil)
		if !res.OK {
			code = 1
		}
		return code
	case "ls":
		res, err := restore.Ls(ctx, env, restore.LsRequest{Subscription: *subName, Snapshot: *snapshot, Path: *path})
		return emit(stdout, res, err)
	case "restore":
		switch *unit {
		case "files":
			res, err := restore.RunFiles(ctx, env, restore.FilesRequest{Subscription: *subName, Snapshot: *snapshot, Path: *path})
			return emit(stdout, res, err)
		case "db":
			res, err := restore.RunDB(ctx, env, restore.DBRequest{Subscription: *subName, Database: *database, Snapshot: *snapshot})
			return emit(stdout, res, err)
		case "panel-config":
			res, err := restore.RunPanelConfig(ctx, env, restore.PanelConfigRequest{Subscription: *subName, Snapshot: *snapshot})
			return emit(stdout, res, err)
		}
		res, err := restore.Run(ctx, env, restore.Request{Subscription: *subName, Domain: *domain, Mailbox: *mailbox, Snapshot: *snapshot, Folder: *folder})
		return emit(stdout, res, err)
	}

	// backup, forget, check. forget and check --all also cover the panel
	// configuration's repository when the server backs it up.
	subs := env.Inv.Subscriptions
	if *all && env.Inv.Server.Has(job.PanelConfigTag) {
		subs = append(append([]inventory.Subscription{}, subs...), job.ServerSubscription())
	}
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
	// Under the lock no panel configuration backup is running, so whatever
	// is in staging was left by a killed run.
	if err := env.CleanStaging(); err != nil {
		return emit(stdout, nil, err)
	}
	if cmd == "backup" && *all {
		if err := restore.CleanStale(cfg, env.Now()); err != nil {
			fmt.Fprintln(stderr, "warning:", err)
		}
	}
	if *all {
		// An unreachable destination is given up on after a few
		// subscriptions instead of costing timeouts for every one.
		env.Breaker = job.NewBreaker()
	}
	// Subscriptions whose inventory entry is invalid were left out: they
	// fail the run and are logged, but are not recorded in the state file.
	invalid := []job.Result{}
	if *all {
		invalid = env.InvalidResults(cmd)
	}
	for _, r := range invalid {
		if lg != nil {
			lg.Result(r)
		}
	}
	// Each subscription's results reach state.json and the run log as soon
	// as they are known, so a run that is killed or hangs still leaves the
	// evidence of what it did.
	saveWarned := false
	env.Progress = func(rs []job.Result) {
		if err := env.Record(rs); err != nil && !saveWarned {
			saveWarned = true
			fmt.Fprintln(stderr, "warning: state not saved:", err)
		}
		if lg != nil {
			for _, r := range rs {
				lg.Result(r)
			}
		}
	}
	results := []job.Result{}
	switch {
	case cmd == "backup" && *all:
		results = env.BackupAll(ctx, *unit)
		// Retention runs in the same process, after the backups: never
		// after an interrupted run (a stop or a reboot must not start a
		// prune of every repository), and not for a run of one unit.
		if ctx.Err() == nil && *unit == "" && !*noForget {
			results = append(results, env.ForgetAll(ctx, subs)...)
		}
	case cmd == "backup":
		results = append(results, env.BackupSubscription(ctx, subs[0], *unit)...)
		env.Progress(results)
	case cmd == "forget":
		results = env.ForgetAll(ctx, subs)
	case cmd == "check":
		results = env.CheckAll(ctx, subs, *subset)
	}
	results = append(invalid, results...)
	code = emitJSON(stdout, results, nil) // already logged as they came
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

// logged are the commands whose runs are written to the run log.
var logged = map[string]bool{"backup": true, "forget": true, "check": true, "adopt": true, "restore": true}

// logOutput writes what a command prints to its run log: one line per
// result, or the error.
func logOutput(lg *job.Log, v any, err error) {
	if err != nil {
		lg.Error(err)
		return
	}
	if rs, ok := v.([]job.Result); ok {
		for _, r := range rs {
			lg.Result(r)
		}
		return
	}
	lg.Result(v)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// report prints the summary of the state file. It never fails on the
// inventory: an inventory that cannot be loaded becomes a warning in the
// report. Only an unreadable state file is an error.
func report(ctx context.Context, env job.Env, stdout io.Writer) int {
	st, err := job.ReadState(env.Cfg.StateDir)
	if err != nil {
		return emitJSON(stdout, nil, err)
	}
	var inv *inventory.Inventory
	loaded, invErr := inventory.Load(ctx, env.Cfg)
	if invErr == nil {
		inv = &loaded
	}
	return emitJSON(stdout, job.BuildReport(st, inv, invErr, env.Now()), nil)
}

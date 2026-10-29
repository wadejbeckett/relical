// Package inventory resolves which subscriptions Relical covers: a static
// list in the config, or the JSON printed by inventory_command (a panel
// adapter). The adapter is trusted to run as root, but everything it says
// is validated before any path or name is used.
package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/wadejbeckett/relical/core/internal/config"
)

type Database struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type Subscription struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	RenamedFrom  string            `json:"renamed_from"`
	SysUser      string            `json:"sys_user"`
	UID          int               `json:"uid"`
	GID          int               `json:"gid"`
	Home         string            `json:"home"`
	Features     []string          `json:"features"`
	MailDomains  []string          `json:"mail_domains"`
	Files        []string          `json:"files"`
	FileExcludes []string          `json:"file_excludes"`
	Databases    []Database        `json:"databases"`
	Destination  string            `json:"destination"`
	Retention    *config.Retention `json:"retention"`
}

// Has reports whether feature is among the subscription's features.
func (s Subscription) Has(feature string) bool {
	for _, f := range s.Features {
		if f == feature {
			return true
		}
	}
	return false
}

type Server struct {
	Features      []string `json:"features"`
	ConfigCommand []string `json:"config_command"`
	ConfigExt     string   `json:"config_ext"`
	// EncryptWithRepoKey (default true) gives config_command the _server
	// repository's key as PLESK_BACKUP_PASSWORD, so that the passwords
	// inside the panel's backup are encrypted with it.
	EncryptWithRepoKey *bool `json:"encrypt_with_repo_key"`
}

// Has reports whether feature is among the server's features.
func (s *Server) Has(feature string) bool {
	if s == nil {
		return false
	}
	for _, f := range s.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// Encrypt reports whether encrypt_with_repo_key is on (the default).
func (s *Server) Encrypt() bool { return s.EncryptWithRepoKey == nil || *s.EncryptWithRepoKey }

// Rename is a subscription whose panel name changed. Its backups go to a
// new repository under the new name; the old repository is left as it is.
type Rename struct {
	Subscription string `json:"subscription"`
	RenamedFrom  string `json:"renamed_from"`
	Note         string `json:"note"`
}

// Skip is a subscription left out of the inventory, with its reason:
// by the adapter (informational), or by validation (Invalid: the entry is
// wrong, so the subscription is not backed up; report shows it as a
// failure).
type Skip struct {
	Name    string `json:"name"`
	Reason  string `json:"reason"`
	Invalid bool   `json:"invalid,omitempty"`
}

// UnmarshalJSON accepts a bare string (the name), an object with name
// and reason, or anything else (kept as its JSON text); it never fails.
func (s *Skip) UnmarshalJSON(b []byte) error {
	var name string
	if json.Unmarshal(b, &name) == nil {
		*s = Skip{Name: name}
		return nil
	}
	var o struct {
		Name   any `json:"name"`
		Reason any `json:"reason"`
	}
	if json.Unmarshal(b, &o) == nil {
		*s = Skip{Name: text(o.Name), Reason: text(o.Reason)}
		return nil
	}
	*s = Skip{Name: string(b)}
	return nil
}

func text(v any) string {
	switch v := v.(type) {
	case nil:
		return ""
	case string:
		return v
	}
	b, _ := json.Marshal(v)
	return string(b)
}

type Inventory struct {
	Version       int            `json:"version"`
	Subscriptions []Subscription `json:"subscriptions"`
	Server        *Server        `json:"server"`
	Renamed       []Rename       `json:"renamed"`
	Skipped       []Skip         `json:"skipped"`
}

// Find returns the subscription called name.
func (inv Inventory) Find(name string) (Subscription, bool) {
	for _, s := range inv.Subscriptions {
		if s.Name == name {
			return s, true
		}
	}
	return Subscription{}, false
}

// ByMailDomain returns the subscription that owns mail domain d.
func (inv Inventory) ByMailDomain(d string) (Subscription, bool) {
	for _, s := range inv.Subscriptions {
		for _, m := range s.MailDomains {
			if m == d {
				return s, true
			}
		}
	}
	return Subscription{}, false
}

// DefaultFileExcludes apply when a subscription lists none.
// restored-* are the folders file and database restores create in home:
// copies of earlier backups, which the customer copies back from.
var DefaultFileExcludes = []string{"logs/**", "statistics/logs/**", "tmp/**", ".cagefs/**", "restored-*"}

// Units a subscription may back up, and the server's.
var (
	SubscriptionFeatures = []string{"mail", "files", "db"}
	ServerFeatures       = []string{"panel-config"}
)

// Limits on inventory_command; variables so tests can shorten them.
var (
	CommandTimeout       = 60 * time.Second
	MaxOutput      int64 = 16 << 20
	maxStderr            = 64 << 10
)

var (
	sysUserRE  = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	dbNameRE   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_$-]{0,63}$`) // never a leading - (mysqldump would read an option)
	dbTypeRE   = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
	idRE       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	configExRE = regexp.MustCompile(`^[a-z0-9]{1,8}$`)
)

// Load returns the validated inventory: the config's static one, or the
// output of its inventory_command.
func Load(ctx context.Context, cfg *config.Config) (Inventory, error) {
	var (
		subs    []Subscription
		server  *Server
		skipped []Skip
	)
	if len(cfg.InventoryCommand) > 0 {
		out, err := runCommand(ctx, cfg.InventoryCommand)
		if err != nil {
			return Inventory{}, err
		}
		var w struct {
			Version       int             `json:"version"`
			Subscriptions json.RawMessage `json:"subscriptions"`
			Server        json.RawMessage `json:"server"`
			Skipped       json.RawMessage `json:"skipped"`
		}
		if err := decodeStrict(out, &w); err != nil {
			return Inventory{}, fmt.Errorf("inventory_command output: %w", err)
		}
		if w.Version != config.Version {
			return Inventory{}, fmt.Errorf("inventory_command output: version must be %d, got %d", config.Version, w.Version)
		}
		if !isSet(w.Subscriptions) {
			return Inventory{}, errors.New("inventory_command output has no subscriptions list")
		}
		if err := decodeStrict(w.Subscriptions, &subs); err != nil {
			return Inventory{}, fmt.Errorf("inventory_command output: subscriptions: %w", err)
		}
		if isSet(w.Server) && cfg.HasServer() {
			return Inventory{}, errors.New("server is set both in the config and by inventory_command; keep one")
		}
		raw := cfg.Server
		if isSet(w.Server) {
			raw = w.Server
		}
		if server, err = decodeServer(raw); err != nil {
			return Inventory{}, err
		}
		skipped = decodeSkipped(w.Skipped)
	} else {
		if err := decodeStrict(cfg.Subscriptions, &subs); err != nil {
			return Inventory{}, fmt.Errorf("subscriptions: %w", err)
		}
		var err error
		if server, err = decodeServer(cfg.Server); err != nil {
			return Inventory{}, err
		}
	}
	// A panel always has a stable id; it ties each repository to its owner
	// (see keys.CheckOwner), so the adapter must give it.
	inv, err := validate(cfg, subs, server, len(cfg.InventoryCommand) > 0)
	if err != nil {
		return inv, err
	}
	inv.Skipped = append(inv.Skipped, skipped...)
	return inv, nil
}

// decodeSkipped reads the informational skipped list; a list that is not
// an array is kept as one entry rather than failing the inventory.
func decodeSkipped(raw json.RawMessage) []Skip {
	if !isSet(raw) {
		return nil
	}
	var s []Skip
	if json.Unmarshal(raw, &s) != nil {
		return []Skip{{Name: string(raw), Reason: "skipped is not a JSON array"}}
	}
	return s
}

func isSet(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

func decodeServer(raw json.RawMessage) (*Server, error) {
	if !isSet(raw) {
		return nil, nil
	}
	var s Server
	if err := decodeStrict(raw, &s); err != nil {
		return nil, fmt.Errorf("server: %w", err)
	}
	return &s, nil
}

// decodeStrict refuses unknown fields and trailing data.
func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// dec.More would miss a stray ] or } after the value.
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("unexpected data after the JSON value")
	}
	return nil
}

// cappedWriter fails once more than max bytes are written, which makes
// exec close the pipe and the command's next write fail.
type cappedWriter struct {
	buf bytes.Buffer
	max int64
	hit bool
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	if int64(w.buf.Len()+len(p)) > w.max {
		w.hit = true
		return 0, fmt.Errorf("output exceeds %d bytes", w.max)
	}
	return w.buf.Write(p)
}

// truncWriter keeps the first max bytes and silently drops the rest.
type truncWriter struct {
	buf bytes.Buffer
	max int
}

func (w *truncWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		if len(p) > room {
			w.buf.Write(p[:room])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

func runCommand(ctx context.Context, argv []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, CommandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// The command runs in its own process group, so that a timeout or an
	// interrupted run also kills its children (plesk runs PHP).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	stdout := &cappedWriter{max: MaxOutput}
	stderr := &truncWriter{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	msg := strings.TrimSpace(stderr.buf.String())
	switch {
	case stdout.hit:
		return nil, fmt.Errorf("inventory_command: output exceeds %d bytes", MaxOutput)
	case ctx.Err() == context.DeadlineExceeded:
		return nil, fmt.Errorf("inventory_command: timed out after %s: %s", CommandTimeout, msg)
	case ctx.Err() != nil:
		return nil, fmt.Errorf("inventory_command: interrupted (%v): %s", ctx.Err(), msg)
	case err != nil:
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, fmt.Errorf("inventory_command: exit %d: %s", ee.ExitCode(), msg)
		}
		return nil, fmt.Errorf("inventory_command: %w", err)
	}
	return stdout.buf.Bytes(), nil
}

func subset(have, allowed []string) error {
	seen := map[string]bool{}
	for _, f := range have {
		ok := false
		for _, a := range allowed {
			ok = ok || f == a
		}
		if !ok {
			return fmt.Errorf("unknown feature %q (allowed: %s)", f, strings.Join(allowed, ", "))
		}
		if seen[f] {
			return fmt.Errorf("feature %q listed twice", f)
		}
		seen[f] = true
	}
	return nil
}

// checkPath validates a backed-up or restore path against the roots.
func checkPath(p string, roots []string) error {
	if !config.CleanAbs(p) {
		return fmt.Errorf("%q must be a clean absolute path without ..", p)
	}
	if !config.Under(p, roots) {
		return fmt.Errorf("%q is not under a files root (%s)", p, strings.Join(roots, ", "))
	}
	return nil
}

func validExclude(x string) bool {
	if x == "" || strings.HasPrefix(x, "/") {
		return false
	}
	for _, r := range x {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(x, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func validateSubscription(cfg *config.Config, s *Subscription, requireID bool) []error {
	var errs []error
	add := func(field string, err error) { errs = append(errs, fmt.Errorf("%s: %w", field, err)) }
	if s.ID == "" && requireID {
		add("id", errors.New("is required in inventory_command output"))
	}
	if s.ID != "" && !idRE.MatchString(s.ID) {
		add("id", fmt.Errorf("%q must be 1-64 letters, digits, _ or -", s.ID))
	}
	if s.RenamedFrom != "" {
		if !config.ValidDomain(s.RenamedFrom) {
			add("renamed_from", fmt.Errorf("%q is not a valid lower-case domain name", s.RenamedFrom))
		} else if s.RenamedFrom == s.Name {
			add("renamed_from", errors.New("is the subscription's own name"))
		}
	}
	// A mail-only subscription needs no system user or webspace: mail
	// restores run as the mailbox owner. files and db restores write into
	// home as the system user, so they need all of them.
	webspace := s.Has("files") || s.Has("db")
	why := " (required when features include files or db)"
	switch {
	case s.SysUser == "" && webspace:
		add("sys_user", errors.New("is empty"+why))
	case s.SysUser != "" && !sysUserRE.MatchString(s.SysUser):
		add("sys_user", fmt.Errorf("%q is not a valid system user name", s.SysUser))
	}
	if s.UID < 0 || (s.UID == 0 && webspace) {
		add("uid", fmt.Errorf("%d must be greater than 0%s", s.UID, why))
	}
	if s.GID < 0 || (s.GID == 0 && webspace) {
		add("gid", fmt.Errorf("%d must be greater than 0%s", s.GID, why))
	}
	if s.Home != "" || webspace {
		if err := checkPath(s.Home, cfg.Roots.Files); err != nil {
			add("home", err)
		} else if !isWebspaceOf(s.Home, s.Name, cfg.Roots.Files) {
			// Panels name the webspace directory after the subscription;
			// this also keeps Plesk's own trees (system, chroot, ...) out.
			add("home", fmt.Errorf("%q must be <files root>/%s", s.Home, s.Name))
		}
	}
	if len(s.Files) == 0 && s.Has("files") {
		add("files", errors.New("is empty (required when features include files)"))
	}
	if err := subset(s.Features, SubscriptionFeatures); err != nil {
		add("features", err)
	}
	for i, d := range s.MailDomains {
		if !config.ValidDomain(d) {
			add(fmt.Sprintf("mail_domains[%d]", i), fmt.Errorf("%q is not a valid lower-case domain name", d))
		}
	}
	for i, p := range s.Files {
		if err := checkPath(p, cfg.Roots.Files); err != nil {
			add(fmt.Sprintf("files[%d]", i), err)
		} else if s.Home == "" || !within(p, s.Home) {
			add(fmt.Sprintf("files[%d]", i), fmt.Errorf("%q must be the subscription's home or lie under it", p))
		}
	}
	for i, x := range s.FileExcludes {
		if !validExclude(x) {
			add(fmt.Sprintf("file_excludes[%d]", i), fmt.Errorf("%q must be a relative pattern without .. or control characters", x))
		}
	}
	dbs := map[string]bool{}
	for i, d := range s.Databases {
		if !dbTypeRE.MatchString(d.Type) {
			add(fmt.Sprintf("databases[%d].type", i), fmt.Errorf("%q is not a valid database type", d.Type))
		}
		if !dbNameRE.MatchString(d.Name) {
			add(fmt.Sprintf("databases[%d].name", i), fmt.Errorf("%q must be 1-64 of A-Z a-z 0-9 _ $ -, starting with a letter, digit or _", d.Name))
		} else if reservedDB(d.Name) {
			add(fmt.Sprintf("databases[%d].name", i), fmt.Errorf("%q is a server or panel database and never belongs to a subscription", d.Name))
		} else if dbs[d.Type+"/"+d.Name] {
			add(fmt.Sprintf("databases[%d].name", i), fmt.Errorf("%q is listed twice", d.Name))
		}
		dbs[d.Type+"/"+d.Name] = true
	}
	if s.Destination != "" {
		if _, ok := cfg.Destinations[s.Destination]; !ok {
			add("destination", fmt.Errorf("%q is not in destinations", s.Destination))
		}
	}
	if s.Retention != nil {
		if err := s.Retention.Validate(); err != nil {
			add("retention", err)
		}
	}
	return errs
}

func validateServer(s *Server) []error {
	var errs []error
	add := func(field string, err error) { errs = append(errs, fmt.Errorf("server: %s: %w", field, err)) }
	if err := subset(s.Features, ServerFeatures); err != nil {
		add("features", err)
	}
	if len(s.ConfigCommand) == 0 {
		add("config_command", errors.New("is required"))
	} else {
		if !config.CleanAbs(s.ConfigCommand[0]) {
			add("config_command[0]", fmt.Errorf("%q must be a clean absolute path", s.ConfigCommand[0]))
		}
		n := 0
		for _, a := range s.ConfigCommand {
			n += strings.Count(a, "{output}")
		}
		if n != 1 {
			add("config_command", fmt.Errorf("must contain {output} exactly once, found %d", n))
		}
	}
	if !configExRE.MatchString(s.ConfigExt) {
		add("config_ext", fmt.Errorf("%q must be 1-8 lower-case letters or digits", s.ConfigExt))
	}
	return errs
}

// reservedDatabases are MySQL's and Plesk's own databases. The dump runs
// with the administrator's credentials, so a subscription listing one of
// them would get a copy of every customer's data.
var reservedDatabases = map[string]bool{
	"mysql": true, "information_schema": true, "performance_schema": true, "sys": true,
	"psa": true, "apsc": true, "horde": true, "roundcubemail": true, "atmail": true, "sitebuilder5": true,
}

// reservedDB reports whether name is a reserved database (compared
// case-insensitively, as MySQL does on case-insensitive file systems).
func reservedDB(name string) bool {
	n := strings.ToLower(name)
	return reservedDatabases[n] || strings.HasPrefix(n, "phpmyadmin")
}

// within reports whether p is dir or lies under it.
func within(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

func isWebspaceOf(home, name string, roots []string) bool {
	for _, r := range roots {
		if home == r+"/"+name {
			return true
		}
	}
	return false
}

func orEmpty[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

// validate checks every subscription and the server. A subscription's own
// errors, and a conflict between two subscriptions (a shared name, id,
// mail domain or database, nested homes, the same renamed_from), skip the
// subscriptions involved: each becomes a Skip with Invalid set and the
// reasons, and the rest of the inventory still loads, so one bad entry
// never stops every other subscription's backups and restores. Only
// errors that belong to no subscription (the server's) are fatal.
func validate(cfg *config.Config, subs []Subscription, server *Server, requireID bool) (Inventory, error) {
	inv := Inventory{Version: config.Version, Subscriptions: []Subscription{}, Server: server, Renamed: []Rename{}, Skipped: []Skip{}}
	errs := make([][]error, len(subs))
	add := func(i int, err error) { errs[i] = append(errs[i], err) }
	// conflict records err for every entry in idx when there are two or more.
	conflict := func(idx []int, err func(i int) error) {
		if len(idx) < 2 {
			return
		}
		for _, i := range idx {
			add(i, err(i))
		}
	}
	others := func(subs []Subscription, idx []int, self int) string {
		var n []string
		for _, i := range idx {
			if i != self {
				n = append(n, subs[i].Name)
			}
		}
		return strings.Join(n, ", ")
	}
	byName := map[string][]int{}
	for i := range subs {
		s := &subs[i]
		switch {
		case s.Name == config.ServerName:
			add(i, fmt.Errorf("name %s is reserved for the panel configuration", config.ServerName))
			continue
		case !config.ValidDomain(s.Name):
			add(i, fmt.Errorf("name %q is not a valid lower-case domain name", s.Name))
			continue
		}
		byName[s.Name] = append(byName[s.Name], i)
		errs[i] = append(errs[i], validateSubscription(cfg, s, requireID)...)
	}
	// Cross-subscription checks, over the entries with a valid name and
	// only the values that are valid on their own: no subscription may
	// cover another's webspace, mail or databases.
	ids, mailOwner, dbOwner, renamedFrom := map[string][]int{}, map[string][]int{}, map[string][]int{}, map[string][]int{}
	var homes []int
	for _, name := range sortedKeys(byName) {
		idx := byName[name]
		conflict(idx, func(int) error { return errors.New("listed twice") })
	}
	for i := range subs {
		s := subs[i]
		if len(byName[s.Name]) == 0 {
			continue // an invalid or reserved name
		}
		if s.ID != "" && idRE.MatchString(s.ID) {
			ids[s.ID] = append(ids[s.ID], i)
		}
		for _, d := range dedupe(s.MailDomains) {
			if config.ValidDomain(d) {
				mailOwner[d] = append(mailOwner[d], i)
				if d != s.Name && len(byName[d]) > 0 {
					add(i, fmt.Errorf("mail domain %s is another subscription's name", d))
					for _, o := range byName[d] {
						add(o, fmt.Errorf("its name is a mail domain of %s", s.Name))
					}
				}
			}
		}
		seen := map[string]bool{}
		for _, d := range s.Databases {
			k := d.Type + "/" + d.Name
			if dbTypeRE.MatchString(d.Type) && dbNameRE.MatchString(d.Name) && !seen[k] {
				seen[k] = true
				dbOwner[k] = append(dbOwner[k], i)
			}
		}
		if checkPath(s.Home, cfg.Roots.Files) == nil && isWebspaceOf(s.Home, s.Name, cfg.Roots.Files) {
			homes = append(homes, i)
		}
		if s.RenamedFrom != "" && config.ValidDomain(s.RenamedFrom) && s.RenamedFrom != s.Name {
			renamedFrom[s.RenamedFrom] = append(renamedFrom[s.RenamedFrom], i)
			if len(byName[s.RenamedFrom]) > 0 {
				add(i, fmt.Errorf("renamed_from %s is still a subscription in this inventory", s.RenamedFrom))
			}
		}
	}
	for _, id := range sortedKeys(ids) {
		idx := ids[id]
		conflict(idx, func(i int) error { return fmt.Errorf("id %q is also used by %s", id, others(subs, idx, i)) })
	}
	for _, d := range sortedKeys(mailOwner) {
		idx := mailOwner[d]
		conflict(idx, func(i int) error { return fmt.Errorf("mail domain %s is also listed by %s", d, others(subs, idx, i)) })
	}
	for _, k := range sortedKeys(dbOwner) {
		idx := dbOwner[k]
		typ, name, _ := strings.Cut(k, "/")
		conflict(idx, func(i int) error {
			return fmt.Errorf("database %s (%s) is also listed by %s", name, typ, others(subs, idx, i))
		})
	}
	for _, from := range sortedKeys(renamedFrom) {
		idx := renamedFrom[from]
		conflict(idx, func(i int) error {
			return fmt.Errorf("renamed_from %s is also given by %s", from, others(subs, idx, i))
		})
	}
	for a, i := range homes {
		for _, j := range homes[a+1:] {
			if within(subs[i].Home, subs[j].Home) || within(subs[j].Home, subs[i].Home) {
				add(i, fmt.Errorf("home %s and the home %s of %s are equal or nested", subs[i].Home, subs[j].Home, subs[j].Name))
				add(j, fmt.Errorf("home %s and the home %s of %s are equal or nested", subs[j].Home, subs[i].Home, subs[i].Name))
			}
		}
	}
	for i := range subs {
		s := subs[i]
		if len(errs[i]) > 0 {
			reasons := make([]string, len(errs[i]))
			for k, err := range errs[i] {
				reasons[k] = err.Error()
			}
			inv.Skipped = append(inv.Skipped, Skip{Name: s.Name, Reason: "invalid inventory entry: " + strings.Join(reasons, "; "), Invalid: true})
			continue
		}
		s.Features = orEmpty(s.Features)
		s.MailDomains = orEmpty(s.MailDomains)
		s.Files = orEmpty(s.Files)
		s.Databases = orEmpty(s.Databases)
		if len(s.FileExcludes) == 0 {
			s.FileExcludes = append([]string{}, DefaultFileExcludes...)
		}
		inv.Subscriptions = append(inv.Subscriptions, s)
		if s.RenamedFrom != "" {
			inv.Renamed = append(inv.Renamed, Rename{
				Subscription: s.Name, RenamedFrom: s.RenamedFrom,
				Note: fmt.Sprintf("renamed from %s: backups now go to a new repository for %s; the repository for %s is kept as it is and is no longer backed up, pruned or checked", s.RenamedFrom, s.Name, s.RenamedFrom),
			})
		}
	}
	var fatal []error
	if server != nil {
		server.Features = orEmpty(server.Features)
		if server.EncryptWithRepoKey == nil {
			on := true
			server.EncryptWithRepoKey = &on
		}
		fatal = append(fatal, validateServer(server)...)
	}
	if err := errors.Join(fatal...); err != nil {
		return Inventory{}, fmt.Errorf("inventory: %w", err)
	}
	return inv, nil
}

// Invalid returns the subscriptions validation left out (see validate).
func (inv Inventory) Invalid() []Skip {
	var out []Skip
	for _, s := range inv.Skipped {
		if s.Invalid {
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys(m map[string][]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func dedupe(v []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range v {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ValidDatabaseName reports whether name passes the inventory's database
// name rule and is not a server or panel database.
func ValidDatabaseName(name string) bool { return dbNameRE.MatchString(name) && !reservedDB(name) }

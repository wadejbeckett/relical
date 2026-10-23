// Package config loads Relical's root-only JSON configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Version is the configuration format this build reads.
const Version = 2

// ServerName is the repository name reserved for the panel's own
// configuration; it is never a subscription name.
const ServerName = "_server"

// Retention is how many snapshots of a tag group to keep.
type Retention struct {
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// Validate refuses a policy that would keep nothing.
func (r Retention) Validate() error {
	if r.Daily < 0 || r.Weekly < 0 || r.Monthly < 0 || r.Daily+r.Weekly+r.Monthly == 0 {
		return errors.New("retention must keep at least one snapshot")
	}
	return nil
}

// Destination is a restic repository address ({subscription} is expanded)
// plus the environment restic needs to reach it.
type Destination struct {
	Repository string            `json:"repository"`
	Env        map[string]string `json:"env"`
	Prune      string            `json:"prune"` // "local" or "remote"
}

// Roots are the directories inventory paths must sit under.
type Roots struct {
	Files []string `json:"files"`
	Mail  []string `json:"mail"`
}

type MySQL struct {
	DefaultsFile string `json:"defaults_file"`
}

type Config struct {
	Version            int                    `json:"version"`
	ServerID           string                 `json:"server_id"`
	ResticBin          string                 `json:"restic_bin"`
	DoveadmBin         string                 `json:"doveadm_bin"`
	StateDir           string                 `json:"state_dir"`
	KeysDir            string                 `json:"keys_dir"`
	MailboxPattern     string                 `json:"mailbox_pattern"`
	Roots              Roots                  `json:"roots"`
	MySQL              MySQL                  `json:"mysql"`
	DefaultDestination string                 `json:"default_destination"`
	Destinations       map[string]Destination `json:"destinations"`
	Retention          Retention              `json:"retention"`
	LimitUploadKiB     int                    `json:"limit_upload_kib"`
	LowPriority        bool                   `json:"low_priority"`

	// The inventory: either static (subscriptions, and optionally server)
	// or produced by inventory_command. Package inventory parses and
	// validates it.
	Subscriptions    json.RawMessage `json:"subscriptions"`
	Server           json.RawMessage `json:"server"`
	InventoryCommand []string        `json:"inventory_command"`
}

var domainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidDomain accepts lower-case (punycode) domain names only, so a domain
// can be used safely inside paths and repository names.
func ValidDomain(d string) bool { return len(d) <= 253 && domainRE.MatchString(d) }

// ValidRepoName accepts a subscription name or the reserved server name.
func ValidRepoName(n string) bool { return n == ServerName || ValidDomain(n) }

// present reports whether a raw JSON value was given (and is not null).
func present(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null"
}

// HasStaticInventory reports whether the config lists subscriptions itself.
func (c *Config) HasStaticInventory() bool { return present(c.Subscriptions) }

// HasServer reports whether the config has a server section.
func (c *Config) HasServer() bool { return present(c.Server) }

func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s: mode %04o is readable by others; chmod 600", path, st.Mode().Perm())
	}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if c.Version != Version {
		if c.Version == 0 {
			return nil, fmt.Errorf(`%s: "version": %d is required (this is a version 1 configuration or has no version; see the README for the version 2 format)`, path, Version)
		}
		return nil, fmt.Errorf("%s: unsupported config version %d (this build reads version %d)", path, c.Version, Version)
	}
	c.applyDefaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.ResticBin == "" {
		c.ResticBin = "restic"
	}
	if c.DoveadmBin == "" {
		c.DoveadmBin = "doveadm"
	}
	if c.Retention == (Retention{}) {
		c.Retention = Retention{Daily: 14, Weekly: 8, Monthly: 12}
	}
	if len(c.Roots.Files) == 0 {
		c.Roots.Files = []string{"/var/www/vhosts"}
	}
	if len(c.Roots.Mail) == 0 {
		c.Roots.Mail = []string{"/var/qmail/mailnames"}
	}
	if c.MailboxPattern == "" {
		c.MailboxPattern = "/var/qmail/mailnames/{domain}/{mailbox}/Maildir"
	}
}

// CleanAbs reports whether p is absolute, already clean and free of "..".
func CleanAbs(p string) bool {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

// Under reports whether p lies strictly inside one of roots.
func Under(p string, roots []string) bool {
	for _, r := range roots {
		if strings.HasPrefix(p, r+"/") {
			return true
		}
	}
	return false
}

func validRoots(field string, roots []string) []error {
	var errs []error
	for i, r := range roots {
		if !CleanAbs(r) || r == "/" {
			errs = append(errs, fmt.Errorf("roots.%s[%d] %q must be a clean absolute directory other than /", field, i, r))
		}
	}
	return errs
}

func (c *Config) Validate() error {
	var errs []error
	if c.ServerID == "" {
		errs = append(errs, errors.New("server_id is required"))
	}
	if !strings.HasPrefix(c.StateDir, "/") {
		errs = append(errs, errors.New("state_dir must be an absolute path"))
	}
	if !strings.HasPrefix(c.KeysDir, "/") {
		errs = append(errs, errors.New("keys_dir must be an absolute path"))
	}
	errs = append(errs, validRoots("files", c.Roots.Files)...)
	errs = append(errs, validRoots("mail", c.Roots.Mail)...)
	if strings.Count(c.MailboxPattern, "{domain}") != 1 || strings.Count(c.MailboxPattern, "{mailbox}") != 1 {
		errs = append(errs, errors.New("mailbox_pattern must contain {domain} and {mailbox} once each"))
	} else if !CleanAbs(c.MailboxPattern) || !Under(c.MailboxPattern, c.Roots.Mail) {
		errs = append(errs, fmt.Errorf("mailbox_pattern %q must be a clean absolute path under a mail root (%s)", c.MailboxPattern, strings.Join(c.Roots.Mail, ", ")))
	}
	if c.MySQL.DefaultsFile != "" && !CleanAbs(c.MySQL.DefaultsFile) {
		errs = append(errs, errors.New("mysql.defaults_file must be a clean absolute path"))
	}
	switch static, cmd := c.HasStaticInventory(), len(c.InventoryCommand) > 0; {
	case static && cmd:
		errs = append(errs, errors.New("give either subscriptions (a static inventory) or inventory_command, not both"))
	case !static && !cmd:
		errs = append(errs, errors.New("give either subscriptions (a static inventory) or inventory_command"))
	case cmd && !CleanAbs(c.InventoryCommand[0]):
		errs = append(errs, errors.New("inventory_command[0] must be a clean absolute path"))
	}
	if _, ok := c.Destinations[c.DefaultDestination]; !ok {
		errs = append(errs, fmt.Errorf("default_destination %q is not in destinations", c.DefaultDestination))
	}
	for name, dst := range c.Destinations {
		if strings.Contains(dst.Repository, "{domain}") {
			errs = append(errs, fmt.Errorf("destination %s: repository uses {domain}; config version 2 keeps one repository per subscription, so replace {domain} with {subscription}", name))
		} else if strings.Count(dst.Repository, "{subscription}") != 1 {
			errs = append(errs, fmt.Errorf("destination %s: repository must contain {subscription} once", name))
		}
		if dst.Prune != "local" && dst.Prune != "remote" {
			errs = append(errs, fmt.Errorf(`destination %s: prune must be "local" or "remote"`, name))
		}
	}
	if err := c.Retention.Validate(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// DestinationFor returns the named destination (the default when name is
// empty) with {subscription} expanded to repo, a subscription name or
// ServerName.
func (c *Config) DestinationFor(name, repo string) (string, Destination, error) {
	if !ValidRepoName(repo) {
		return "", Destination{}, fmt.Errorf("invalid subscription name %q", repo)
	}
	if name == "" {
		name = c.DefaultDestination
	}
	d, ok := c.Destinations[name]
	if !ok {
		return "", Destination{}, fmt.Errorf("destination %q is not defined", name)
	}
	d.Repository = strings.ReplaceAll(d.Repository, "{subscription}", repo)
	return name, d, nil
}

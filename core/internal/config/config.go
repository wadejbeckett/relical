// Package config loads Relical's root-only JSON configuration.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Retention is how many snapshots of the backup tag to keep.
type Retention struct {
	Daily   int `json:"daily"`
	Weekly  int `json:"weekly"`
	Monthly int `json:"monthly"`
}

// Destination is a restic repository address ({domain} is expanded) plus
// the environment restic needs to reach it.
type Destination struct {
	Repository string            `json:"repository"`
	Env        map[string]string `json:"env"`
	Prune      string            `json:"prune"` // "local" or "remote"
}

type Config struct {
	ServerID           string                 `json:"server_id"`
	ResticBin          string                 `json:"restic_bin"`
	DoveadmBin         string                 `json:"doveadm_bin"`
	StateDir           string                 `json:"state_dir"`
	KeysDir            string                 `json:"keys_dir"`
	MailboxPattern     string                 `json:"mailbox_pattern"`
	Domains            []string               `json:"domains"`
	DefaultDestination string                 `json:"default_destination"`
	DomainDestinations map[string]string      `json:"domain_destinations"`
	Destinations       map[string]Destination `json:"destinations"`
	Retention          Retention              `json:"retention"`
	LimitUploadKiB     int                    `json:"limit_upload_kib"`
	LowPriority        bool                   `json:"low_priority"`
}

var domainRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ValidDomain accepts lower-case (punycode) domain names only, so a domain
// can be used safely inside paths and repository names.
func ValidDomain(d string) bool { return len(d) <= 253 && domainRE.MatchString(d) }

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
	if strings.Count(c.MailboxPattern, "{domain}") != 1 || strings.Count(c.MailboxPattern, "{mailbox}") != 1 {
		errs = append(errs, errors.New("mailbox_pattern must contain {domain} and {mailbox} once each"))
	}
	for _, d := range c.Domains {
		if !ValidDomain(d) {
			errs = append(errs, fmt.Errorf("domain %q is not a valid lower-case domain name", d))
		}
	}
	if _, ok := c.Destinations[c.DefaultDestination]; !ok {
		errs = append(errs, fmt.Errorf("default_destination %q is not in destinations", c.DefaultDestination))
	}
	for d, name := range c.DomainDestinations {
		if _, ok := c.Destinations[name]; !ok {
			errs = append(errs, fmt.Errorf("domain_destinations[%s]: destination %q is not defined", d, name))
		}
	}
	for name, dst := range c.Destinations {
		if strings.Count(dst.Repository, "{domain}") != 1 {
			errs = append(errs, fmt.Errorf("destination %s: repository must contain {domain} once", name))
		}
		if dst.Prune != "local" && dst.Prune != "remote" {
			errs = append(errs, fmt.Errorf(`destination %s: prune must be "local" or "remote"`, name))
		}
	}
	r := c.Retention
	if r.Daily < 0 || r.Weekly < 0 || r.Monthly < 0 || r.Daily+r.Weekly+r.Monthly == 0 {
		errs = append(errs, errors.New("retention must keep at least one snapshot"))
	}
	return errors.Join(errs...)
}

// Enabled reports whether domain is listed in domains.
func (c *Config) Enabled(domain string) bool {
	for _, d := range c.Domains {
		if d == domain {
			return true
		}
	}
	return false
}

// DestinationFor returns the destination name and the destination for
// domain, with {domain} expanded in its repository.
func (c *Config) DestinationFor(domain string) (string, Destination, error) {
	if !ValidDomain(domain) {
		return "", Destination{}, fmt.Errorf("invalid domain %q", domain)
	}
	name := c.DefaultDestination
	if n, ok := c.DomainDestinations[domain]; ok {
		name = n
	}
	d := c.Destinations[name]
	d.Repository = strings.ReplaceAll(d.Repository, "{domain}", domain)
	return name, d, nil
}

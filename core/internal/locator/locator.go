// Package locator finds a domain's mailboxes from a path pattern such as
// /var/qmail/mailnames/{domain}/{mailbox}/Maildir (Plesk).
package locator

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/wadejbeckett/relical/core/internal/config"
)

type Mailbox struct {
	Domain string `json:"domain"`
	Name   string `json:"mailbox"`
	Login  string `json:"login"`
	Path   string `json:"path"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
}

// Pattern contains {domain} and {mailbox} once each; {mailbox} is a whole
// path component.
type Pattern string

func (p Pattern) split(domain string) (prefix, suffix string, err error) {
	if !config.ValidDomain(domain) {
		return "", "", fmt.Errorf("invalid domain %q", domain)
	}
	t := strings.ReplaceAll(string(p), "{domain}", domain)
	i := strings.Index(t, "{mailbox}")
	if i < 0 {
		return "", "", errors.New("mailbox pattern has no {mailbox}")
	}
	prefix, suffix = t[:i], t[i+len("{mailbox}"):]
	if !strings.HasSuffix(prefix, "/") || (suffix != "" && !strings.HasPrefix(suffix, "/")) {
		return "", "", errors.New("{mailbox} must be a whole path component")
	}
	return prefix, suffix, nil
}

func validMailbox(name string) bool {
	return name != "" && !strings.HasPrefix(name, ".") && !strings.HasPrefix(name, "@") && !strings.Contains(name, "/")
}

// mailbox returns the mailbox if prefix+name+suffix is a real directory.
// Symlinks are never followed, at either level.
func mailbox(domain, name, prefix, suffix string) (Mailbox, bool) {
	if fi, err := os.Lstat(prefix + name); err != nil || !fi.IsDir() {
		return Mailbox{}, false
	}
	path := prefix + name + suffix
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() {
		return Mailbox{}, false
	}
	st := fi.Sys().(*syscall.Stat_t)
	return Mailbox{Domain: domain, Name: name, Login: name + "@" + domain, Path: path, UID: int(st.Uid), GID: int(st.Gid)}, true
}

// Mailboxes lists the domain's mailboxes, sorted by name.
func (p Pattern) Mailboxes(domain string) ([]Mailbox, error) {
	prefix, suffix, err := p.split(domain)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(prefix)
	if err != nil {
		return nil, err
	}
	out := []Mailbox{}
	for _, e := range entries {
		if !validMailbox(e.Name()) {
			continue
		}
		if m, ok := mailbox(domain, e.Name(), prefix, suffix); ok {
			out = append(out, m)
		}
	}
	return out, nil
}

// Find returns one mailbox by its local part.
func (p Pattern) Find(domain, name string) (Mailbox, error) {
	prefix, suffix, err := p.split(domain)
	if err != nil {
		return Mailbox{}, err
	}
	if !validMailbox(name) || name == ".." {
		return Mailbox{}, fmt.Errorf("invalid mailbox %q", name)
	}
	m, ok := mailbox(domain, name, prefix, suffix)
	if !ok {
		return Mailbox{}, fmt.Errorf("mailbox %s@%s not found", name, domain)
	}
	return m, nil
}

// Package keys manages one random restic repository password per
// repository: one per subscription, plus one for the panel configuration
// (config.ServerName). Losing a key loses that repository's backups:
// escrow copies are an operational duty (SCOPE §6).
package keys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wadejbeckett/relical/core/internal/config"
)

func checkDir(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be a directory with mode 0700", dir)
	}
	return nil
}

func keyPath(dir, name string) (string, error) {
	if !config.ValidRepoName(name) {
		return "", fmt.Errorf("invalid subscription name %q", name)
	}
	if err := checkDir(dir); err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".key"), nil
}

func checkFile(p string, fi fs.FileInfo) error {
	if fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s is readable by others; chmod 600", p)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%s is empty", p)
	}
	return nil
}

// ErrNoKey is returned by Path when the repository has no key file yet,
// which means nothing has been backed up into it.
var ErrNoKey = errors.New("no key file")

// Path returns the existing key file for a subscription.
func Path(dir, name string) (string, error) {
	p, err := keyPath(dir, name)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("no key for %s: %w (%s)", name, ErrNoKey, p)
	}
	if err != nil {
		return "", fmt.Errorf("no key for %s: %w", name, err)
	}
	return p, checkFile(p, fi)
}

// Ensure returns the key file for a subscription, creating a new random
// key if none exists. It never replaces an existing key.
func Ensure(dir, name string) (string, bool, error) {
	p, err := keyPath(dir, name)
	if err != nil {
		return "", false, err
	}
	if fi, err := os.Stat(p); err == nil {
		return p, false, checkFile(p, fi)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", false, err
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false, err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", false, err
	}
	if _, err := f.WriteString(hex.EncodeToString(buf) + "\n"); err != nil {
		f.Close()
		os.Remove(p)
		return "", false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(p)
		return "", false, err
	}
	return p, true, f.Close()
}

// The owner file <dir>/<name>.id records the panel id of the subscription
// a repository was first backed up for. A subscription name can be reused
// (a deleted subscription's name taken by a new customer, or a rename onto
// an old name); the owner check stops the new subscription from using, and
// restoring from, the old owner's repository.

func ownerPath(dir, name string) (string, error) {
	p, err := keyPath(dir, name)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(p, ".key") + ".id", nil
}

// CheckOwner refuses when the repository's recorded owner id differs from
// id. A repository without an owner file passes.
func CheckOwner(dir, name, id string) error {
	p, err := ownerPath(dir, name)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	rec := strings.TrimSpace(string(b))
	if rec == id {
		return nil
	}
	given := fmt.Sprintf("id %q", id)
	if id == "" {
		given = "no id"
	}
	kp := strings.TrimSuffix(p, ".id") + ".key"
	return fmt.Errorf("the repository for %s belongs to subscription id %q (recorded in %s), but the inventory gives %s: "+
		"the name was reused or another subscription was renamed onto it. To start a new repository for this subscription, "+
		"move %s, %s and the repository itself aside; relical never deletes them", name, rec, p, given, kp, p)
}

// HasOwner reports whether the repository has an owner file.
func HasOwner(dir, name string) (bool, error) {
	p, err := ownerPath(dir, name)
	if err != nil {
		return false, err
	}
	_, err = os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// RecordOwner records id as the repository's owner if no owner is recorded
// yet, and otherwise checks it. An empty id (a static inventory without
// ids, or the panel configuration) records nothing.
func RecordOwner(dir, name, id string) error {
	if err := CheckOwner(dir, name, id); err != nil || id == "" {
		return err
	}
	p, err := ownerPath(dir, name)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return CheckOwner(dir, name, id)
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(id + "\n"); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(p)
		return err
	}
	return f.Close()
}

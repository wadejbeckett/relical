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

// Path returns the existing key file for a subscription.
func Path(dir, name string) (string, error) {
	p, err := keyPath(dir, name)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(p)
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

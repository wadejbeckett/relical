package keys

import (
	"os"
	"path/filepath"
	"testing"
)

func keyDir(t *testing.T) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "keys")
	if err := os.Mkdir(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestEnsureCreatesOnceThenReuses(t *testing.T) {
	d := keyDir(t)
	p, created, err := Ensure(d, "example.test")
	if err != nil || !created {
		t.Fatalf("first: %v %v", created, err)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 || st.Size() != 65 {
		t.Errorf("mode %v size %d", st.Mode().Perm(), st.Size())
	}
	first, _ := os.ReadFile(p)
	_, created, err = Ensure(d, "example.test")
	second, _ := os.ReadFile(p)
	if err != nil || created || string(first) != string(second) {
		t.Fatalf("second call changed the key: created=%v err=%v", created, err)
	}
}

func TestEnsureRefusesOpenDir(t *testing.T) {
	d := keyDir(t)
	os.Chmod(d, 0o755)
	if _, _, err := Ensure(d, "example.test"); err == nil {
		t.Fatal("0755 keys dir accepted")
	}
}

func TestEnsureRefusesBadDomain(t *testing.T) {
	if _, _, err := Ensure(keyDir(t), "../x"); err == nil {
		t.Fatal("bad domain accepted")
	}
}

func TestPathRequiresExistingKey(t *testing.T) {
	d := keyDir(t)
	if _, err := Path(d, "example.test"); err == nil {
		t.Fatal("missing key accepted")
	}
	Ensure(d, "example.test")
	if p, err := Path(d, "example.test"); err != nil || filepath.Base(p) != "example.test.key" {
		t.Fatalf("%s %v", p, err)
	}
}

func TestPathRefusesReadableKey(t *testing.T) {
	d := keyDir(t)
	p, _, _ := Ensure(d, "example.test")
	os.Chmod(p, 0o644)
	if _, err := Path(d, "example.test"); err == nil {
		t.Fatal("0644 key accepted")
	}
}

package crypto

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrGenerate_GeneratesOnFirstRun(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "nested", "server.key")
	pubPath := filepath.Join(dir, "nested", "server.pub")

	e, generated, err := LoadOrGenerate(privPath, pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if !generated {
		t.Fatal("expected generated=true on first run")
	}
	if _, err := os.Stat(privPath); err != nil {
		t.Fatalf("private key not written: %v", err)
	}
	if _, err := os.Stat(pubPath); err != nil {
		t.Fatalf("public key not written: %v", err)
	}

	info, _ := os.Stat(privPath)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("private key mode = %o, want 0600", info.Mode().Perm())
	}

	if e.PublicKey() == (Key{}) {
		t.Fatal("encryptor public key is zero")
	}
}

func TestLoadOrGenerate_ReusesExistingKeys(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")

	first, _, err := LoadOrGenerate(privPath, pubPath)
	if err != nil {
		t.Fatal(err)
	}
	second, generated, err := LoadOrGenerate(privPath, pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if generated {
		t.Fatal("expected generated=false on second run")
	}
	if first.PublicKey() != second.PublicKey() {
		t.Fatal("public key changed between runs")
	}
}

func TestLoadOrGenerate_RequiresBothPaths(t *testing.T) {
	if _, _, err := LoadOrGenerate("", "foo"); err == nil {
		t.Fatal("expected error when private path is empty")
	}
	if _, _, err := LoadOrGenerate("foo", ""); err == nil {
		t.Fatal("expected error when public path is empty")
	}
}

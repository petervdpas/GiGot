package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func newSealedFile(t *testing.T) (*SealedFile, *Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "data.enc")
	priv, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := New(priv)
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewSealedFile(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	return f, enc, path
}

func TestSealedFile_LoadMissingReturnsNil(t *testing.T) {
	f, _, _ := newSealedFile(t)
	got, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing file, got %d bytes", len(got))
	}
}

func TestSealedFile_RoundTrip(t *testing.T) {
	f, _, _ := newSealedFile(t)
	payload := []byte(`{"hello":"world"}`)

	if err := f.Save(payload); err != nil {
		t.Fatal(err)
	}
	got, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q, want %q", got, payload)
	}
}

func TestSealedFile_FileIsActuallySealed(t *testing.T) {
	f, _, path := newSealedFile(t)
	plain := []byte("supersecret-token-material")
	if err := f.Save(plain); err != nil {
		t.Fatal(err)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(onDisk, plain) {
		t.Fatal("plaintext leaked into the on-disk file")
	}
}

func TestSealedFile_AtomicWriteLeavesNoTemp(t *testing.T) {
	f, _, path := newSealedFile(t)
	if err := f.Save([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected .tmp cleanup, got err=%v", err)
	}
}

func TestSealedFile_DifferentEncryptorCannotLoad(t *testing.T) {
	f, _, path := newSealedFile(t)
	_ = f.Save([]byte("secret"))

	otherPriv, _, _ := GenerateKeyPair()
	otherEnc, _ := New(otherPriv)
	other, _ := NewSealedFile(path, otherEnc)
	if _, err := other.Load(); err == nil {
		t.Fatal("expected decryption to fail with a different keypair")
	}
}

func TestSealedFile_EmptyFileTreatedAsMissing(t *testing.T) {
	f, _, path := newSealedFile(t)
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := f.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil for empty file, got %d bytes", len(got))
	}
}

func TestNewSealedFile_RejectsEmptyPath(t *testing.T) {
	priv, _, _ := GenerateKeyPair()
	enc, _ := New(priv)
	if _, err := NewSealedFile("", enc); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestNewSealedFile_RejectsNilEncryptor(t *testing.T) {
	if _, err := NewSealedFile("/tmp/x", nil); err == nil {
		t.Fatal("expected error for nil encryptor")
	}
}

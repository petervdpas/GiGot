package crypto

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func seedKeypairAndStore(t *testing.T, dir string, payload []byte) (priv Key, storePath string) {
	t.Helper()
	privPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")
	enc, _, err := LoadOrGenerate(privPath, pubPath)
	if err != nil {
		t.Fatal(err)
	}
	storePath = filepath.Join(dir, "state.enc")
	sf, err := NewSealedFile(storePath, enc)
	if err != nil {
		t.Fatal(err)
	}
	if err := sf.Save(payload); err != nil {
		t.Fatal(err)
	}
	return enc.PublicKey(), storePath // priv is named but we don't expose it — caller uses paths
}

func TestRotate_SwapsKeypairAndRewrapsStores(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")

	payload := []byte(`{"admin":"peter"}`)
	oldPub, storePath := seedKeypairAndStore(t, dir, payload)

	result, err := Rotate(privPath, pubPath, []string{storePath})
	if err != nil {
		t.Fatal(err)
	}

	if result.OldPublicKey != oldPub {
		t.Fatal("result should record the previous public key")
	}
	if result.NewPublicKey == oldPub {
		t.Fatal("new public key must differ from old")
	}
	if len(result.Rewrapped) != 1 || result.Rewrapped[0] != storePath {
		t.Fatalf("unexpected rewrapped list: %+v", result.Rewrapped)
	}

	// The new on-disk keypair must open the rewrapped store.
	newEnc, _, err := LoadOrGenerate(privPath, pubPath)
	if err != nil {
		t.Fatal(err)
	}
	if newEnc.PublicKey() != result.NewPublicKey {
		t.Fatal("loaded key does not match rotation result")
	}
	sf, _ := NewSealedFile(storePath, newEnc)
	got, err := sf.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload lost across rotation: %q", got)
	}
}

func TestRotate_BackupsExist(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")
	_, storePath := seedKeypairAndStore(t, dir, []byte("payload"))

	result, err := Rotate(privPath, pubPath, []string{storePath})
	if err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{privPath, pubPath, storePath} {
		bak := p + ".bak." + result.BackupSuffix
		if _, err := os.Stat(bak); err != nil {
			t.Fatalf("missing backup %s: %v", bak, err)
		}
	}
}

func TestRotate_MissingStoreIsSkipped(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")
	// Seed a keypair but do not write the sealed file.
	if _, _, err := LoadOrGenerate(privPath, pubPath); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(dir, "never-created.enc")
	result, err := Rotate(privPath, pubPath, []string{missing})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rewrapped) != 0 {
		t.Fatalf("expected no rewrap for missing file, got %+v", result.Rewrapped)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("Rotate must not create a missing sealed file")
	}
}

func TestRotate_RefusesWhenNoExistingKeypair(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")

	_, err := Rotate(privPath, pubPath, nil)
	if err == nil {
		t.Fatal("Rotate should refuse when no existing keypair exists")
	}
}

func TestRotate_OldKeyCannotOpenAfterRotation(t *testing.T) {
	dir := t.TempDir()
	privPath := filepath.Join(dir, "server.key")
	pubPath := filepath.Join(dir, "server.pub")
	_, storePath := seedKeypairAndStore(t, dir, []byte("secret"))

	// Remember the old privkey.
	oldEnc, _, _ := LoadOrGenerate(privPath, pubPath)

	if _, err := Rotate(privPath, pubPath, []string{storePath}); err != nil {
		t.Fatal(err)
	}

	// Old encryptor must fail to open the rewrapped store.
	stale, _ := NewSealedFile(storePath, oldEnc)
	if _, err := stale.Load(); err == nil {
		t.Fatal("old key should not be able to open the rewrapped store")
	}
}

func TestDefaultSealedFiles(t *testing.T) {
	got := DefaultSealedFiles("/var/lib/gigot/data")
	want := []string{
		"/var/lib/gigot/data/admins.enc",
		"/var/lib/gigot/data/clients.enc",
		"/var/lib/gigot/data/credentials.enc",
		"/var/lib/gigot/data/destinations.enc",
		"/var/lib/gigot/data/sessions.enc",
		"/var/lib/gigot/data/tokens.enc",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d files, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("got[%d]=%q, want %q", i, got[i], w)
		}
	}
}

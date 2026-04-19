package crypto

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// RotateResult reports what a Rotate call produced.
type RotateResult struct {
	OldPublicKey Key
	NewPublicKey Key
	BackupSuffix string   // suffix appended to .bak files (e.g. "20260415-103000")
	Rewrapped    []string // absolute paths of files re-sealed with the new key
}

// Rotate generates a fresh keypair, re-encrypts each sealed file under the
// new key, and swaps the keypair on disk. The previous keypair and every
// rewrapped file are preserved as .bak.{timestamp} alongside the originals so
// a failed rotation can be reverted by hand.
//
// The server must be stopped for the duration of this call: concurrent
// requests would see inconsistent on-disk state.
//
// privPath / pubPath point at the server keypair. sealedFiles is the list of
// paths whose contents are sealed to the server pubkey. Missing sealed files
// are skipped (nothing to rewrap).
func Rotate(privPath, pubPath string, sealedFiles []string) (*RotateResult, error) {
	if privPath == "" || pubPath == "" {
		return nil, fmt.Errorf("crypto: rotate needs both key paths")
	}

	oldEnc, generated, err := LoadOrGenerate(privPath, pubPath)
	if err != nil {
		return nil, fmt.Errorf("crypto: load current keypair: %w", err)
	}
	if generated {
		// A rotation that had to generate the first keypair is effectively a
		// no-op — there's nothing sealed under a previous key. Surface that.
		return nil, fmt.Errorf("crypto: no existing keypair to rotate from (one was just generated at %s)", privPath)
	}

	// Decrypt all stores with the old key before touching anything on disk.
	type payload struct {
		path  string
		plain []byte
	}
	var decrypted []payload
	for _, p := range sealedFiles {
		sf, err := NewSealedFile(p, oldEnc)
		if err != nil {
			return nil, fmt.Errorf("crypto: open %s: %w", p, err)
		}
		plain, err := sf.Load()
		if err != nil {
			return nil, fmt.Errorf("crypto: decrypt %s (old key may have already been replaced): %w", p, err)
		}
		if plain == nil {
			continue
		}
		decrypted = append(decrypted, payload{path: p, plain: plain})
	}

	// Generate the new keypair in memory — not written until the old one is
	// safely backed up.
	newPriv, newPub, err := GenerateKeyPair()
	if err != nil {
		return nil, fmt.Errorf("crypto: generate new keypair: %w", err)
	}
	newEnc, err := New(newPriv)
	if err != nil {
		return nil, err
	}

	suffix := time.Now().UTC().Format("20060102-150405")
	result := &RotateResult{
		OldPublicKey: oldEnc.PublicKey(),
		NewPublicKey: newEnc.PublicKey(),
		BackupSuffix: suffix,
	}

	// Back up and swap the keypair first so the new key is what the server
	// would load on restart.
	if err := backupAndReplaceKey(privPath, newPriv, 0600, suffix); err != nil {
		return nil, err
	}
	if err := backupAndReplaceKey(pubPath, newPub, 0644, suffix); err != nil {
		return nil, fmt.Errorf("crypto: write new pubkey (privkey already rotated — manual recovery needed): %w", err)
	}

	// Now rewrap every sealed file. If any one fails, we stop and leave the
	// rest for the operator to investigate via the .bak files.
	for _, d := range decrypted {
		backup := d.path + ".bak." + suffix
		if err := copyFile(d.path, backup); err != nil {
			return result, fmt.Errorf("crypto: backup %s: %w", d.path, err)
		}
		dst, err := NewSealedFile(d.path, newEnc)
		if err != nil {
			return result, fmt.Errorf("crypto: open %s for rewrap: %w", d.path, err)
		}
		if err := dst.Save(d.plain); err != nil {
			return result, fmt.Errorf("crypto: rewrap %s: %w", d.path, err)
		}
		result.Rewrapped = append(result.Rewrapped, d.path)
	}

	return result, nil
}

func backupAndReplaceKey(path string, k Key, mode os.FileMode, suffix string) error {
	if _, err := os.Stat(path); err == nil {
		if err := copyFile(path, path+".bak."+suffix); err != nil {
			return fmt.Errorf("crypto: backup %s: %w", path, err)
		}
	}
	tmp := path + ".new"
	if err := os.WriteFile(tmp, []byte(k.Encode()+"\n"), mode); err != nil {
		return fmt.Errorf("crypto: write %s: %w", tmp, err)
	}
	return os.Rename(tmp, path)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0600)
}

// DefaultSealedFiles returns the standard set of sealed files Rotate should
// rewrap given a data directory. Consumers that add new sealed stores should
// add to this list.
func DefaultSealedFiles(dataDir string) []string {
	return []string{
		filepath.Join(dataDir, "admins.enc"),
		filepath.Join(dataDir, "clients.enc"),
		filepath.Join(dataDir, "credentials.enc"),
		filepath.Join(dataDir, "destinations.enc"),
		filepath.Join(dataDir, "tokens.enc"),
	}
}

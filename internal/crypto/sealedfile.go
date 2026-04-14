package crypto

import (
	"fmt"
	"os"
	"path/filepath"
)

// SealedFile is a file whose contents are NaCl-sealed to an Encryptor's own
// public key. Callers hand in plaintext bytes; SealedFile handles encryption,
// atomic writes, and the missing-file case (returned as nil, nil).
//
// This exists to keep clients, tokens, admins, and any future stores from
// re-implementing the same open → decrypt / encrypt → atomic-rename dance.
type SealedFile struct {
	path string
	enc  *Encryptor
}

// NewSealedFile prepares a SealedFile at path, creating the parent directory
// with 0700 permissions if needed.
func NewSealedFile(path string, enc *Encryptor) (*SealedFile, error) {
	if path == "" {
		return nil, fmt.Errorf("crypto: sealed file path required")
	}
	if enc == nil {
		return nil, fmt.Errorf("crypto: sealed file encryptor required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("crypto: mkdir sealed file: %w", err)
	}
	return &SealedFile{path: path, enc: enc}, nil
}

// Path returns the on-disk location.
func (f *SealedFile) Path() string { return f.path }

// Load reads and decrypts the file. A missing or zero-byte file returns
// (nil, nil) so callers can treat it as "no data yet".
func (f *SealedFile) Load() ([]byte, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("crypto: read %s: %w", f.path, err)
	}
	if len(data) == 0 {
		return nil, nil
	}
	plain, err := f.enc.OpenSelf(data)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt %s: %w", f.path, err)
	}
	return plain, nil
}

// Rewrap re-encrypts a sealed file from one Encryptor's key to another's,
// atomically. Used by the key-rotation routine: decrypt every store with the
// old key, re-encrypt with the new one, swap. Missing or empty source files
// are a no-op (nothing to rewrap).
func Rewrap(from, to *Encryptor, path string) error {
	src, err := NewSealedFile(path, from)
	if err != nil {
		return err
	}
	plain, err := src.Load()
	if err != nil {
		return err
	}
	if plain == nil {
		return nil
	}
	dst, err := NewSealedFile(path, to)
	if err != nil {
		return err
	}
	return dst.Save(plain)
}

// Save seals plaintext to the Encryptor's own public key and atomically
// writes the result to the file with 0600 permissions.
func (f *SealedFile) Save(plaintext []byte) error {
	sealed, err := f.enc.SealSelf(plaintext)
	if err != nil {
		return fmt.Errorf("crypto: seal %s: %w", f.path, err)
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, sealed, 0600); err != nil {
		return fmt.Errorf("crypto: write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, f.path); err != nil {
		return fmt.Errorf("crypto: rename %s: %w", tmp, err)
	}
	return nil
}

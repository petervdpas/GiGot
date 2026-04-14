package crypto

import (
	"fmt"
	"os"
	"path/filepath"
)

// LoadOrGenerate loads a keypair from the given file paths, generating and
// writing one if either file is missing. The private key file is written 0600,
// the public key file 0644. The containing directories are created as needed.
//
// Returns the Encryptor ready to use and a boolean indicating whether a new
// pair was generated (true) or an existing one loaded (false).
func LoadOrGenerate(privPath, pubPath string) (*Encryptor, bool, error) {
	if privPath == "" || pubPath == "" {
		return nil, false, fmt.Errorf("crypto: private/public key paths required")
	}

	if fileExists(privPath) {
		priv, err := readKeyFile(privPath)
		if err != nil {
			return nil, false, fmt.Errorf("crypto: read private key: %w", err)
		}
		e, err := New(priv)
		if err != nil {
			return nil, false, err
		}
		if !fileExists(pubPath) {
			if err := writeKeyFile(pubPath, e.PublicKey(), 0644); err != nil {
				return nil, false, fmt.Errorf("crypto: write public key: %w", err)
			}
		}
		return e, false, nil
	}

	priv, pub, err := GenerateKeyPair()
	if err != nil {
		return nil, false, err
	}
	if err := os.MkdirAll(filepath.Dir(privPath), 0700); err != nil {
		return nil, false, fmt.Errorf("crypto: mkdir for private key: %w", err)
	}
	if err := writeKeyFile(privPath, priv, 0600); err != nil {
		return nil, false, fmt.Errorf("crypto: write private key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pubPath), 0755); err != nil {
		return nil, false, fmt.Errorf("crypto: mkdir for public key: %w", err)
	}
	if err := writeKeyFile(pubPath, pub, 0644); err != nil {
		return nil, false, fmt.Errorf("crypto: write public key: %w", err)
	}
	e, err := New(priv)
	if err != nil {
		return nil, false, err
	}
	return e, true, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func readKeyFile(p string) (Key, error) {
	var zero Key
	data, err := os.ReadFile(p)
	if err != nil {
		return zero, err
	}
	// Trim trailing whitespace/newlines users or editors may have added.
	s := string(data)
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return DecodeKey(s)
}

func writeKeyFile(p string, k Key, mode os.FileMode) error {
	return os.WriteFile(p, []byte(k.Encode()+"\n"), mode)
}

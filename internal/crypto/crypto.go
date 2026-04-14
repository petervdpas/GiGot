// Package crypto provides NaCl box encryption for sealed request/response
// bodies and at-rest storage. It is a leaf package — zero imports from other
// internal packages. The server holds its own keypair; recipient/sender public
// keys are passed in by callers that already resolved them from their stores.
package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/nacl/box"
)

const (
	KeySize   = 32
	NonceSize = 24
)

var (
	ErrBadKey     = errors.New("crypto: key must be 32 bytes")
	ErrBadPayload = errors.New("crypto: payload too short")
	ErrDecrypt    = errors.New("crypto: decryption failed")
)

// Key is a 32-byte NaCl key (public or private).
type Key [KeySize]byte

// Encode returns the base64 form.
func (k Key) Encode() string { return base64.StdEncoding.EncodeToString(k[:]) }

// DecodeKey parses a base64-encoded key.
func DecodeKey(s string) (Key, error) {
	var k Key
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return k, fmt.Errorf("crypto: decode key: %w", err)
	}
	if len(raw) != KeySize {
		return k, ErrBadKey
	}
	copy(k[:], raw)
	return k, nil
}

// GenerateKeyPair creates a new curve25519 keypair.
func GenerateKeyPair() (priv, pub Key, err error) {
	pubPtr, privPtr, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return priv, pub, fmt.Errorf("crypto: generate keypair: %w", err)
	}
	copy(priv[:], privPtr[:])
	copy(pub[:], pubPtr[:])
	return priv, pub, nil
}

// Encryptor seals and opens NaCl box messages using a fixed private key.
type Encryptor struct {
	priv Key
	pub  Key
}

// New builds an Encryptor from a private key. The public key is derived from it.
func New(priv Key) (*Encryptor, error) {
	pub, err := derivePublic(priv)
	if err != nil {
		return nil, err
	}
	return &Encryptor{priv: priv, pub: pub}, nil
}

func derivePublic(priv Key) (Key, error) {
	var pub Key
	raw, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		return pub, fmt.Errorf("crypto: derive public: %w", err)
	}
	copy(pub[:], raw)
	return pub, nil
}

// PublicKey returns the Encryptor's public key.
func (e *Encryptor) PublicKey() Key { return e.pub }

// Seal encrypts plaintext for the given recipient. Output is nonce||ciphertext.
func (e *Encryptor) Seal(recipient Key, plaintext []byte) ([]byte, error) {
	var nonce [NonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("crypto: nonce: %w", err)
	}
	var peer [KeySize]byte
	copy(peer[:], recipient[:])
	var priv [KeySize]byte
	copy(priv[:], e.priv[:])

	out := box.Seal(nonce[:], plaintext, &nonce, &peer, &priv)
	return out, nil
}

// SealString is Seal with base64 output.
func (e *Encryptor) SealString(recipient Key, plaintext []byte) (string, error) {
	raw, err := e.Seal(recipient, plaintext)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// Open decrypts nonce||ciphertext sent by the given sender.
func (e *Encryptor) Open(sender Key, sealed []byte) ([]byte, error) {
	if len(sealed) < NonceSize+box.Overhead {
		return nil, ErrBadPayload
	}
	var nonce [NonceSize]byte
	copy(nonce[:], sealed[:NonceSize])
	var peer [KeySize]byte
	copy(peer[:], sender[:])
	var priv [KeySize]byte
	copy(priv[:], e.priv[:])

	plaintext, ok := box.Open(nil, sealed[NonceSize:], &nonce, &peer, &priv)
	if !ok {
		return nil, ErrDecrypt
	}
	return plaintext, nil
}

// OpenString is Open with base64 input.
func (e *Encryptor) OpenString(sender Key, b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("crypto: decode sealed: %w", err)
	}
	return e.Open(sender, raw)
}

// SealSelf encrypts plaintext to the Encryptor's own public key (for at-rest
// storage the server itself can later open).
func (e *Encryptor) SealSelf(plaintext []byte) ([]byte, error) {
	return e.Seal(e.pub, plaintext)
}

// OpenSelf decrypts a SealSelf payload.
func (e *Encryptor) OpenSelf(sealed []byte) ([]byte, error) {
	return e.Open(e.pub, sealed)
}

// Package admins stores administrator accounts with bcrypt-hashed passwords.
// The on-disk file is sealed to the server's own public key.
package admins

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrNotFound        = errors.New("admins: not found")
	ErrInvalidPassword = errors.New("admins: invalid password")
)

// Admin is a single administrator account.
type Admin struct {
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Roles        []string  `json:"roles"`
	CreatedAt    time.Time `json:"created_at"`
}

// Store holds admin accounts, persisted to an encrypted file on disk.
type Store struct {
	file *crypto.SealedFile

	mu     sync.RWMutex
	admins map[string]*Admin
}

// Open loads (or initialises) the admin store at path, sealed to enc.
func Open(path string, enc *crypto.Encryptor) (*Store, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("admins: %w", err)
	}
	s := &Store{file: f, admins: make(map[string]*Admin)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	plain, err := s.file.Load()
	if err != nil {
		return fmt.Errorf("admins: %w", err)
	}
	if plain == nil {
		return nil
	}
	var list []*Admin
	if err := json.Unmarshal(plain, &list); err != nil {
		return fmt.Errorf("admins: parse: %w", err)
	}
	for _, a := range list {
		s.admins[a.Username] = a
	}
	return nil
}

func (s *Store) persist() error {
	list := make([]*Admin, 0, len(s.admins))
	for _, a := range s.admins {
		list = append(list, a)
	}
	plain, err := json.Marshal(list)
	if err != nil {
		return fmt.Errorf("admins: marshal: %w", err)
	}
	return s.file.Save(plain)
}

// Put creates or overwrites an admin. Password is hashed with bcrypt.
func (s *Store) Put(username, password string, roles []string) (*Admin, error) {
	if username == "" {
		return nil, fmt.Errorf("admins: username required")
	}
	if password == "" {
		return nil, fmt.Errorf("admins: password required")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("admins: hash: %w", err)
	}
	a := &Admin{
		Username:     username,
		PasswordHash: string(hash),
		Roles:        roles,
		CreatedAt:    time.Now().UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.admins[username] = a
	if err := s.persist(); err != nil {
		delete(s.admins, username)
		return nil, err
	}
	return a, nil
}

// Verify checks a username/password pair and returns the Admin on success.
func (s *Store) Verify(username, password string) (*Admin, error) {
	s.mu.RLock()
	a, ok := s.admins[username]
	s.mu.RUnlock()
	if !ok {
		// Run bcrypt against a dummy hash so timing doesn't leak which usernames
		// exist.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$usesomesilentstringforleakproof."), []byte(password))
		return nil, ErrNotFound
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidPassword
	}
	return a, nil
}

// Remove deletes an admin.
func (s *Store) Remove(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.admins[username]; !ok {
		return ErrNotFound
	}
	delete(s.admins, username)
	return s.persist()
}

// Count returns the number of stored admins.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.admins)
}

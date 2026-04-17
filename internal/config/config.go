package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the top-level GiGot configuration.
type Config struct {
	Server  ServerConfig  `json:"server"`
	Storage StorageConfig `json:"storage"`
	Auth    AuthConfig    `json:"auth"`
	Crypto  CryptoConfig  `json:"crypto"`
	Logging LoggingConfig `json:"logging"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// FormidableFirst flips the deployment-level stamping default per
	// docs/design/structured-sync-api.md §2.7. When true, both `POST
	// /api/repos` init and clone stamp .formidable/context.json by
	// default (clones idempotently). When false (the default), stamping
	// is strictly opt-in per request via scaffold_formidable: true.
	FormidableFirst bool `json:"formidable_first"`
}

// StorageConfig controls where repositories are kept.
type StorageConfig struct {
	RepoRoot string `json:"repo_root"`
}

// AuthConfig controls authentication.
type AuthConfig struct {
	Enabled bool   `json:"enabled"`
	Type    string `json:"type"`
}

// CryptoConfig controls server-side NaCl keypair storage and on-disk layout
// for encrypted data (client enrollments, token store).
type CryptoConfig struct {
	// PrivateKeyPath is where the server's private key is stored (base64, 0600).
	// Generated on first run if missing.
	PrivateKeyPath string `json:"private_key_path"`
	// PublicKeyPath is where the server's public key is stored (base64).
	PublicKeyPath string `json:"public_key_path"`
	// DataDir holds encrypted state files (clients.enc, tokens.enc, admins.json).
	DataDir string `json:"data_dir"`
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	Level string `json:"level"`
}

// Defaults returns a Config with sensible defaults.
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "127.0.0.1",
			Port: 3417,
		},
		Storage: StorageConfig{
			RepoRoot: "./repos",
		},
		Auth: AuthConfig{
			Enabled: false,
			Type:    "token",
		},
		Crypto: CryptoConfig{
			PrivateKeyPath: "./data/server.key",
			PublicKeyPath:  "./data/server.pub",
			DataDir:        "./data",
		},
		Logging: LoggingConfig{
			Level: "info",
		},
	}
}

// Load reads the config file. It looks for the path in this order:
//  1. Explicit path passed as argument (--config flag)
//  2. ./gigot.json in the working directory
//  3. Falls back to defaults
func Load(path string) (*Config, error) {
	cfg := Defaults()

	if path == "" {
		path = "gigot.json"
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && path == "gigot.json" {
			return cfg, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}

	// Resolve relative paths against config file directory.
	dir := filepath.Dir(path)
	if !filepath.IsAbs(cfg.Storage.RepoRoot) {
		cfg.Storage.RepoRoot = filepath.Join(dir, cfg.Storage.RepoRoot)
	}
	if cfg.Crypto.PrivateKeyPath != "" && !filepath.IsAbs(cfg.Crypto.PrivateKeyPath) {
		cfg.Crypto.PrivateKeyPath = filepath.Join(dir, cfg.Crypto.PrivateKeyPath)
	}
	if cfg.Crypto.PublicKeyPath != "" && !filepath.IsAbs(cfg.Crypto.PublicKeyPath) {
		cfg.Crypto.PublicKeyPath = filepath.Join(dir, cfg.Crypto.PublicKeyPath)
	}
	if cfg.Crypto.DataDir != "" && !filepath.IsAbs(cfg.Crypto.DataDir) {
		cfg.Crypto.DataDir = filepath.Join(dir, cfg.Crypto.DataDir)
	}

	return cfg, nil
}

// Save writes the config to a JSON file.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

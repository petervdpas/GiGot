package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the top-level GiGot configuration.
type Config struct {
	Server   ServerConfig   `json:"server"`
	Storage  StorageConfig  `json:"storage"`
	Auth     AuthConfig     `json:"auth"`
	Logging  LoggingConfig  `json:"logging"`
}

// ServerConfig controls the HTTP listener.
type ServerConfig struct {
	Host string `json:"host"`
	Port int    `json:"port"`
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

	// Resolve relative repo_root against config file directory.
	if !filepath.IsAbs(cfg.Storage.RepoRoot) {
		dir := filepath.Dir(path)
		cfg.Storage.RepoRoot = filepath.Join(dir, cfg.Storage.RepoRoot)
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

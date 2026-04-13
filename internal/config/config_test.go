package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 3417 {
		t.Errorf("expected port 3417, got %d", cfg.Server.Port)
	}
	if cfg.Storage.RepoRoot != "./repos" {
		t.Errorf("expected repo_root ./repos, got %s", cfg.Storage.RepoRoot)
	}
	if cfg.Auth.Enabled {
		t.Error("expected auth disabled by default")
	}
	if cfg.Auth.Type != "token" {
		t.Errorf("expected auth type token, got %s", cfg.Auth.Type)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected log level info, got %s", cfg.Logging.Level)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 3417 {
		t.Errorf("expected default port 3417, got %d", cfg.Server.Port)
	}
}

func TestLoadExplicitMissingFileErrors(t *testing.T) {
	_, err := Load("/nonexistent/path/gigot.json")
	if err == nil {
		t.Fatal("expected error for missing explicit config path")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gigot.json")

	data := []byte(`{
		"server": { "host": "127.0.0.1", "port": 9000 },
		"storage": { "repo_root": "/var/gigot/repos" },
		"auth": { "enabled": true, "type": "basic" },
		"logging": { "level": "debug" }
	}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 9000 {
		t.Errorf("expected port 9000, got %d", cfg.Server.Port)
	}
	if cfg.Storage.RepoRoot != "/var/gigot/repos" {
		t.Errorf("expected absolute repo_root unchanged, got %s", cfg.Storage.RepoRoot)
	}
	if !cfg.Auth.Enabled {
		t.Error("expected auth enabled")
	}
	if cfg.Auth.Type != "basic" {
		t.Errorf("expected auth type basic, got %s", cfg.Auth.Type)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected log level debug, got %s", cfg.Logging.Level)
	}
}

func TestLoadPartialOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gigot.json")

	data := []byte(`{ "server": { "port": 8080 } }`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("expected overridden port 8080, got %d", cfg.Server.Port)
	}
	// Defaults should remain for unset fields.
	if cfg.Auth.Type != "token" {
		t.Errorf("expected default auth type token, got %s", cfg.Auth.Type)
	}
}

func TestLoadRelativeRepoRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gigot.json")

	data := []byte(`{ "storage": { "repo_root": "data/repos" } }`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := filepath.Join(dir, "data/repos")
	if cfg.Storage.RepoRoot != expected {
		t.Errorf("expected repo_root resolved to %s, got %s", expected, cfg.Storage.RepoRoot)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gigot.json")

	if err := os.WriteFile(path, []byte(`{not json}`), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gigot.json")

	original := Defaults()
	original.Server.Port = 5555
	original.Logging.Level = "warn"

	if err := original.Save(path); err != nil {
		t.Fatalf("failed to save config: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("failed to load saved config: %v", err)
	}

	if loaded.Server.Port != 5555 {
		t.Errorf("expected port 5555, got %d", loaded.Server.Port)
	}
	if loaded.Logging.Level != "warn" {
		t.Errorf("expected log level warn, got %s", loaded.Logging.Level)
	}
}

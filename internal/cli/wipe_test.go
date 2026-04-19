package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/petervdpas/GiGot/internal/config"
)

// seedFakeDataDir populates a temp directory with every file a wipe
// could target, plus a repo_root directory with one bare-like subdir.
// Returns a Config pointing at the seeded paths so runWipe can be
// exercised against realistic state.
func seedFakeDataDir(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()

	dataDir := filepath.Join(root, "data")
	repoRoot := filepath.Join(root, "repos")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoRoot, "sample.git"), 0o755); err != nil {
		t.Fatalf("mkdir repoRoot: %v", err)
	}

	files := []string{
		filepath.Join(dataDir, "server.key"),
		filepath.Join(dataDir, "server.pub"),
		filepath.Join(dataDir, "admins.enc"),
		filepath.Join(dataDir, "tokens.enc"),
		filepath.Join(dataDir, "clients.enc"),
		filepath.Join(dataDir, "sessions.enc"),
		filepath.Join(dataDir, "credentials.enc"),
		filepath.Join(dataDir, "destinations.enc"),
		filepath.Join(dataDir, "server.key.bak.20260101T000000Z"),
		filepath.Join(dataDir, "admins.enc.bak.20260101T000000Z"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatalf("seed %s: %v", f, err)
		}
	}

	cfg := config.Defaults()
	cfg.Storage.RepoRoot = repoRoot
	cfg.Crypto.DataDir = dataDir
	cfg.Crypto.PrivateKeyPath = filepath.Join(dataDir, "server.key")
	cfg.Crypto.PublicKeyPath = filepath.Join(dataDir, "server.pub")
	return cfg
}

func TestRunWipe_Granular_RemovesOnlyTargetedFile(t *testing.T) {
	cfg := seedFakeDataDir(t)
	var out bytes.Buffer
	if err := runWipe(cfg, WipeTargets{Admins: true}, true, &out, strings.NewReader("")); err != nil {
		t.Fatalf("runWipe: %v", err)
	}
	if fileExists(t, filepath.Join(cfg.Crypto.DataDir, "admins.enc")) {
		t.Error("admins.enc should be gone")
	}
	// Every other store must survive a granular wipe.
	for _, survivor := range []string{"tokens.enc", "clients.enc", "sessions.enc",
		"credentials.enc", "destinations.enc", "server.key", "server.pub"} {
		if !fileExists(t, filepath.Join(cfg.Crypto.DataDir, survivor)) {
			t.Errorf("%s should survive -wipe-admins", survivor)
		}
	}
	if !dirExists(t, cfg.Storage.RepoRoot) {
		t.Error("repo_root should survive -wipe-admins")
	}
}

func TestRunWipe_Repos_RemovesRepoRootOnly(t *testing.T) {
	cfg := seedFakeDataDir(t)
	var out bytes.Buffer
	if err := runWipe(cfg, WipeTargets{Repos: true}, true, &out, strings.NewReader("")); err != nil {
		t.Fatalf("runWipe: %v", err)
	}
	if dirExists(t, cfg.Storage.RepoRoot) {
		t.Error("repo_root should be gone")
	}
	if !fileExists(t, filepath.Join(cfg.Crypto.DataDir, "admins.enc")) {
		t.Error("admins.enc should survive -wipe-repos")
	}
}

func TestRunWipe_FactoryReset_RemovesEverything(t *testing.T) {
	cfg := seedFakeDataDir(t)
	targets := WipeTargets{
		Repos: true, Admins: true, Tokens: true, Clients: true,
		Sessions: true, Credentials: true, Destinations: true, Keys: true,
	}
	var out bytes.Buffer
	if err := runWipe(cfg, targets, true, &out, strings.NewReader("")); err != nil {
		t.Fatalf("runWipe: %v", err)
	}
	for _, gone := range []string{"server.key", "server.pub", "admins.enc", "tokens.enc",
		"clients.enc", "sessions.enc", "credentials.enc", "destinations.enc",
		"server.key.bak.20260101T000000Z", "admins.enc.bak.20260101T000000Z"} {
		if fileExists(t, filepath.Join(cfg.Crypto.DataDir, gone)) {
			t.Errorf("%s should be gone after factory reset", gone)
		}
	}
	if dirExists(t, cfg.Storage.RepoRoot) {
		t.Error("repo_root should be gone after factory reset")
	}
}

// TestRunWipe_PromptRefusalAborts proves the confirmation gate is
// load-bearing: anything other than the literal "yes" leaves state
// untouched.
func TestRunWipe_PromptRefusalAborts(t *testing.T) {
	cfg := seedFakeDataDir(t)
	var out bytes.Buffer
	err := runWipe(cfg, WipeTargets{Admins: true}, false, &out, strings.NewReader("no\n"))
	if err != nil {
		t.Fatalf("runWipe: %v", err)
	}
	if !fileExists(t, filepath.Join(cfg.Crypto.DataDir, "admins.enc")) {
		t.Error("admins.enc must survive a refused prompt")
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("output should mention abort, got: %s", out.String())
	}
}

// TestRunWipe_PromptYesProceeds proves the same gate accepts the
// literal "yes" and then executes.
func TestRunWipe_PromptYesProceeds(t *testing.T) {
	cfg := seedFakeDataDir(t)
	var out bytes.Buffer
	if err := runWipe(cfg, WipeTargets{Admins: true}, false, &out, strings.NewReader("yes\n")); err != nil {
		t.Fatalf("runWipe: %v", err)
	}
	if fileExists(t, filepath.Join(cfg.Crypto.DataDir, "admins.enc")) {
		t.Error("admins.enc should be gone after prompt-confirmed wipe")
	}
}

// TestRunWipe_MissingPathsAreTreatedAsDone makes the wipe idempotent:
// removing a file that was never there is not an error, so running the
// same wipe twice in a row succeeds both times.
func TestRunWipe_MissingPathsAreTreatedAsDone(t *testing.T) {
	cfg := seedFakeDataDir(t)
	var out bytes.Buffer
	if err := runWipe(cfg, WipeTargets{Admins: true}, true, &out, strings.NewReader("")); err != nil {
		t.Fatalf("first runWipe: %v", err)
	}
	// Second run: admins.enc is already gone.
	out.Reset()
	if err := runWipe(cfg, WipeTargets{Admins: true}, true, &out, strings.NewReader("")); err != nil {
		t.Fatalf("second runWipe should be idempotent, got: %v", err)
	}
}

// TestRunWipe_EmptyTargetsRejected guards the "Parse should have
// rejected this" edge — even if a caller misuses runWipe directly with
// no targets, it must refuse rather than claim success.
func TestRunWipe_EmptyTargetsRejected(t *testing.T) {
	cfg := seedFakeDataDir(t)
	var out bytes.Buffer
	err := runWipe(cfg, WipeTargets{}, true, &out, strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error on empty targets")
	}
	if !strings.Contains(err.Error(), "no wipe targets") {
		t.Errorf("error should mention no targets, got: %v", err)
	}
}

func TestBuildWipePlan_FactoryResetCoversEverything(t *testing.T) {
	cfg := seedFakeDataDir(t)
	plan := buildWipePlan(cfg, WipeTargets{
		Repos: true, Admins: true, Tokens: true, Clients: true,
		Sessions: true, Credentials: true, Destinations: true, Keys: true,
	})
	// Must mention every sealed store, the repo root, both keys, and the
	// rotation-backup glob. Labels are user-facing copy; asserting
	// keywords rather than exact strings keeps the test non-brittle.
	expected := []string{"repos", "admins.enc", "tokens.enc", "clients.enc",
		"sessions.enc", "credentials.enc", "destinations.enc",
		"server.key", "server.pub", "*.bak.*"}
	blob := ""
	for _, item := range plan {
		blob += " " + item.Label + " " + item.Path + " " + item.Glob
	}
	for _, kw := range expected {
		if !strings.Contains(blob, kw) {
			t.Errorf("factory-reset plan missing %q (plan text: %s)", kw, blob)
		}
	}
}

func fileExists(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("stat %s: %v", path, err)
	}
	return !info.IsDir()
}

func dirExists(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.IsDir()
}

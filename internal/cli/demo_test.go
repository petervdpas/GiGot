package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/petervdpas/GiGot/internal/config"
)

// demoTestConfig builds a Config pointing at a fresh tempdir so every test
// starts from a clean slate. We create the data + repo dirs eagerly so the
// stores' Open call finds the sealed files (or misses them cleanly).
func demoTestConfig(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	cfg := config.Defaults()
	cfg.Storage.RepoRoot = filepath.Join(root, "repos")
	cfg.Crypto.DataDir = filepath.Join(root, "data")
	cfg.Crypto.PrivateKeyPath = filepath.Join(cfg.Crypto.DataDir, "server.key")
	cfg.Crypto.PublicKeyPath = filepath.Join(cfg.Crypto.DataDir, "server.pub")
	if err := os.MkdirAll(cfg.Storage.RepoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repos: %v", err)
	}
	if err := os.MkdirAll(cfg.Crypto.DataDir, 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	return cfg
}

// TestAddDemoSetup_ProvisionsEverything proves one invocation puts the
// Postman demo state on disk: admin account, scaffolded repo (with the
// Formidable marker), credential, and a freshly-issued subscription
// token whose output is printable and non-empty.
func TestAddDemoSetup_ProvisionsEverything(t *testing.T) {
	cfg := demoTestConfig(t)
	var out bytes.Buffer
	if err := runAddDemoSetup(cfg, &out); err != nil {
		t.Fatalf("runAddDemoSetup: %v", err)
	}

	// Admin verify.
	stores, err := openDemoStores(cfg)
	if err != nil {
		t.Fatalf("reopen stores: %v", err)
	}
	if _, err := stores.admins.Verify(DemoAdminUser, DemoAdminPassword); err != nil {
		t.Errorf("demo admin bcrypt verification failed: %v", err)
	}

	// Repo exists and has the Formidable marker.
	if !stores.git.Exists(DemoRepoName) {
		t.Fatalf("demo repo should exist after add-demo-setup")
	}
	tree, err := stores.git.Tree(DemoRepoName, "")
	if err != nil {
		t.Fatalf("Tree %s: %v", DemoRepoName, err)
	}
	foundMarker := false
	foundBasic := false
	for _, entry := range tree.Files {
		switch entry.Path {
		case ".formidable/context.json":
			foundMarker = true
		case "templates/basic.yaml":
			foundBasic = true
		}
	}
	if !foundMarker {
		t.Errorf("scaffolded repo missing .formidable/context.json")
	}
	if !foundBasic {
		t.Errorf("scaffolded repo missing templates/basic.yaml")
	}

	// Credential.
	cred, err := stores.credentials.Get(DemoCredentialName)
	if err != nil {
		t.Fatalf("credential Get: %v", err)
	}
	if cred.Kind != DemoCredentialKind {
		t.Errorf("credential kind = %q, want %q", cred.Kind, DemoCredentialKind)
	}

	// Token: printed to stdout, issued in the sealed store.
	tokenPattern := regexp.MustCompile(`token\s+([a-f0-9]{32,})`)
	match := tokenPattern.FindStringSubmatch(out.String())
	if match == nil {
		t.Fatalf("output missing a token line:\n%s", out.String())
	}
	issued := match[1]
	var foundInStore bool
	for _, entry := range stores.tokens.List() {
		if entry.Token == issued && entry.Username == demoTokenUsername {
			foundInStore = true
			if len(entry.Repos) != 1 || entry.Repos[0] != DemoRepoName {
				t.Errorf("token repos = %v, want [%q]", entry.Repos, DemoRepoName)
			}
			break
		}
	}
	if !foundInStore {
		t.Errorf("token %q not in the sealed store", issued)
	}
}

// TestAddDemoSetup_IdempotentRepeat proves running add twice doesn't
// error, keeps the admin/repo/credential in place, and produces a
// second valid token (so multiple runs stack tokens rather than
// clobbering state).
func TestAddDemoSetup_IdempotentRepeat(t *testing.T) {
	cfg := demoTestConfig(t)
	var out bytes.Buffer
	if err := runAddDemoSetup(cfg, &out); err != nil {
		t.Fatalf("first add: %v", err)
	}
	out.Reset()
	if err := runAddDemoSetup(cfg, &out); err != nil {
		t.Fatalf("second add: %v", err)
	}
	if !strings.Contains(out.String(), "already present") {
		t.Errorf("second run should note repo already present, got: %s", out.String())
	}
	stores, err := openDemoStores(cfg)
	if err != nil {
		t.Fatalf("reopen stores: %v", err)
	}
	if len(stores.tokens.List()) != 2 {
		t.Errorf("expected 2 tokens after two add-demo-setup runs, got %d", len(stores.tokens.List()))
	}
}

// TestRemoveDemoSetup_UndoesEverything proves remove wipes every
// artefact add provisioned — admin, repo, credential, and every token
// ever issued to the demo user.
func TestRemoveDemoSetup_UndoesEverything(t *testing.T) {
	cfg := demoTestConfig(t)
	var out bytes.Buffer
	if err := runAddDemoSetup(cfg, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Issue a second demo token to prove remove sweeps all of them.
	if err := runAddDemoSetup(cfg, &out); err != nil {
		t.Fatalf("second add: %v", err)
	}
	out.Reset()
	if err := runRemoveDemoSetup(cfg, &out); err != nil {
		t.Fatalf("remove: %v", err)
	}

	stores, err := openDemoStores(cfg)
	if err != nil {
		t.Fatalf("reopen stores: %v", err)
	}
	if _, err := stores.admins.Verify(DemoAdminUser, DemoAdminPassword); err == nil {
		t.Error("demo admin should be gone after remove")
	}
	if stores.git.Exists(DemoRepoName) {
		t.Error("demo repo should be gone after remove")
	}
	if _, err := stores.credentials.Get(DemoCredentialName); err == nil {
		t.Error("demo credential should be gone after remove")
	}
	for _, entry := range stores.tokens.List() {
		if entry.Username == demoTokenUsername {
			t.Errorf("demo-owned token %q should have been revoked", entry.Token)
		}
	}
}

// TestRemoveDemoSetup_Idempotent proves remove on a clean data dir
// succeeds and leaves no residue — "should not be on disk afterwards"
// is the contract, and it doesn't care whether something was there to
// begin with.
func TestRemoveDemoSetup_Idempotent(t *testing.T) {
	cfg := demoTestConfig(t)
	var out bytes.Buffer
	if err := runRemoveDemoSetup(cfg, &out); err != nil {
		t.Fatalf("remove on clean dir: %v", err)
	}
}

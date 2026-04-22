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
	if _, err := stores.accounts.Verify(DemoAdminUser, DemoAdminPassword); err != nil {
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

	// Plain companion repo exists, has a commit, and does NOT carry
	// the Formidable marker — that's the point of the convert-flow
	// fixture.
	if !stores.git.Exists(DemoPlainRepoName) {
		t.Fatalf("plain repo should exist after add-demo-setup")
	}
	plainTree, err := stores.git.Tree(DemoPlainRepoName, "")
	if err != nil {
		t.Fatalf("Tree %s: %v", DemoPlainRepoName, err)
	}
	for _, entry := range plainTree.Files {
		if entry.Path == ".formidable/context.json" {
			t.Errorf("plain repo should NOT carry the Formidable marker")
		}
	}

	// Credential.
	cred, err := stores.credentials.Get(DemoCredentialName)
	if err != nil {
		t.Fatalf("credential Get: %v", err)
	}
	if cred.Kind != DemoCredentialKind {
		t.Errorf("credential kind = %q, want %q", cred.Kind, DemoCredentialKind)
	}

	// Tokens: one per demo repo, each printed to stdout and present in
	// the sealed store. Subscription keys are one-repo-per-key, so we
	// expect exactly two entries for the demo user.
	tokenPattern := regexp.MustCompile(`token\s+\S+\s+([a-f0-9]{32,})`)
	matches := tokenPattern.FindAllStringSubmatch(out.String(), -1)
	if len(matches) != 2 {
		t.Fatalf("expected 2 token lines, got %d:\n%s", len(matches), out.String())
	}
	printed := map[string]bool{matches[0][1]: true, matches[1][1]: true}

	reposByToken := map[string]string{}
	for _, entry := range stores.tokens.List() {
		if entry.Username == demoTokenUsername {
			reposByToken[entry.Token] = entry.Repo
		}
	}
	if len(reposByToken) != 2 {
		t.Fatalf("demo user should have 2 tokens in the store, got %d: %+v", len(reposByToken), reposByToken)
	}
	wantRepos := map[string]bool{DemoRepoName: false, DemoPlainRepoName: false}
	for tok, repo := range reposByToken {
		if !printed[tok] {
			t.Errorf("store token %q was not printed to stdout", tok)
		}
		if _, ok := wantRepos[repo]; !ok {
			t.Errorf("unexpected repo on demo token: %q", repo)
			continue
		}
		wantRepos[repo] = true
	}
	for r, seen := range wantRepos {
		if !seen {
			t.Errorf("no demo token for repo %q", r)
		}
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
	// With idempotent Issue-or-existing, re-running -add-demo-setup
	// should NOT duplicate tokens — each run reconciles the same
	// (user, repo) pairs. So two runs still yield two tokens total.
	if len(stores.tokens.List()) != 2 {
		t.Errorf("expected 2 tokens (one per demo repo) after two runs, got %d", len(stores.tokens.List()))
	}
}

// TestRemoveDemoSetup_UndoesEverything proves remove wipes every
// artefact add provisioned — admin, repo, credential, and every token
// ever issued to the demo user. Also sweeps legacy-postman tokens
// because the Postman collection's "Legacy Tokens" folder can leave
// those behind on an interrupted run (this test documents the contract:
// the legacy username is part of the demo flow's footprint).
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
	// Simulate a legacy-postman token lingering from a prior collection
	// run whose teardown didn't complete (what the admin UI shows under
	// Active keys when a Postman run was interrupted).
	stores, err := openDemoStores(cfg)
	if err != nil {
		t.Fatalf("reopen stores to seed legacy token: %v", err)
	}
	staleLegacy, err := stores.tokens.Issue(legacyTokenUsername, "legacy-repo", nil)
	if err != nil {
		t.Fatalf("seed legacy token: %v", err)
	}

	out.Reset()
	if err := runRemoveDemoSetup(cfg, &out); err != nil {
		t.Fatalf("remove: %v", err)
	}

	stores, err = openDemoStores(cfg)
	if err != nil {
		t.Fatalf("reopen stores: %v", err)
	}
	if _, err := stores.accounts.Verify(DemoAdminUser, DemoAdminPassword); err == nil {
		t.Error("demo admin should be gone after remove")
	}
	if stores.git.Exists(DemoRepoName) {
		t.Error("demo repo should be gone after remove")
	}
	if stores.git.Exists(DemoPlainRepoName) {
		t.Error("plain repo should be gone after remove")
	}
	if _, err := stores.credentials.Get(DemoCredentialName); err == nil {
		t.Error("demo credential should be gone after remove")
	}
	for _, entry := range stores.tokens.List() {
		if entry.Username == demoTokenUsername {
			t.Errorf("demo-owned token %q should have been revoked", entry.Token)
		}
		if entry.Username == legacyTokenUsername {
			t.Errorf("legacy-postman token %q should have been revoked", entry.Token)
		}
		if entry.Token == staleLegacy {
			t.Errorf("seeded stale legacy token survived remove: %q", staleLegacy)
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

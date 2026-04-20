package server

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExecuteMirrorPush_LocalBareRemote spins up two bare repos in a
// temp dir — one with a commit on master and an audit ref, one empty —
// and pushes the first into the second via executeMirrorPush. A
// local plain-path push is the simplest way to exercise the real git
// binary with the real refspec pair without needing a network or PAT.
// Plain filesystem paths (not file:// URLs) bypass the protocol.file
// allowlist, so no GIT_CONFIG_GLOBAL dance is needed. The askpass shim
// echoes a placeholder secret that git then ignores because local
// pushes don't authenticate.
func TestExecuteMirrorPush_LocalBareRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "source.git")
	dstPath := filepath.Join(tmp, "mirror.git")

	// Source bare repo.
	if out, err := exec.Command("git", "init", "--bare", srcPath).CombinedOutput(); err != nil {
		t.Fatalf("init source: %s %v", out, err)
	}

	// Working tree on the side so we have something real to push into source.
	workPath := filepath.Join(tmp, "work")
	mustRun(t, "git", "init", "-b", "master", workPath)
	runIn(t, workPath, "git", "config", "user.email", "test@example.com")
	runIn(t, workPath, "git", "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(workPath, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, workPath, "git", "add", "README.md")
	runIn(t, workPath, "git", "commit", "-m", "initial")
	runIn(t, workPath, "git", "remote", "add", "src", srcPath)
	runIn(t, workPath, "git", "push", "src", "master")

	// Stamp a fake audit entry on source by copying master to
	// refs/audit/main — enough to prove the refspec carries custom
	// namespaces through the push.
	runIn(t, srcPath, "git", "update-ref", "refs/audit/main", "master")

	// Empty destination bare repo.
	if out, err := exec.Command("git", "init", "--bare", dstPath).CombinedOutput(); err != nil {
		t.Fatalf("init mirror: %s %v", out, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Non-empty secret required; content irrelevant since local pushes
	// don't authenticate.
	out, err := executeMirrorPush(ctx, srcPath, dstPath, "irrelevant-secret")
	if err != nil {
		t.Fatalf("push failed: %v\noutput: %s", err, string(out))
	}

	// Both refs must land on the mirror — refs/heads/master is table
	// stakes; refs/audit/main is the whole point of the custom refspec.
	refs, err := exec.Command("git", "-C", dstPath, "for-each-ref",
		"--format=%(refname)").CombinedOutput()
	if err != nil {
		t.Fatalf("for-each-ref on mirror: %v", err)
	}
	got := string(refs)
	if !strings.Contains(got, "refs/heads/master") {
		t.Errorf("mirror missing refs/heads/master, got: %s", got)
	}
	if !strings.Contains(got, "refs/audit/main") {
		t.Errorf("mirror missing refs/audit/main (the whole point), got: %s", got)
	}
}

// TestExecuteMirrorPush_SecretRedactedFromOutput covers the defence-in-
// depth stripping: even if git (or a credential helper) echoes the
// secret into stderr, executeMirrorPush redacts it before surfacing
// the bytes. We force a push failure against a nonexistent remote so
// git writes diagnostic output, then assert the plaintext secret is
// not in it.
func TestExecuteMirrorPush_SecretRedactedFromOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "source.git")
	if out, err := exec.Command("git", "init", "--bare", srcPath).CombinedOutput(); err != nil {
		t.Fatalf("init source: %s %v", out, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Push to a path that doesn't exist — git will fail loudly.
	bogus := filepath.Join(tmp, "does-not-exist.git")
	secret := "ghp_mysecrettoken_xyz"
	out, err := executeMirrorPush(ctx, srcPath, bogus, secret)
	if err == nil {
		t.Fatal("expected push to fail against missing remote")
	}
	if strings.Contains(string(out), secret) {
		t.Errorf("secret leaked in push output: %s", string(out))
	}
}

// TestRedactSecret_Basic is a tiny unit test for the redactor so
// future changes can't silently break the scrubbing contract.
func TestRedactSecret_Basic(t *testing.T) {
	in := []byte("fatal: authentication failed for ghp_token123 at https://ghp_token123@github.com")
	got := string(redactSecret(in, "ghp_token123"))
	if strings.Contains(got, "ghp_token123") {
		t.Errorf("redact failed: %s", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Errorf("redact replacement missing: %s", got)
	}
}

func TestRedactSecret_EmptySecretIsNoop(t *testing.T) {
	in := []byte("unchanged output")
	got := string(redactSecret(in, ""))
	if got != "unchanged output" {
		t.Errorf("empty secret should be a noop, got: %s", got)
	}
}

// mustRun / runIn are tiny helpers — t.Fatalf on any error so the
// mirror_test.go file stays readable without a nested if-err chain.
func mustRun(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %s %v", name, args, out, err)
	}
}

func runIn(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("in %s: %s %v failed: %s %v", dir, name, args, out, err)
	}
}

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

// TestExecuteLsRemote_LocalBareRemote spins up a bare repo with a
// commit on master AND a refs/audit/main entry, then calls
// executeLsRemote against it via a plain filesystem path. Mirrors
// the shape of TestExecuteMirrorPush_LocalBareRemote so the real
// shell-out path (args + askpass shim + env) is exercised end-to-
// end on the actual git binary, not the lsRemoteFn stub.
//
// Until this test landed, every existing test stubbed lsRemote and
// the only thing exercising the real `git ls-remote` command was
// production traffic — a bug in argv ("ls-remote" misspelled) or
// the askpass shim contract would land in production undetected.
func TestExecuteLsRemote_LocalBareRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "source.git")

	// Bare repo with one commit on master + a synthetic refs/audit/main.
	// The audit ref is the load-bearing assertion: ls-remote must
	// surface NON-standard namespaces, not just refs/heads/* — the
	// mirror remote-status compare needs both. --refs strips peeled
	// tags; we don't seed any, so this also pins that --refs hides
	// nothing the compare cares about.
	if out, err := exec.Command("git", "init", "--bare", srcPath).CombinedOutput(); err != nil {
		t.Fatalf("init source: %s %v", out, err)
	}
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
	runIn(t, srcPath, "git", "update-ref", "refs/audit/main", "master")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	refs, out, err := executeLsRemote(ctx, srcPath, "irrelevant-secret")
	if err != nil {
		t.Fatalf("ls-remote failed: %v\noutput: %s", err, string(out))
	}
	if got := refs["refs/heads/master"]; got == "" {
		t.Errorf("missing refs/heads/master in result: %+v", refs)
	}
	if got := refs["refs/audit/main"]; got == "" {
		t.Errorf("missing refs/audit/main in result (whole point of the audit-namespace pin): %+v", refs)
	}
	// Both refs point at the same commit — sanity check the SHA isn't
	// silently empty/mangled by the parser.
	if refs["refs/heads/master"] != refs["refs/audit/main"] {
		t.Errorf("refs should share commit; got master=%q audit=%q",
			refs["refs/heads/master"], refs["refs/audit/main"])
	}
	if len(refs["refs/heads/master"]) < 7 {
		t.Errorf("SHA looks truncated: %q", refs["refs/heads/master"])
	}
}

// TestExecuteLsRemote_EmptyRepoReturnsEmpty — ls-remote against a
// repo with no refs returns an empty map and no error. This is the
// path a fresh-cloned destination hits before its first push; the
// compare logic treats it as "no remote refs in mirrored namespaces"
// → in_sync if local is also empty. Pinning the empty case here
// guards against the parser ever erroring on a zero-line response.
func TestExecuteLsRemote_EmptyRepoReturnsEmpty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	emptyPath := filepath.Join(tmp, "empty.git")
	if out, err := exec.Command("git", "init", "--bare", emptyPath).CombinedOutput(); err != nil {
		t.Fatalf("init empty: %s %v", out, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	refs, out, err := executeLsRemote(ctx, emptyPath, "irrelevant-secret")
	if err != nil {
		t.Fatalf("ls-remote on empty repo failed: %v\noutput: %s", err, string(out))
	}
	if len(refs) != 0 {
		t.Errorf("empty repo should return zero refs, got: %+v", refs)
	}
}

// TestExecuteLsRemote_SecretRedactedFromOutput — defence-in-depth
// scrubbing on the ls-remote path, paired with the same assertion
// on executeMirrorPush. Forces a failure against a nonexistent
// remote so git writes diagnostic output, then asserts the
// plaintext secret never appears in it. The redaction code is
// shared between push and ls-remote (redactSecret), but if a
// future refactor splits the implementation, this test catches the
// regression on either side.
func TestExecuteLsRemote_SecretRedactedFromOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	bogus := filepath.Join(tmp, "does-not-exist.git")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	secret := "ghp_lsremote_secret_xyz"
	_, out, err := executeLsRemote(ctx, bogus, secret)
	if err == nil {
		t.Fatal("expected ls-remote to fail against missing remote")
	}
	if strings.Contains(string(out), secret) {
		t.Errorf("secret leaked in ls-remote output: %s", string(out))
	}
}

// TestExecuteLsRemote_RejectsEmptyArgs — the input-guard branches
// (no URL, no secret) return early before any shell-out. Pins both
// to keep the function signature honest about its contract.
func TestExecuteLsRemote_RejectsEmptyArgs(t *testing.T) {
	ctx := context.Background()
	if _, _, err := executeLsRemote(ctx, "", "secret"); err == nil {
		t.Error("empty URL should be rejected before shell-out")
	}
	if _, _, err := executeLsRemote(ctx, "https://example.com/x.git", ""); err == nil {
		t.Error("empty secret should be rejected before shell-out")
	}
}

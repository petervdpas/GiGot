package server

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExecuteMirrorPull_LocalBareRemote spins up two bare repos in a
// temp dir — a "remote" with two refs (refs/heads/master + refs/audit/
// main) and an empty "local" — then runs executeMirrorPull from local
// against remote. Local should grow both refs to match remote. A local
// plain-path fetch is the simplest way to exercise the real git binary
// with the real refspec pair without needing a network or PAT. Plain
// filesystem paths bypass the protocol.file allowlist; the askpass
// shim echoes a placeholder secret git ignores for local fetches.
func TestExecuteMirrorPull_LocalBareRemote(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	remotePath := filepath.Join(tmp, "remote.git")
	localPath := filepath.Join(tmp, "local.git")

	// Remote bare with a real commit on master and a fake audit ref
	// pointing at the same commit. Mirrors the push test's setup
	// inverted: there the bare being-pushed-from carries the refs;
	// here the bare being-pulled-from does.
	if out, err := exec.Command("git", "init", "--bare", remotePath).CombinedOutput(); err != nil {
		t.Fatalf("init remote: %s %v", out, err)
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
	runIn(t, workPath, "git", "remote", "add", "src", remotePath)
	runIn(t, workPath, "git", "push", "src", "master")
	runIn(t, remotePath, "git", "update-ref", "refs/audit/main", "master")

	// Empty local bare.
	if out, err := exec.Command("git", "init", "--bare", localPath).CombinedOutput(); err != nil {
		t.Fatalf("init local: %s %v", out, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := executeMirrorPull(ctx, localPath, remotePath, "irrelevant-secret")
	if err != nil {
		t.Fatalf("pull failed: %v\noutput: %s", err, string(out))
	}

	// Both refs must land on local — refs/heads/master is table
	// stakes; refs/audit/main is the whole point of the symmetric
	// refspec.
	refs, err := exec.Command("git", "-C", localPath, "for-each-ref",
		"--format=%(refname)").CombinedOutput()
	if err != nil {
		t.Fatalf("for-each-ref on local: %v", err)
	}
	got := string(refs)
	if !strings.Contains(got, "refs/heads/master") {
		t.Errorf("local missing refs/heads/master, got: %s", got)
	}
	if !strings.Contains(got, "refs/audit/main") {
		t.Errorf("local missing refs/audit/main (the whole point), got: %s", got)
	}
}

// TestExecuteMirrorPull_ForceOverwritesLocal is the load-bearing one
// for the admin pull semantics: when local has commits the remote
// doesn't, the pull DROPS them. This is the admin's deliberate "I'm
// accepting the remote's state as truth" choice (see remote-sync.md
// §3.2). A pull that silently merged or refused on divergence would
// turn this test green by accident — the assertion must read the
// final ref and see the remote's SHA, not the local one.
func TestExecuteMirrorPull_ForceOverwritesLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	remotePath := filepath.Join(tmp, "remote.git")
	localPath := filepath.Join(tmp, "local.git")

	// Build remote with one commit ("remote-commit").
	if out, err := exec.Command("git", "init", "--bare", remotePath).CombinedOutput(); err != nil {
		t.Fatalf("init remote: %s %v", out, err)
	}
	rWork := filepath.Join(tmp, "rwork")
	mustRun(t, "git", "init", "-b", "master", rWork)
	runIn(t, rWork, "git", "config", "user.email", "remote@example.com")
	runIn(t, rWork, "git", "config", "user.name", "Remote")
	if err := os.WriteFile(filepath.Join(rWork, "from-remote.txt"), []byte("remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, rWork, "git", "add", "from-remote.txt")
	runIn(t, rWork, "git", "commit", "-m", "remote-commit")
	runIn(t, rWork, "git", "remote", "add", "src", remotePath)
	runIn(t, rWork, "git", "push", "src", "master")

	remoteSHA := strings.TrimSpace(string(mustRunOut(t, "git", "-C", remotePath, "rev-parse", "refs/heads/master")))

	// Build local with a DIFFERENT commit — same branch name, parallel
	// history. This is the divergence the admin is resolving.
	if out, err := exec.Command("git", "init", "--bare", localPath).CombinedOutput(); err != nil {
		t.Fatalf("init local: %s %v", out, err)
	}
	lWork := filepath.Join(tmp, "lwork")
	mustRun(t, "git", "init", "-b", "master", lWork)
	runIn(t, lWork, "git", "config", "user.email", "local@example.com")
	runIn(t, lWork, "git", "config", "user.name", "Local")
	if err := os.WriteFile(filepath.Join(lWork, "from-local.txt"), []byte("local\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIn(t, lWork, "git", "add", "from-local.txt")
	runIn(t, lWork, "git", "commit", "-m", "local-commit")
	runIn(t, lWork, "git", "remote", "add", "dst", localPath)
	runIn(t, lWork, "git", "push", "dst", "master")

	localBefore := strings.TrimSpace(string(mustRunOut(t, "git", "-C", localPath, "rev-parse", "refs/heads/master")))
	if localBefore == remoteSHA {
		t.Fatalf("test setup: local and remote SHAs already match (%s); divergence not established", localBefore)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if out, err := executeMirrorPull(ctx, localPath, remotePath, "irrelevant-secret"); err != nil {
		t.Fatalf("pull failed: %v\noutput: %s", err, string(out))
	}

	localAfter := strings.TrimSpace(string(mustRunOut(t, "git", "-C", localPath, "rev-parse", "refs/heads/master")))
	if localAfter != remoteSHA {
		t.Errorf("force-pull did not overwrite local: before=%s after=%s remote=%s",
			localBefore, localAfter, remoteSHA)
	}
	if localAfter == localBefore {
		t.Errorf("force-pull left local unchanged — refspec not honouring the leading +")
	}
}

// TestExecuteMirrorPull_SecretRedactedFromOutput covers the same
// defence-in-depth stripping the push side has: even if git echoes
// the secret on a failed fetch, executeMirrorPull redacts it before
// surfacing the bytes. We force a fetch failure against a nonexistent
// remote so git writes diagnostic output.
func TestExecuteMirrorPull_SecretRedactedFromOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	tmp := t.TempDir()
	localPath := filepath.Join(tmp, "local.git")
	if out, err := exec.Command("git", "init", "--bare", localPath).CombinedOutput(); err != nil {
		t.Fatalf("init local: %s %v", out, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	bogus := filepath.Join(tmp, "does-not-exist.git")
	secret := "ghp_pullsecret_xyz"
	out, err := executeMirrorPull(ctx, localPath, bogus, secret)
	if err == nil {
		t.Fatal("expected pull to fail against missing remote")
	}
	if bytes.Contains(out, []byte(secret)) {
		t.Errorf("secret leaked in pull output: %s", string(out))
	}
}

// mustRunOut is the capturing variant of mustRun — returns combined
// output. Lives here because mirror_push_test.go's helpers only
// fatal-on-error; this test needs the bytes.
func mustRunOut(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %s %v", name, args, out, err)
	}
	return out
}

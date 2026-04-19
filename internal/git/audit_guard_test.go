package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInitBareInstallsAuditGuard proves the pre-receive hook lands on disk
// as an executable file containing the expected refusal logic. A repo that
// escapes this check would leave refs/audit/* writable via git push.
func TestInitBareInstallsAuditGuard(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("guarded"); err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	hookPath := filepath.Join(m.RepoPath("guarded"), "hooks", "pre-receive")

	info, err := os.Stat(hookPath)
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("pre-receive should be executable, mode=%v", info.Mode())
	}

	body, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("read hook: %v", err)
	}
	if !strings.Contains(string(body), "refs/audit/*") {
		t.Errorf("hook body missing refs/audit/* guard: %s", body)
	}
}

// TestAuditGuardRejectsClientPush proves the hook actually fires on a real
// git push over local transport: a client cannot overwrite refs/audit/main
// even after AppendAudit has seeded the chain.
func TestAuditGuardRejectsClientPush(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("target"); err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	// Seed an audit entry so refs/audit/main exists and a client could
	// plausibly know the ref name.
	if _, err := m.AppendAudit("target", AuditEvent{Type: "seed"}); err != nil {
		t.Fatalf("AppendAudit seed: %v", err)
	}

	// Local workbench: make one ordinary commit so we have a SHA to push.
	work := filepath.Join(t.TempDir(), "work")
	runExpectOK(t, "git", "init", work)
	runExpectOK(t, "git", "-C", work, "config", "user.email", "evil@client")
	runExpectOK(t, "git", "-C", work, "config", "user.name", "evil")
	if err := os.WriteFile(filepath.Join(work, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runExpectOK(t, "git", "-C", work, "add", "x.txt")
	runExpectOK(t, "git", "-C", work, "commit", "-m", "forged")
	headOut, err := exec.Command("git", "-C", work, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	sha := strings.TrimSpace(string(headOut))

	// Push to refs/audit/main — hook must refuse. Use a file-transport URL
	// that points straight at the bare repo so receive-pack runs and the
	// hook is exercised end-to-end.
	pushURL := m.RepoPath("target")
	pushCmd := exec.Command("git", "-C", work, "push", pushURL, sha+":refs/audit/main")
	// Non-ff overrides are irrelevant here — the hook rejects before any
	// ref-update logic runs — but pass --force to isolate "blocked by
	// hook" from "blocked by non-ff" in the failure mode.
	pushCmd.Args = append(pushCmd.Args, "--force")
	out, err := pushCmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected push to refs/audit/main to fail, but it succeeded: %s", out)
	}
	if !strings.Contains(string(out), "server-owned") {
		t.Errorf("expected hook rejection message in stderr, got: %s", out)
	}

	// Sanity: refs/audit/main must still point at the seed commit, not the
	// forged one. This is the load-bearing post-condition.
	headAfter, err := m.AuditHead("target")
	if err != nil {
		t.Fatalf("AuditHead after rejected push: %v", err)
	}
	if headAfter == sha {
		t.Errorf("refs/audit/main was overwritten despite hook rejection")
	}
}

// TestEnsureAuditGuardsRetrofitsLegacyRepo mimics a repo created before
// slice 2: the hook file gets deleted, then EnsureAuditGuards re-installs
// it so existing deployments don't need manual intervention.
func TestEnsureAuditGuardsRetrofitsLegacyRepo(t *testing.T) {
	m := NewManager(t.TempDir())
	if err := m.InitBare("legacy"); err != nil {
		t.Fatalf("InitBare: %v", err)
	}
	hookPath := filepath.Join(m.RepoPath("legacy"), "hooks", "pre-receive")
	if err := os.Remove(hookPath); err != nil {
		t.Fatalf("remove hook: %v", err)
	}
	if err := m.EnsureAuditGuards(); err != nil {
		t.Fatalf("EnsureAuditGuards: %v", err)
	}
	if _, err := os.Stat(hookPath); err != nil {
		t.Fatalf("hook not re-installed: %v", err)
	}
}

func runExpectOK(t *testing.T, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v: %s", name, strings.Join(args, " "), err, out)
	}
}

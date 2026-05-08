package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)

// mirrorPullTimeout caps one fetch invocation. Same generous two-minute
// budget as the push side: pull is the rare admin-only escape hatch
// (see remote-sync.md §3.2), but the network round-trip shape is
// identical, so the budget should match.
const mirrorPullTimeout = 2 * time.Minute

// pullDestinationFn is the signature the server uses to invoke an
// inbound mirror fetch. Mirrors pushDestinationFn so tests can stub
// the shell-out without running real git.
type pullDestinationFn func(ctx context.Context, repoPath, destURL, secret string) (output []byte, err error)

// executeMirrorPull is the real implementation wired into Server on
// boot. It runs `git fetch --force <url> +refs/heads/*:refs/heads/*
// +refs/audit/*:refs/audit/*` against the destination, force-updating
// the local bare repo's mirrored namespaces to match whatever the
// remote currently holds. Same askpass shim and secret-redaction as
// the push path.
//
// Force semantics are deliberate: this is the operator's escape hatch
// for "the remote has a commit GiGot doesn't have, accept it as the
// new local state." If the operator wanted to preserve commits on
// both sides they would have used a working clone; GiGot's job here
// is to be a sync hub, not a merge tool.
func executeMirrorPull(ctx context.Context, repoPath, destURL, secret string) ([]byte, error) {
	if repoPath == "" {
		return nil, fmt.Errorf("mirror: repo path required")
	}
	if destURL == "" {
		return nil, fmt.Errorf("mirror: destination url required")
	}
	if secret == "" {
		return nil, fmt.Errorf("mirror: credential secret required")
	}

	ask, err := os.CreateTemp("", "gigot-askpass-*.sh")
	if err != nil {
		return nil, fmt.Errorf("mirror: askpass tempfile: %w", err)
	}
	askPath := ask.Name()
	defer os.Remove(askPath)
	if _, err := ask.WriteString(mirrorAskpassScript); err != nil {
		ask.Close()
		return nil, fmt.Errorf("mirror: askpass write: %w", err)
	}
	if err := ask.Chmod(0o700); err != nil {
		ask.Close()
		return nil, fmt.Errorf("mirror: askpass chmod: %w", err)
	}
	if err := ask.Close(); err != nil {
		return nil, fmt.Errorf("mirror: askpass close: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, mirrorPullTimeout)
	defer cancel()

	// Force-fetch into refs/heads/* and refs/audit/* directly. The +
	// in mirrorRefspecs already permits non-fast-forward updates;
	// --force is belt-and-braces and harmless when the refspec
	// already has it. --no-tags keeps stray tag namespaces from
	// landing in the bare repo.
	args := append([]string{"-C", repoPath, "fetch", "--force", "--no-tags", destURL}, mirrorRefspecs...)
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(),
		"GIT_ASKPASS="+askPath,
		"GIT_TERMINAL_PROMPT=0",
		"GIGOT_PUSH_USERNAME=x-access-token",
		"GIGOT_PUSH_PASSWORD="+secret,
	)
	out, runErr := cmd.CombinedOutput()
	return redactSecret(out, secret), runErr
}

package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// mirrorRefspecs is the fixed refspec pair every mirror push uses.
// refs/heads/* covers the user-visible branches; refs/audit/* piggybacks
// the server-authored audit chain onto the same remote so an external
// mirror is as auditable as the source (see audit-trail.md). GitHub
// accepts refs/audit/* as a non-standard namespace — confirmed by the
// 2026-04-20 spike against petervdpas/Braindamage.
var mirrorRefspecs = []string{
	"+refs/heads/*:refs/heads/*",
	"+refs/audit/*:refs/audit/*",
}

// mirrorPushTimeout caps one push invocation. A mirror push is a short
// outbound op; two minutes is generous even on a slow network. Callers
// run synchronously in an HTTP handler, so the cap keeps the client
// from waiting forever on a stuck remote.
const mirrorPushTimeout = 2 * time.Minute

// mirrorAskpassScript is the body of a one-shot GIT_ASKPASS shim. Git
// calls it for username/password prompts; the shim reads the answers
// from environment variables set only for this push. This avoids
// putting the secret on the command line (where /proc/*/cmdline would
// leak it to other local users) or in the URL's userinfo (same
// problem).
const mirrorAskpassScript = `#!/bin/sh
case "$1" in
  *[Uu]sername*) echo "$GIGOT_PUSH_USERNAME" ;;
  *)             echo "$GIGOT_PUSH_PASSWORD" ;;
esac
`

// pushDestinationFn is the signature the server uses to invoke an
// outbound mirror push. Injected on Server so tests can stub it
// without shelling out to git. output is returned for writing into
// last_sync_error on failure; redact the secret before surfacing it
// anywhere a user can see.
type pushDestinationFn func(ctx context.Context, repoPath, destURL, secret string) (output []byte, err error)

// executeMirrorPush is the real implementation wired into Server on
// boot. It writes a temp askpass shim, invokes git push with the fixed
// refspec pair, and strips the secret from any captured output before
// returning.
func executeMirrorPush(ctx context.Context, repoPath, destURL, secret string) ([]byte, error) {
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

	ctx, cancel := context.WithTimeout(ctx, mirrorPushTimeout)
	defer cancel()

	args := append([]string{"-C", repoPath, "push", destURL}, mirrorRefspecs...)
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

// redactSecret scrubs any literal occurrence of secret from captured
// output. Git with GIT_ASKPASS won't normally echo the PAT, but a
// misbehaving remote or credential helper could, so this is a
// defence-in-depth pass before the output reaches last_sync_error.
func redactSecret(out []byte, secret string) []byte {
	if secret == "" {
		return out
	}
	return []byte(strings.ReplaceAll(string(out), secret, "<redacted>"))
}

package server

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/petervdpas/GiGot/internal/credentials"
	"github.com/petervdpas/GiGot/internal/destinations"
)

// Remote-status overall states. Empty string means "never checked"
// (the destination was just added). The set is small and stable; the
// admin UI renders them as coloured badges.
const (
	remoteStatusInSync   = "in_sync"
	remoteStatusDiverged = "diverged"
	remoteStatusError    = "error"
)

// Per-ref state in destinations.RemoteRefStatus.State. Bare git
// ls-remote returns SHAs only — without history walking we can say
// equal/unequal/missing-on-one-side, not ahead/behind. That's enough
// for "is the mirror in sync with what GiGot would push?" which is
// the only question this surface actually answers.
const (
	remoteRefSame       = "same"
	remoteRefDifferent  = "different"
	remoteRefOnlyLocal  = "only_local"
	remoteRefOnlyRemote = "only_remote"
)

// mirrorLsRemoteTimeout caps one ls-remote invocation. Same shape as
// mirrorPushTimeout; ls-remote is a strictly smaller op (refs only,
// no objects) so the same cap is generous.
const mirrorLsRemoteTimeout = 2 * time.Minute

// lsRemoteFn is the shape the server uses to invoke `git ls-remote`.
// Injected on Server alongside pushDest so tests can stub the network
// call without shelling out.
type lsRemoteFn func(ctx context.Context, destURL, secret string) (refs map[string]string, output []byte, err error)

// executeLsRemote is the real implementation wired into Server on
// boot. It writes the same one-shot askpass shim mirror.go uses,
// invokes `git ls-remote --refs <url>`, and parses the
// `<sha>\t<refname>` lines into a map. --refs strips peeled tags
// (`^{}` suffixes) so we don't have to filter them out manually.
func executeLsRemote(ctx context.Context, destURL, secret string) (map[string]string, []byte, error) {
	if destURL == "" {
		return nil, nil, fmt.Errorf("ls-remote: destination url required")
	}
	if secret == "" {
		return nil, nil, fmt.Errorf("ls-remote: credential secret required")
	}

	ask, err := os.CreateTemp("", "gigot-askpass-*.sh")
	if err != nil {
		return nil, nil, fmt.Errorf("ls-remote: askpass tempfile: %w", err)
	}
	askPath := ask.Name()
	defer os.Remove(askPath)
	if _, err := ask.WriteString(mirrorAskpassScript); err != nil {
		ask.Close()
		return nil, nil, fmt.Errorf("ls-remote: askpass write: %w", err)
	}
	if err := ask.Chmod(0o700); err != nil {
		ask.Close()
		return nil, nil, fmt.Errorf("ls-remote: askpass chmod: %w", err)
	}
	if err := ask.Close(); err != nil {
		return nil, nil, fmt.Errorf("ls-remote: askpass close: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, mirrorLsRemoteTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--refs", destURL)
	cmd.Env = append(os.Environ(),
		"GIT_ASKPASS="+askPath,
		"GIT_TERMINAL_PROMPT=0",
		"GIGOT_PUSH_USERNAME=x-access-token",
		"GIGOT_PUSH_PASSWORD="+secret,
	)
	out, runErr := cmd.CombinedOutput()
	out = redactSecret(out, secret)
	if runErr != nil {
		return nil, out, runErr
	}
	refs, parseErr := parseLsRemoteOutput(out)
	if parseErr != nil {
		return nil, out, parseErr
	}
	return refs, out, nil
}

// parseLsRemoteOutput decodes the canonical `<sha>\t<refname>\n` form
// emitted by git ls-remote. Lines that don't match are skipped (rather
// than failing the whole check) so a chatty remote that prepends a
// banner doesn't take the entire status read with it. Returned map is
// keyed by full refname (e.g. "refs/heads/main").
func parseLsRemoteOutput(out []byte) (map[string]string, error) {
	refs := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	// 1MiB line buffer covers any plausible refname + SHA line.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		// Expected: "<40-or-64-hex-sha>\t<refname>". A space-separated
		// variant is sometimes seen from older git; accept both.
		var sep int
		if i := strings.IndexByte(line, '\t'); i > 0 {
			sep = i
		} else if i := strings.IndexByte(line, ' '); i > 0 {
			sep = i
		} else {
			continue
		}
		sha := strings.TrimSpace(line[:sep])
		ref := strings.TrimSpace(line[sep+1:])
		if sha == "" || ref == "" {
			continue
		}
		// Belt-and-braces: ignore peeled-tag entries even if --refs
		// somehow let one through.
		if strings.HasSuffix(ref, "^{}") {
			continue
		}
		refs[ref] = sha
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ls-remote: parse: %w", err)
	}
	return refs, nil
}

// mirroredNamespaces lists the ref prefixes the mirror push actually
// touches (matches mirrorRefspecs in mirror.go). Status comparison
// scopes itself to these namespaces — refs the remote has under
// e.g. refs/pull/* on GitHub are noise from GiGot's perspective.
var mirroredNamespaces = []string{
	"refs/heads/",
	"refs/audit/",
}

func isMirroredRef(ref string) bool {
	for _, p := range mirroredNamespaces {
		if strings.HasPrefix(ref, p) {
			return true
		}
	}
	return false
}

// compareRefs computes the per-ref status table + an overall summary
// from a local and remote ref snapshot. Only refs in the mirrored
// namespaces (refs/heads/*, refs/audit/*) participate; anything else
// the remote happens to carry (refs/pull/*, refs/tags/*, vendor PR
// refs) is filtered out so the badge isn't misleading.
//
// Summary is in_sync iff every mirrored-namespace ref present on
// either side has state=same; otherwise diverged.
func compareRefs(local, remote map[string]string) (status string, refs []destinations.RemoteRefStatus) {
	seen := make(map[string]struct{})
	for r := range local {
		if isMirroredRef(r) {
			seen[r] = struct{}{}
		}
	}
	for r := range remote {
		if isMirroredRef(r) {
			seen[r] = struct{}{}
		}
	}
	names := make([]string, 0, len(seen))
	for r := range seen {
		names = append(names, r)
	}
	sort.Strings(names)

	allSame := true
	out := make([]destinations.RemoteRefStatus, 0, len(names))
	for _, ref := range names {
		l, hasL := local[ref]
		rm, hasR := remote[ref]
		var state string
		switch {
		case hasL && hasR && l == rm:
			state = remoteRefSame
		case hasL && hasR:
			state = remoteRefDifferent
			allSame = false
		case hasL && !hasR:
			state = remoteRefOnlyLocal
			allSame = false
		case !hasL && hasR:
			state = remoteRefOnlyRemote
			allSame = false
		}
		out = append(out, destinations.RemoteRefStatus{
			Ref:    ref,
			Local:  l,
			Remote: rm,
			State:  state,
		})
	}
	if allSame {
		return remoteStatusInSync, out
	}
	return remoteStatusDiverged, out
}

// repoDestination pairs a repo name with one of its destinations.
// The status poller iterates a flat slice of these so the iteration
// order is stable across enable/disable churn during one tick.
type repoDestination struct {
	Repo string
	Dest *destinations.Destination
}

// enabledDestinations returns every enabled destination across every
// repo, in repo-then-creation-order. Disabled destinations skip the
// background poll (matching the mirror_worker post-receive behaviour);
// the admin UI's manual Refresh button still hits them either way
// because manual is explicit operator intent.
func (s *Server) enabledDestinations() []repoDestination {
	repos, err := s.git.List()
	if err != nil {
		return nil
	}
	var out []repoDestination
	for _, name := range repos {
		for _, d := range s.destinations.All(name) {
			if !d.Enabled {
				continue
			}
			out = append(out, repoDestination{Repo: name, Dest: d})
		}
	}
	return out
}

// refreshRemoteStatus runs one ls-remote against the destination's
// URL, compares against the local mirrored namespaces, and writes the
// result onto the destination. Returns the updated destination on
// success; on a network/auth failure the destination is still updated
// with status="error" + the redacted git output, and a wrapped error
// is returned so the caller can decide whether to log it.
//
// Used by three call sites:
//   - manual Refresh button on /admin/repositories (handler)
//   - successful syncOnce (push-time piggyback — inferred without an
//     extra round-trip; see syncOnce for the rationale)
//   - background statusPoller (every cfg.Mirror.StatusPollSec seconds)
func (s *Server) refreshRemoteStatus(ctx context.Context, repo, id string) error {
	dest, err := s.destinations.Get(repo, id)
	if err != nil {
		return err
	}
	cred, err := s.credentials.Get(dest.CredentialName)
	if err != nil {
		// Credential vanished from the vault — record the error on the
		// destination so the admin sees it in the badge instead of
		// silently never refreshing.
		now := time.Now().UTC()
		_, _ = s.destinations.Update(repo, id, func(d *destinations.Destination) {
			d.RemoteStatus = remoteStatusError
			d.RemoteCheckedAt = &now
			d.RemoteCheckError = "credential " + dest.CredentialName + " is not in the vault"
			d.RemoteRefs = nil
		})
		if errors.Is(err, credentials.ErrNotFound) {
			return fmt.Errorf("credential %q not in vault", dest.CredentialName)
		}
		return err
	}

	remote, out, lsErr := s.lsRemote(ctx, dest.URL, cred.Secret)
	now := time.Now().UTC()
	if lsErr != nil {
		errText := strings.TrimSpace(string(out))
		if errText == "" {
			errText = lsErr.Error()
		}
		_, updErr := s.destinations.Update(repo, id, func(d *destinations.Destination) {
			d.RemoteStatus = remoteStatusError
			d.RemoteCheckedAt = &now
			d.RemoteCheckError = errText
			// Leave RemoteRefs in place — the previous successful check
			// is still informative even when the latest one failed.
		})
		if updErr != nil {
			return updErr
		}
		return fmt.Errorf("ls-remote: %w", lsErr)
	}

	local, err := s.localMirroredRefs(repo)
	if err != nil {
		return err
	}
	status, refs := compareRefs(local, remote)
	_, updErr := s.destinations.Update(repo, id, func(d *destinations.Destination) {
		d.RemoteStatus = status
		d.RemoteCheckedAt = &now
		d.RemoteCheckError = ""
		d.RemoteRefs = refs
	})
	return updErr
}

// markRemoteInSyncFromPush is the push-time piggyback: after a
// successful mirror push we know the mirrored namespaces on the
// remote now equal the local refs we just pushed (force-mirror
// refspecs guarantee it). Recording that inferred state here is free
// — no extra round-trip — and keeps the badge in step with the
// sync-now button. A subsequent ls-remote check (manual or via the
// poller) will overwrite this with an authoritative read.
func (s *Server) markRemoteInSyncFromPush(repo, id string) {
	local, err := s.localMirroredRefs(repo)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	refs := make([]destinations.RemoteRefStatus, 0, len(local))
	keys := make([]string, 0, len(local))
	for k := range local {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		refs = append(refs, destinations.RemoteRefStatus{
			Ref:    k,
			Local:  local[k],
			Remote: local[k],
			State:  remoteRefSame,
		})
	}
	_, _ = s.destinations.Update(repo, id, func(d *destinations.Destination) {
		d.RemoteStatus = remoteStatusInSync
		d.RemoteCheckedAt = &now
		d.RemoteCheckError = ""
		d.RemoteRefs = refs
	})
}

// localMirroredRefs returns the subset of refs/heads/* + refs/audit/*
// from the bare repo. Built on RefSnapshot (which already excludes
// refs/audit/*) plus a second for-each-ref for the audit namespace,
// keeping the mirror's two-namespace shape in one place.
func (s *Server) localMirroredRefs(repo string) (map[string]string, error) {
	heads, err := s.git.RefSnapshot(repo)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(heads))
	for r, sha := range heads {
		if isMirroredRef(r) {
			out[r] = sha
		}
	}
	cmd := exec.Command("git", "-C", s.git.RepoPath(repo),
		"for-each-ref", "--format=%(refname) %(objectname)", "refs/audit/")
	auditOut, err := cmd.Output()
	if err != nil {
		// Repo without an audit ref isn't an error — return what we have.
		return out, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(auditOut)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}

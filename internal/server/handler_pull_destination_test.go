package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubPull mirrors stubPush: every invocation is recorded so tests can
// assert the handler routed the right repo path, URL, and secret to
// the pull executor. Output + err let the caller pick happy or sad
// path per scenario.
type pullCall struct {
	repoPath string
	destURL  string
	secret   string
}

func stubPull(calls *[]pullCall, out []byte, err error) pullDestinationFn {
	return func(_ context.Context, repoPath, destURL, secret string) ([]byte, error) {
		*calls = append(*calls, pullCall{repoPath: repoPath, destURL: destURL, secret: secret})
		return out, err
	}
}

// TestPullDestination_AdminSuccess covers the admin happy path: the
// admin POSTs /pull, the handler resolves the credential, calls the
// stubbed fetch with the right args, and returns the destination view
// with remote_status flipped to in_sync. Touch on the credential also
// fires (last-used bookkeeping mirrors the push side).
func TestPullDestination_AdminSuccess(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	var calls []pullCall
	srv.pullDest = stubPull(&calls, []byte("ok"), nil)

	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dest want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	rec = do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/pull", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("pull want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var pulled DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &pulled); err != nil {
		t.Fatal(err)
	}
	// Pull must NOT touch LastSync* — those belong to the push
	// direction. Verifying the field is unchanged from the create.
	if pulled.LastSyncStatus != created.LastSyncStatus {
		t.Errorf("pull should not touch last_sync_status; was %q, now %q",
			created.LastSyncStatus, pulled.LastSyncStatus)
	}
	// Pull SHOULD mark the remote as in-sync — local just became
	// equal to remote by construction.
	if pulled.RemoteStatus != remoteStatusInSync {
		t.Errorf("pull should mark remote_status=in_sync, got %q", pulled.RemoteStatus)
	}

	if len(calls) != 1 {
		t.Fatalf("pullDest call count: want 1, got %d", len(calls))
	}
	got := calls[0]
	if got.destURL != "https://github.com/alice/addresses.git" {
		t.Errorf("destURL: got %q", got.destURL)
	}
	if got.secret != "ghp_x" {
		t.Errorf("secret: want secret from vault %q, got %q", "ghp_x", got.secret)
	}
	if got.repoPath != srv.git.RepoPath("addresses") {
		t.Errorf("repoPath: want %q, got %q", srv.git.RepoPath("addresses"), got.repoPath)
	}

	// credentials.Touch on success: LastUsed should now be populated.
	cred, err := srv.credentials.Get("github-personal")
	if err != nil {
		t.Fatal(err)
	}
	if cred.LastUsed == nil {
		t.Error("credentials.Touch should have populated LastUsed on a successful pull")
	}
}

// TestPullDestination_FetchFailureReturns502 — the fetch failed
// upstream; local state is fine. 502 (bad gateway) is the right code
// because the local server passed the gate, ran the lookup, and only
// the destination side broke. Body should carry the redacted git
// output so the operator can diagnose. Pull MUST NOT touch LastSync*
// or remote_status on failure (those are still describing the prior
// push state).
func TestPullDestination_FetchFailureReturns502(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	var calls []pullCall
	srv.pullDest = stubPull(&calls,
		[]byte("fatal: could not read Username for 'https://github.com'"),
		errors.New("exit status 128"))

	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	var created DestinationView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	rec = do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/pull", nil, sess)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("pull with failing fetch want 502, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "could not read Username") {
		t.Errorf("502 body should include redacted git output, got %s", rec.Body.String())
	}

	// Touch should NOT fire on a failed pull.
	cred, err := srv.credentials.Get("github-personal")
	if err != nil {
		t.Fatal(err)
	}
	if cred.LastUsed != nil {
		t.Error("credentials.Touch should NOT fire on a failed pull")
	}
}

// TestPullDestination_CredentialGoneReturns409 — symmetric to the push
// 409 scenario. Direct store surgery deletes the credential out from
// under the destination; /pull should respond 409, not 500.
func TestPullDestination_CredentialGoneReturns409(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	var created DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if err := srv.credentials.Remove("github-personal"); err != nil {
		t.Fatal(err)
	}

	rec = do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/pull", nil, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("pull with missing credential want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPullDestination_MissingDestinationReturns404 — typo-resistance
// on the {id} segment, symmetric to the push 404.
func TestPullDestination_MissingDestinationReturns404(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/does-not-exist/pull", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("pull with unknown id want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPullDestination_SubscriberSurfaceHasNoPullRoute — the
// load-bearing admin-only invariant. A subscriber token holding the
// mirror ability can hit /sync, but /pull is not registered on the
// subscriber dispatcher at all. The request reaches the action switch
// and falls through to "unknown destination action" → 404. Even with
// the strongest subscriber token shape we can issue, the route must
// not exist.
func TestPullDestination_SubscriberSurfaceHasNoPullRoute(t *testing.T) {
	srv := subscriberTestServer(t)
	token, err := srv.tokenStrategy.Issue("alice", "addresses", []string{"mirror"})
	if err != nil {
		t.Fatal(err)
	}
	var calls []pullCall
	srv.pullDest = stubPull(&calls, []byte("ok"), nil)

	// Create a real destination as admin so we have a valid id and
	// can rule out "id-not-found" as the reason for the 404.
	adminSrv, sess := adminTestServer(t)
	if err := adminSrv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, adminSrv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	var created DestinationView
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	req := bearer(t, http.MethodPost,
		"/api/repos/addresses/destinations/"+created.ID+"/pull", nil, token)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("subscriber surface /pull want 404 (no such action), got %d body=%s",
			rec2.Code, rec2.Body.String())
	}
	if len(calls) != 0 {
		t.Errorf("pullDest must NOT fire from subscriber surface; got %d calls", len(calls))
	}
}

// TestPullDestination_WithoutAdminSessionReturns401 — pull lives only
// behind the admin-session gate. Unauthenticated requests bounce off
// requireAdminSession at the top of handleAdminRepoDestinations
// before any routing or destination lookup.
func TestPullDestination_WithoutAdminSessionReturns401(t *testing.T) {
	srv, _ := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	// No session cookie passed.
	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/any-id/pull", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("pull without session want 401, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestPullDestination_WrongMethodIsNotFound — symmetric to the push
// /sync wrong-method case. GET /pull should 404 (unknown action),
// not 405, because the dispatcher can't distinguish "valid action,
// wrong method" from "unknown action".
func TestPullDestination_WrongMethodIsNotFound(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodGet,
		"/api/admin/repos/addresses/destinations/whatever/pull", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /pull want 404 (unknown action), got %d body=%s", rec.Code, rec.Body.String())
	}
}

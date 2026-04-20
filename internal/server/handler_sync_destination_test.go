package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// stubPushFactory returns a pushDestinationFn that records every
// invocation and returns a caller-chosen output + error. The recorded
// calls let tests assert the handler actually invoked the push with
// the right repo path, URL, and secret pulled from the vault.
type pushCall struct {
	repoPath string
	destURL  string
	secret   string
}

func stubPush(calls *[]pushCall, out []byte, err error) pushDestinationFn {
	return func(_ context.Context, repoPath, destURL, secret string) ([]byte, error) {
		*calls = append(*calls, pushCall{repoPath: repoPath, destURL: destURL, secret: secret})
		return out, err
	}
}

// TestSyncDestination_AdminSuccess covers the admin-session path: a
// POST to /sync runs the push, updates last_sync_* to ok, and touches
// the credential. Stubbed push so the test doesn't shell out to git.
func TestSyncDestination_AdminSuccess(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	var calls []pushCall
	srv.pushDest = stubPush(&calls, []byte("ok"), nil)

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
		"/api/admin/repos/addresses/destinations/"+created.ID+"/sync", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync want 200, got %d body=%s", rec.Code, rec.Body.String())
	}

	var synced DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &synced); err != nil {
		t.Fatal(err)
	}
	if synced.LastSyncStatus != syncStatusOK {
		t.Fatalf("want status=%q after success, got %q", syncStatusOK, synced.LastSyncStatus)
	}
	if synced.LastSyncAt == nil {
		t.Fatal("last_sync_at should be set after success")
	}
	if synced.LastSyncError != "" {
		t.Fatalf("last_sync_error should be empty on success, got %q", synced.LastSyncError)
	}

	if len(calls) != 1 {
		t.Fatalf("pushDest call count: want 1, got %d", len(calls))
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
		t.Error("credentials.Touch should have populated LastUsed on success")
	}
}

// TestSyncDestination_AdminFailurePopulatesError proves that a failing
// push writes status=error plus the captured output into
// last_sync_error, and does NOT touch the credential's LastUsed.
func TestSyncDestination_AdminFailurePopulatesError(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	var calls []pushCall
	srv.pushDest = stubPush(&calls,
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
		"/api/admin/repos/addresses/destinations/"+created.ID+"/sync", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync with failing push should still 200 (status is on the body), got %d", rec.Code)
	}
	var synced DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &synced); err != nil {
		t.Fatal(err)
	}
	if synced.LastSyncStatus != syncStatusError {
		t.Fatalf("status: want %q, got %q", syncStatusError, synced.LastSyncStatus)
	}
	if synced.LastSyncError == "" {
		t.Error("last_sync_error should carry the captured push output on failure")
	}

	cred, err := srv.credentials.Get("github-personal")
	if err != nil {
		t.Fatal(err)
	}
	if cred.LastUsed != nil {
		t.Error("credentials.Touch should NOT fire on a failed push")
	}
}

// TestSyncDestination_SubscriberSuccess covers the token path with the
// mirror ability — the identical handler runs, gated by
// TokenAbilityPolicy instead of the admin session.
func TestSyncDestination_SubscriberSuccess(t *testing.T) {
	srv := subscriberTestServer(t)
	token, err := srv.tokenStrategy.Issue("alice", []string{"addresses"}, []string{"mirror"})
	if err != nil {
		t.Fatal(err)
	}
	var calls []pushCall
	srv.pushDest = stubPush(&calls, []byte("ok"), nil)

	req := bearer(t, http.MethodPost, "/api/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	var created DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	req = bearer(t, http.MethodPost,
		"/api/repos/addresses/destinations/"+created.ID+"/sync", nil, token)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("subscriber sync want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 {
		t.Fatalf("push should have fired once, got %d calls", len(calls))
	}
}

// TestSyncDestination_SubscriberWithoutMirrorDenied — same ability gate
// as the rest of the subscriber surface. Without `mirror`, sync is 403.
func TestSyncDestination_SubscriberWithoutMirrorDenied(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	// Admin creates a destination so we have an id to aim at.
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
		}, sess)
	var created DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	srv.auth.SetEnabled(true)
	token, err := srv.tokenStrategy.Issue("bob", []string{"addresses"}, nil) // no abilities
	if err != nil {
		t.Fatal(err)
	}

	req := bearer(t, http.MethodPost,
		"/api/repos/addresses/destinations/"+created.ID+"/sync", nil, token)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("subscriber sync without mirror want 403, got %d body=%s", rec2.Code, rec2.Body.String())
	}
}

// TestSyncDestination_DisabledStillSyncs — enabled=false gates the
// post-receive fan-out (slice 2b), not an explicit operator action.
// A manual sync runs regardless so an operator can test a destination
// before flipping it live.
func TestSyncDestination_DisabledStillSyncs(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	var calls []pushCall
	srv.pushDest = stubPush(&calls, []byte("ok"), nil)

	disabled := false
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/addresses/destinations",
		map[string]any{
			"url":             "https://github.com/alice/addresses.git",
			"credential_name": "github-personal",
			"enabled":         &disabled,
		}, sess)
	var created DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Enabled {
		t.Fatalf("precondition: dest should be disabled, got enabled=%v", created.Enabled)
	}

	rec = do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/sync", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("manual sync of disabled dest want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if len(calls) != 1 {
		t.Fatalf("push should have fired once even for disabled dest, got %d", len(calls))
	}
}

// TestSyncDestination_CredentialGoneReturns409 — if the credential
// referenced by a destination has been deleted out from under it, the
// sync endpoint should return 409 rather than 500. Usually this can't
// happen because credential deletion is blocked by Refs(), but it can
// arise via direct store surgery or a race.
func TestSyncDestination_CredentialGoneReturns409(t *testing.T) {
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
	// Bypass the Refs() guard directly at the store layer.
	if err := srv.credentials.Remove("github-personal"); err != nil {
		t.Fatal(err)
	}

	rec = do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/sync", nil, sess)
	if rec.Code != http.StatusConflict {
		t.Fatalf("sync with missing credential want 409, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSyncDestination_MissingDestinationReturns404 — typo-resistance on
// the {id} segment.
func TestSyncDestination_MissingDestinationReturns404(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/does-not-exist/sync", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("sync with unknown id want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSyncDestination_WrongMethodIsNotFound — the /sync suffix only
// accepts POST. GET etc. are surfaced as "unknown destination action"
// (404) rather than 405, because the router can't distinguish "valid
// action, wrong method" from "unknown action".
func TestSyncDestination_WrongMethodIsNotFound(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodGet,
		"/api/admin/repos/addresses/destinations/whatever/sync", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /sync want 404 (unknown action), got %d body=%s", rec.Code, rec.Body.String())
	}
}

package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

// sessionCookie is the type returned by adminTestServer for the
// session arg. Aliased here so the helpers below don't drift from
// the existing test plumbing.

// stubLsRemote returns an lsRemoteFn that records every call and
// returns caller-chosen output. Same shape as stubPush — gives tests
// deterministic control over what `git ls-remote` "says" the remote
// holds without touching the network.
type lsRemoteCall struct {
	destURL string
	secret  string
}

func stubLsRemote(calls *[]lsRemoteCall, refs map[string]string, out []byte, err error) lsRemoteFn {
	return func(_ context.Context, destURL, secret string) (map[string]string, []byte, error) {
		*calls = append(*calls, lsRemoteCall{destURL: destURL, secret: secret})
		return refs, out, err
	}
}

// TestRefreshStatus_InSync — happy path: ls-remote returns the same
// refs the local repo has, so the destination flips to remote_status
// "in_sync" with one same-state row per ref. Bare-repo with no
// commits has no local refs/heads/*, so we use a remote that also
// has no mirrored refs — both empty equals in_sync.
func TestRefreshStatus_InSync(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	created := createDestForTest(t, srv, sess, "addresses", "https://github.com/alice/addresses.git", "github-personal")

	var calls []lsRemoteCall
	srv.lsRemote = stubLsRemote(&calls, map[string]string{}, []byte(""), nil)

	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/status/refresh", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RemoteStatus != "in_sync" {
		t.Fatalf("RemoteStatus: want in_sync, got %q", got.RemoteStatus)
	}
	if got.RemoteCheckedAt == nil {
		t.Fatal("RemoteCheckedAt should be set after a check")
	}
	if got.RemoteCheckError != "" {
		t.Errorf("RemoteCheckError should be empty on success, got %q", got.RemoteCheckError)
	}
	if len(calls) != 1 {
		t.Fatalf("ls-remote call count: want 1, got %d", len(calls))
	}
	if calls[0].secret != "ghp_x" {
		t.Errorf("ls-remote secret: want ghp_x from vault, got %q", calls[0].secret)
	}
	if calls[0].destURL != "https://github.com/alice/addresses.git" {
		t.Errorf("ls-remote destURL: got %q", calls[0].destURL)
	}
}

// TestRefreshStatus_Diverged — ls-remote claims a ref the local
// repo doesn't have. Summary flips to diverged, the per-ref entry is
// state=only_remote.
func TestRefreshStatus_Diverged(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	created := createDestForTest(t, srv, sess, "addresses", "https://github.com/alice/addresses.git", "github-personal")

	var calls []lsRemoteCall
	srv.lsRemote = stubLsRemote(&calls,
		map[string]string{"refs/heads/main": "deadbeef"}, nil, nil)

	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/status/refresh", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RemoteStatus != "diverged" {
		t.Fatalf("RemoteStatus: want diverged, got %q", got.RemoteStatus)
	}
	if len(got.RemoteRefs) != 1 {
		t.Fatalf("RemoteRefs len: want 1, got %d (%v)", len(got.RemoteRefs), got.RemoteRefs)
	}
	r := got.RemoteRefs[0]
	if r.Ref != "refs/heads/main" || r.State != "only_remote" || r.Remote != "deadbeef" {
		t.Errorf("ref entry: %+v", r)
	}
}

// TestRefreshStatus_LsRemoteFails — auth/network failure. Endpoint
// returns 502 AND the destination is updated with status=error so
// the badge in the admin UI reflects it.
func TestRefreshStatus_LsRemoteFails(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	created := createDestForTest(t, srv, sess, "addresses", "https://github.com/alice/addresses.git", "github-personal")

	var calls []lsRemoteCall
	srv.lsRemote = stubLsRemote(&calls, nil,
		[]byte("fatal: Authentication failed"), errors.New("exit status 128"))

	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/status/refresh", nil, sess)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("ls-remote failure want 502, got %d body=%s", rec.Code, rec.Body.String())
	}

	// The error response doesn't carry the updated dest — refetch
	// directly to confirm the badge state is persisted.
	d, err := srv.destinations.Get("addresses", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.RemoteStatus != "error" {
		t.Fatalf("persisted RemoteStatus: want error, got %q", d.RemoteStatus)
	}
	if d.RemoteCheckError == "" {
		t.Error("RemoteCheckError should be populated on failure")
	}
	if d.RemoteCheckedAt == nil {
		t.Error("RemoteCheckedAt should be set even on failure")
	}
}

// TestRefreshStatus_MissingDestinationReturns404 — typo on the {id}
// segment must not 500.
func TestRefreshStatus_MissingDestinationReturns404(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/nope/status/refresh", nil, sess)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestSyncDestination_MarksRemoteInSyncOnSuccess — the push-time
// piggyback: a successful Sync-now must leave the destination with
// remote_status="in_sync" too, since the force-mirror refspec
// guarantees the remote's mirrored namespaces now equal the local
// ones we just pushed. Otherwise the remote-status badge would still
// say "not yet checked" right after a fresh push, which would be
// confusing.
func TestSyncDestination_MarksRemoteInSyncOnSuccess(t *testing.T) {
	srv, sess := adminTestServer(t)
	if err := srv.git.InitBare("addresses"); err != nil {
		t.Fatal(err)
	}
	created := createDestForTest(t, srv, sess, "addresses", "https://github.com/alice/addresses.git", "github-personal")

	var calls []pushCall
	srv.pushDest = stubPush(&calls, []byte("ok"), nil)

	rec := do(t, srv, http.MethodPost,
		"/api/admin/repos/addresses/destinations/"+created.ID+"/sync", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var got DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.RemoteStatus != "in_sync" {
		t.Fatalf("post-push RemoteStatus: want in_sync, got %q", got.RemoteStatus)
	}
	if got.RemoteCheckedAt == nil {
		t.Error("RemoteCheckedAt should be set after a successful push")
	}
}

// createDestForTest is a small helper to keep the new tests focused on
// the status surface rather than re-asserting the create plumbing. It
// returns the created destination view; assertions that the create
// call itself works live in handler_admin_destinations_test.go.
func createDestForTest(t *testing.T, srv *Server, sess *http.Cookie, repo, url, credName string) DestinationView {
	t.Helper()
	rec := do(t, srv, http.MethodPost, "/api/admin/repos/"+repo+"/destinations",
		map[string]any{"url": url, "credential_name": credName}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create dest want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var d DestinationView
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

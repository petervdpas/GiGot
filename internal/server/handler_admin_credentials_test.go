package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestCredentials_CreateWithExpires_RoundTrip pins the wire contract
// the /admin/credentials form relies on: POST {expires: <ISO>} must
// come back unchanged on the list view, so the amber/red column can
// trust the value the server hands it.
func TestCredentials_CreateWithExpires_RoundTrip(t *testing.T) {
	srv, sess := adminTestServer(t)

	expires := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)
	body := map[string]any{
		"name":    "gh-with-expiry",
		"kind":    "pat",
		"secret":  "ghp_1",
		"expires": expires.Format(time.RFC3339),
	}
	rec := do(t, srv, http.MethodPost, "/api/admin/credentials", body, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
	}
	var created CredentialView
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Expires == nil || !created.Expires.Equal(expires) {
		t.Fatalf("POST Expires = %v, want %v", created.Expires, expires)
	}

	rec = do(t, srv, http.MethodGet, "/api/admin/credentials", nil, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: want 200, got %d", rec.Code)
	}
	var listed CredentialListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	var found *CredentialView
	for i, v := range listed.Credentials {
		if v.Name == "gh-with-expiry" {
			found = &listed.Credentials[i]
			break
		}
	}
	if found == nil {
		t.Fatal("gh-with-expiry missing from list")
	}
	if found.Expires == nil || !found.Expires.Equal(expires) {
		t.Fatalf("list Expires = %v, want %v", found.Expires, expires)
	}
}

// TestCredentials_CreateWithoutExpires_OmitsField keeps the optional
// semantics honest: blank date input on the form means the server
// stores nil, and the GET view omits the key entirely.
func TestCredentials_CreateWithoutExpires_OmitsField(t *testing.T) {
	srv, sess := adminTestServer(t)

	body := map[string]any{"name": "no-expiry", "kind": "pat", "secret": "ghp_x"}
	rec := do(t, srv, http.MethodPost, "/api/admin/credentials", body, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d body=%s", rec.Code, rec.Body.String())
	}

	// Assert the raw body does not carry the key — the UI classifier
	// treats missing and "" the same, but the wire shape matters for
	// any future consumer that distinguishes nil from zero time.
	if got := rec.Body.String(); containsKey(got, `"expires"`) {
		t.Fatalf("unexpected expires in response: %s", got)
	}
}

// TestCredentials_PatchExpires pins that PATCH {expires: <ISO>}
// updates the stored value without touching anything else. The admin
// API supports this today even though the UI currently only exposes
// create; keeping the regression fence means the door stays open for
// a later "Edit expiry" affordance without silent breakage.
func TestCredentials_PatchExpires(t *testing.T) {
	srv, sess := adminTestServer(t)

	// Seed.
	rec := do(t, srv, http.MethodPost, "/api/admin/credentials",
		map[string]any{"name": "to-patch", "kind": "pat", "secret": "s1"}, sess)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed: want 201, got %d", rec.Code)
	}

	newExp := time.Date(2028, 6, 15, 0, 0, 0, 0, time.UTC)
	rec = do(t, srv, http.MethodPatch, "/api/admin/credentials/to-patch",
		map[string]any{"expires": newExp.Format(time.RFC3339)}, sess)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var patched CredentialView
	if err := json.Unmarshal(rec.Body.Bytes(), &patched); err != nil {
		t.Fatal(err)
	}
	if patched.Expires == nil || !patched.Expires.Equal(newExp) {
		t.Fatalf("PATCH Expires = %v, want %v", patched.Expires, newExp)
	}
	if patched.Kind != "pat" {
		t.Fatalf("kind drifted on expires-only patch: %q", patched.Kind)
	}
}

// containsKey is a tiny helper that avoids pulling in strings.Contains
// semantics into the assertion — keeps intent readable.
func containsKey(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

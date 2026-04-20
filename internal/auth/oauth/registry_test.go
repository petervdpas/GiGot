package oauth

import (
	"context"
	"testing"
)

// stubRegProvider is a tiny Provider impl for exercising Registry
// mutation semantics — no network, no discovery, just enough surface
// for the map writes.
type stubRegProvider struct{ name string }

func (p *stubRegProvider) Name() string         { return p.name }
func (p *stubRegProvider) DisplayName() string  { return p.name }
func (p *stubRegProvider) AllowsRegister() bool { return false }
func (p *stubRegProvider) AuthURL(redirectURI, state, nonce, cc string) string {
	return "https://stub.example/" + state
}
func (p *stubRegProvider) ExchangeAndClaim(_ context.Context, _, _, _, _ string) (Claim, error) {
	return Claim{}, nil
}

// Replace on a fresh name acts as insert (Registry is a map, not a
// list). Verifying this because ReloadAuth's "enable a provider for
// the first time" path depends on it.
func TestRegistry_ReplaceInserts(t *testing.T) {
	r := RegistryForTest(nil)
	p := &stubRegProvider{name: "github"}
	r.Replace("github", p)

	got := r.Get("github")
	if got != p {
		t.Fatalf("Replace did not insert: got %v, want %v", got, p)
	}
}

// Replace on an existing name swaps the pointer — the next Get sees
// the new instance. Concurrent handleOAuthLogin callers already
// holding a reference to the old one finish against that snapshot
// (that contract is documented on Replace; this test only asserts
// the post-swap read sees the new pointer).
func TestRegistry_ReplaceSwaps(t *testing.T) {
	r := RegistryForTest(map[string]Provider{"github": &stubRegProvider{name: "old"}})
	newP := &stubRegProvider{name: "new"}
	r.Replace("github", newP)
	if got := r.Get("github"); got != newP {
		t.Fatalf("Get after Replace returned the old provider")
	}
}

// Replace(name, nil) is documented as equivalent to Remove(name).
// Tested explicitly because the ReloadAuth handler-side relies on
// that shorthand when disabling a provider.
func TestRegistry_ReplaceWithNilRemoves(t *testing.T) {
	r := RegistryForTest(map[string]Provider{"github": &stubRegProvider{name: "x"}})
	r.Replace("github", nil)
	if r.Get("github") != nil {
		t.Fatal("Replace(name, nil) should remove the provider")
	}
}

func TestRegistry_Remove(t *testing.T) {
	r := RegistryForTest(map[string]Provider{"github": &stubRegProvider{name: "x"}})
	if !r.Remove("github") {
		t.Fatal("Remove should return true on an existing key")
	}
	if r.Remove("github") {
		t.Error("Remove on a missing key should return false")
	}
	if r.Get("github") != nil {
		t.Error("Get after Remove should return nil")
	}
}

// Providers() still returns a stable [github, entra, microsoft] order
// regardless of insertion order. Guards against a future refactor
// accidentally mutating to a Go-map-random order — the login page
// button order is user-visible and should be deterministic.
func TestRegistry_ProvidersStableOrder(t *testing.T) {
	r := RegistryForTest(nil)
	// Insert in reverse-canonical order.
	r.Replace("microsoft", &stubRegProvider{name: "microsoft"})
	r.Replace("entra", &stubRegProvider{name: "entra"})
	r.Replace("github", &stubRegProvider{name: "github"})

	got := r.Providers()
	want := []string{"github", "entra", "microsoft"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, p := range got {
		if p.Name() != want[i] {
			t.Errorf("position %d = %q, want %q", i, p.Name(), want[i])
		}
	}
}

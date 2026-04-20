package policy

import (
	"context"
	"testing"

	"github.com/petervdpas/GiGot/internal/auth"
)

func TestAllowAuthenticated_AllowsAnyIdentity(t *testing.T) {
	p := AllowAuthenticated{}
	id := &auth.Identity{Username: "alice", Provider: "token"}
	for _, a := range []Action{ActionReadRepo, ActionWriteRepo, ActionManageTokens, ActionManageAdmins, ActionManageRepos} {
		d := p.Decide(context.Background(), id, a, "any-repo")
		if !d.Allowed {
			t.Fatalf("action %q should be allowed for authenticated caller, got %+v", a, d)
		}
	}
}

func TestAllowAuthenticated_DeniesAnonymous(t *testing.T) {
	p := AllowAuthenticated{}
	d := p.Decide(context.Background(), nil, ActionReadRepo, "repo")
	if d.Allowed {
		t.Fatal("anonymous caller should be denied")
	}
	if d.Reason == "" {
		t.Fatal("deny decision should include a reason")
	}
}

func TestDenyAll_RejectsEverything(t *testing.T) {
	p := DenyAll{}
	id := &auth.Identity{Username: "alice"}
	for _, a := range []Action{ActionReadRepo, ActionWriteRepo, ActionManageTokens} {
		if p.Decide(context.Background(), id, a, "any").Allowed {
			t.Fatalf("DenyAll should deny %q", a)
		}
	}
}

func TestAllowHelper(t *testing.T) {
	d := Allow()
	if !d.Allowed {
		t.Fatal("Allow() should produce Allowed=true")
	}
}

func TestDenyHelper(t *testing.T) {
	d := Deny("nope")
	if d.Allowed {
		t.Fatal("Deny() should produce Allowed=false")
	}
	if d.Reason != "nope" {
		t.Fatalf("got reason %q, want %q", d.Reason, "nope")
	}
}

func withToken(entry *auth.TokenEntry) context.Context {
	return auth.WithTokenEntry(context.Background(), entry)
}

func TestTokenRepoPolicy_AdminSessionAllowsEverything(t *testing.T) {
	p := TokenRepoPolicy{}
	id := &auth.Identity{Username: "peter", Provider: ProviderSession}
	for _, a := range []Action{ActionReadRepo, ActionWriteRepo, ActionManageRepos, ActionManageTokens, ActionManageAdmins} {
		if !p.Decide(context.Background(), id, a, "any").Allowed {
			t.Fatalf("admin session should be allowed for %q", a)
		}
	}
}

func TestTokenRepoPolicy_AuthDisabledAllowsEverything(t *testing.T) {
	p := TokenRepoPolicy{}
	id := &auth.Identity{Username: "auth-disabled", Provider: ProviderAuthDisabled}
	if !p.Decide(context.Background(), id, ActionWriteRepo, "any").Allowed {
		t.Fatal("auth-disabled (dev) mode should be allowed for write actions")
	}
}

func TestTokenRepoPolicy_TokenAllowsAssignedRepo(t *testing.T) {
	p := TokenRepoPolicy{}
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	ctx := withToken(&auth.TokenEntry{Token: "t", Username: "alice", Repos: []string{"my-templates"}})

	if !p.Decide(ctx, id, ActionReadRepo, "my-templates").Allowed {
		t.Fatal("token should be allowed on its assigned repo")
	}
	if !p.Decide(ctx, id, ActionWriteRepo, "my-templates").Allowed {
		t.Fatal("token should be allowed to write its assigned repo")
	}
}

func TestTokenRepoPolicy_TokenDeniesUnassignedRepo(t *testing.T) {
	p := TokenRepoPolicy{}
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	ctx := withToken(&auth.TokenEntry{Token: "t", Username: "alice", Repos: []string{"my-templates"}})

	if p.Decide(ctx, id, ActionReadRepo, "someone-elses-repo").Allowed {
		t.Fatal("token should be denied on an unassigned repo")
	}
}

func TestTokenRepoPolicy_TokenDeniesManagementActions(t *testing.T) {
	p := TokenRepoPolicy{}
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	ctx := withToken(&auth.TokenEntry{Token: "t", Username: "alice", Repos: []string{"r"}})

	for _, a := range []Action{ActionManageRepos, ActionManageTokens, ActionManageAdmins} {
		if p.Decide(ctx, id, a, "").Allowed {
			t.Fatalf("token caller should not be allowed management action %q", a)
		}
	}
}

func TestTokenRepoPolicy_TokenListingRequiresRepos(t *testing.T) {
	p := TokenRepoPolicy{}
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}

	withNone := withToken(&auth.TokenEntry{Token: "t", Username: "alice"})
	if p.Decide(withNone, id, ActionReadRepo, "").Allowed {
		t.Fatal("listing should be denied when token has no repos assigned")
	}

	withOne := withToken(&auth.TokenEntry{Token: "t", Username: "alice", Repos: []string{"r"}})
	if !p.Decide(withOne, id, ActionReadRepo, "").Allowed {
		t.Fatal("listing should be allowed when token has any repo assigned")
	}
}

func TestTokenRepoPolicy_MissingTokenEntryDenies(t *testing.T) {
	p := TokenRepoPolicy{}
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	if p.Decide(context.Background(), id, ActionReadRepo, "repo").Allowed {
		t.Fatal("missing token entry in context should deny")
	}
}

func TestTokenAbilityPolicy_DeniesAnonymous(t *testing.T) {
	p := NewTokenAbilityPolicy("mirror")
	if p.Decide(context.Background(), nil, ActionReadRepo, "").Allowed {
		t.Fatal("anonymous caller should be denied")
	}
}

func TestTokenAbilityPolicy_AdminSessionBypasses(t *testing.T) {
	p := NewTokenAbilityPolicy("mirror")
	id := &auth.Identity{Username: "peter", Provider: ProviderSession}
	// Admin holds no token at all — bypass should still allow.
	if !p.Decide(context.Background(), id, ActionReadRepo, "").Allowed {
		t.Fatal("admin session should bypass ability gate")
	}
}

func TestTokenAbilityPolicy_AuthDisabledBypasses(t *testing.T) {
	p := NewTokenAbilityPolicy("mirror")
	id := &auth.Identity{Username: "dev", Provider: ProviderAuthDisabled}
	if !p.Decide(context.Background(), id, ActionReadRepo, "").Allowed {
		t.Fatal("auth-disabled (dev) mode should bypass ability gate")
	}
}

func TestTokenAbilityPolicy_TokenWithAbilityAllowed(t *testing.T) {
	p := NewTokenAbilityPolicy("mirror")
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	ctx := withToken(&auth.TokenEntry{
		Token: "t", Username: "alice", Abilities: []string{"mirror"},
	})
	if !p.Decide(ctx, id, ActionReadRepo, "").Allowed {
		t.Fatal("token with mirror ability should be allowed")
	}
}

func TestTokenAbilityPolicy_TokenWithoutAbilityDenied(t *testing.T) {
	p := NewTokenAbilityPolicy("mirror")
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	ctx := withToken(&auth.TokenEntry{
		Token: "t", Username: "alice", Abilities: []string{"some-other"},
	})
	d := p.Decide(ctx, id, ActionReadRepo, "")
	if d.Allowed {
		t.Fatal("token without mirror ability should be denied")
	}
	if d.Reason == "" {
		t.Fatal("deny decision should carry a reason")
	}
}

func TestTokenAbilityPolicy_TokenWithNoAbilitiesDenied(t *testing.T) {
	p := NewTokenAbilityPolicy("mirror")
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	ctx := withToken(&auth.TokenEntry{Token: "t", Username: "alice"})
	if p.Decide(ctx, id, ActionReadRepo, "").Allowed {
		t.Fatal("token with no abilities at all should be denied")
	}
}

func TestTokenAbilityPolicy_MissingTokenEntryDenies(t *testing.T) {
	p := NewTokenAbilityPolicy("mirror")
	id := &auth.Identity{Username: "alice", Provider: ProviderToken}
	// No WithTokenEntry on the context — exercises the missing-entry branch.
	if p.Decide(context.Background(), id, ActionReadRepo, "").Allowed {
		t.Fatal("missing token entry should deny")
	}
}

func TestTokenAbilityPolicy_EmptyAbilityNamePanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("constructing with empty ability name should panic")
		}
	}()
	_ = NewTokenAbilityPolicy("")
}

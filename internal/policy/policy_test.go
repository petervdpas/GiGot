package policy

import (
	"testing"

	"github.com/petervdpas/GiGot/internal/auth"
)

func TestAllowAuthenticated_AllowsAnyIdentity(t *testing.T) {
	p := AllowAuthenticated{}
	id := &auth.Identity{Username: "alice", Provider: "token"}
	for _, a := range []Action{ActionReadRepo, ActionWriteRepo, ActionManageTokens, ActionManageAdmins, ActionManageRepos} {
		d := p.Decide(id, a, "any-repo")
		if !d.Allowed {
			t.Fatalf("action %q should be allowed for authenticated caller, got %+v", a, d)
		}
	}
}

func TestAllowAuthenticated_DeniesAnonymous(t *testing.T) {
	p := AllowAuthenticated{}
	d := p.Decide(nil, ActionReadRepo, "repo")
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
		if p.Decide(id, a, "any").Allowed {
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

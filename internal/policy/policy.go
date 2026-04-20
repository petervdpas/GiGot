// Package policy is the single place that decides whether an authenticated
// identity may perform an action on a resource. Handlers should never make
// ad-hoc authorisation decisions — they call Evaluator.Decide and honour the
// result. New authorisation rules (per-repo allowlists, read-vs-write, etc.)
// are added here by swapping in a different Evaluator implementation, not by
// threading new fields through every DTO.
package policy

import (
	"context"

	"github.com/petervdpas/GiGot/internal/auth"
)

// Action names a thing the caller wants to do. Add new actions as the server
// grows; keep names stable because they appear in logs.
type Action string

const (
	ActionReadRepo     Action = "repo.read"
	ActionWriteRepo    Action = "repo.write"
	ActionManageRepos  Action = "repo.manage"
	ActionManageTokens Action = "admin.tokens"
	ActionManageAdmins Action = "admin.admins"
)

// Decision is the result of evaluating a policy.
type Decision struct {
	Allowed bool
	Reason  string // populated on deny; used for log/debug, not for clients
}

// Allow is a convenience for evaluators that want to return an allow decision.
func Allow() Decision { return Decision{Allowed: true} }

// Deny is a convenience for evaluators that want to return a deny decision.
func Deny(reason string) Decision { return Decision{Allowed: false, Reason: reason} }

// Evaluator decides whether an identity may perform an action on a resource.
// A nil identity means "not authenticated"; evaluators decide how to treat it.
// resource is the action-specific target (typically a repo name); pass "" if
// not applicable. The context carries provider-specific attributes such as
// the originating TokenEntry (see auth.TokenEntryFromContext).
type Evaluator interface {
	Decide(ctx context.Context, id *auth.Identity, action Action, resource string) Decision
}

// AllowAuthenticated allows every action for any authenticated identity and
// denies anonymous callers. It is the baseline policy used before per-repo
// access control is introduced.
type AllowAuthenticated struct{}

// Decide implements Evaluator.
func (AllowAuthenticated) Decide(_ context.Context, id *auth.Identity, _ Action, _ string) Decision {
	if id == nil {
		return Deny("not authenticated")
	}
	return Allow()
}

// DenyAll rejects every request. Useful in tests.
type DenyAll struct{}

// Decide implements Evaluator.
func (DenyAll) Decide(context.Context, *auth.Identity, Action, string) Decision {
	return Deny("denied by policy")
}

// ProviderSession identifies session-authenticated admins. Kept here so the
// evaluator doesn't need to import auth implementation details.
const (
	ProviderSession      = "session"
	ProviderToken        = "token"
	ProviderAuthDisabled = "auth-disabled"
)

// TokenRepos is the minimum interface a TokenRepoPolicy needs to consult the
// repo allowlist of the token used for the request.
type TokenRepos interface {
	// Repos returns the allowlist of repository names for the authenticating
	// token. Empty means no repos.
	Repos() []string
}

// TokenRepoPolicy enforces per-repo access for token-authenticated callers.
// Admin sessions and auth-disabled (dev) callers bypass the repo allowlist
// and are allowed every action.
//
// For token callers:
//   - ActionReadRepo / ActionWriteRepo with a specific repo: allow iff the
//     token's Repos list contains the repo.
//   - ActionReadRepo with no resource (listing): allow iff the token has at
//     least one repo assigned. Handlers are expected to filter the result
//     to the assigned set before returning it to the caller.
//   - ActionManageRepos / ActionManageTokens / ActionManageAdmins: deny.
//     These require an admin session.
type TokenRepoPolicy struct{}

// Decide implements Evaluator.
func (TokenRepoPolicy) Decide(ctx context.Context, id *auth.Identity, action Action, resource string) Decision {
	if id == nil {
		return Deny("not authenticated")
	}
	switch id.Provider {
	case ProviderSession, ProviderAuthDisabled:
		return Allow()
	case ProviderToken:
		return decideTokenAccess(ctx, action, resource)
	default:
		return Deny("unknown identity provider: " + id.Provider)
	}
}

func decideTokenAccess(ctx context.Context, action Action, resource string) Decision {
	switch action {
	case ActionManageRepos, ActionManageTokens, ActionManageAdmins:
		return Deny("management actions require an admin session")
	case ActionReadRepo, ActionWriteRepo:
		entry := auth.TokenEntryFromContext(ctx)
		if entry == nil {
			return Deny("token entry missing from context")
		}
		if resource == "" {
			// Listing: allow when at least one repo is assigned. Handler
			// filters the returned set to those repos.
			if len(entry.Repos) == 0 {
				return Deny("no repos assigned to this token")
			}
			return Allow()
		}
		for _, r := range entry.Repos {
			if r == resource {
				return Allow()
			}
		}
		return Deny("token not permitted for repo " + resource)
	default:
		return Deny("unhandled action: " + string(action))
	}
}

// TokenAbilityPolicy enforces that a token-authenticated caller holds a
// named ability (see auth.KnownAbilities). It is orthogonal to
// TokenRepoPolicy: handlers compose the two for endpoints that need both
// a repo scope and an ability (e.g. the subscriber-facing destinations
// API — see remote-sync.md §2.6).
//
// Admin sessions and auth-disabled (dev) callers bypass the ability
// check — admins already have god-mode; dev mode is off by design.
// Token callers are allowed iff their TokenEntry's Abilities slice
// contains the configured name.
//
// The Action and resource arguments are accepted to satisfy the
// Evaluator interface and are not consulted — the policy is purely an
// ability gate. Callers pass the repo name via TokenRepoPolicy separately.
type TokenAbilityPolicy struct {
	ability string
}

// NewTokenAbilityPolicy constructs a policy that requires the named
// ability on the caller's token. Panics if ability is empty — an empty
// name would allow any token through, which is almost certainly a bug
// at the call site rather than a legitimate configuration.
func NewTokenAbilityPolicy(ability string) TokenAbilityPolicy {
	if ability == "" {
		panic("policy: TokenAbilityPolicy requires a non-empty ability name")
	}
	return TokenAbilityPolicy{ability: ability}
}

// Decide implements Evaluator.
func (p TokenAbilityPolicy) Decide(ctx context.Context, id *auth.Identity, _ Action, _ string) Decision {
	if id == nil {
		return Deny("not authenticated")
	}
	switch id.Provider {
	case ProviderSession, ProviderAuthDisabled:
		return Allow()
	case ProviderToken:
		entry := auth.TokenEntryFromContext(ctx)
		if entry == nil {
			return Deny("token entry missing from context")
		}
		if !entry.HasAbility(p.ability) {
			return Deny("token missing ability: " + p.ability)
		}
		return Allow()
	default:
		return Deny("unknown identity provider: " + id.Provider)
	}
}

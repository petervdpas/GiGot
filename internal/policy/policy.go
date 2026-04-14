// Package policy is the single place that decides whether an authenticated
// identity may perform an action on a resource. Handlers should never make
// ad-hoc authorisation decisions — they call Evaluator.Decide and honour the
// result. New authorisation rules (per-repo allowlists, read-vs-write, etc.)
// are added here by swapping in a different Evaluator implementation, not by
// threading new fields through every DTO.
package policy

import "github.com/petervdpas/GiGot/internal/auth"

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
// not applicable.
type Evaluator interface {
	Decide(id *auth.Identity, action Action, resource string) Decision
}

// AllowAuthenticated allows every action for any authenticated identity and
// denies anonymous callers. It is the baseline policy used before per-repo
// access control is introduced.
type AllowAuthenticated struct{}

// Decide implements Evaluator.
func (AllowAuthenticated) Decide(id *auth.Identity, _ Action, _ string) Decision {
	if id == nil {
		return Deny("not authenticated")
	}
	return Allow()
}

// DenyAll rejects every request. Useful in tests.
type DenyAll struct{}

// Decide implements Evaluator.
func (DenyAll) Decide(*auth.Identity, Action, string) Decision {
	return Deny("denied by policy")
}

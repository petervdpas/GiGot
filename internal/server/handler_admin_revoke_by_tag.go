package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/petervdpas/GiGot/internal/auth"
	gitmanager "github.com/petervdpas/GiGot/internal/git"
)

// handleAdminRevokeByTag godoc
// @Summary      Bulk revoke subscriptions by effective tag (admin only)
// @Description  Revokes every subscription whose effective tag set
// @Description  (sub.tags ∪ repo.tags ∪ account.tags) carries at least
// @Description  one of the tags in the request body. Multiple tag
// @Description  values union (OR — same inclusion semantics as the
// @Description  chip UI; selecting more chips widens the match).
// @Description  Empty tag list is a 400 — bulk-revoke without a
// @Description  filter would clear the catalogue, which has enough
// @Description  blast radius to deserve a different endpoint.
// @Description
// @Description  The Confirm field must match the deterministic phrase
// @Description  `revoke <comma-joined-lower-tags>` (tags sorted, e.g.
// @Description  `revoke contractor:acme,team:marketing`). This is
// @Description  anti-typo friction — same purpose as a `--force` flag
// @Description  on a destructive command — not an anti-script gate
// @Description  (the phrase is deterministic from the request inputs,
// @Description  so any caller that can compute the tag list can compute
// @Description  the phrase). Server-side validation runs alongside the
// @Description  UI check so a buggy callsite can't omit the field by
// @Description  accident. The auth boundary is the admin session,
// @Description  same as every other `/api/admin/*` endpoint.
// @Description
// @Description  Each revoked subscription emits a `tag.revoked.bulk`
// @Description  event on its repo's refs/audit/main with the matching
// @Description  tag set, so the audit chain answers "why was sub-77
// @Description  revoked?" six months later. Session-cookie auth.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      RevokeByTagRequest   true  "Tag set + typed confirmation"
// @Success      200   {object}  RevokeByTagResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/subscriptions/revoke-by-tag [post]
func (s *Server) handleAdminRevokeByTag(w http.ResponseWriter, r *http.Request) {
	caller := s.requireAdminSession(w, r)
	if caller == nil {
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req RevokeByTagRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Normalise + validate the requested tag set: trim, lower, dedupe,
	// reject empty. An empty set after normalisation would match every
	// subscription, which is exactly the unbounded sweep the design
	// rejects (§5.6 wraps the destructive button behind active chips).
	wantLower := normaliseRevokeByTagInput(req.Tags)
	if len(wantLower) == 0 {
		writeError(w, http.StatusBadRequest, "tags is required (at least one tag must be specified)")
		return
	}

	// Server-side phrase gate. The expected value is deterministic from
	// the request so the UI can compute and surface it identically; a
	// caller that didn't go through the dialog (curl + a typo) gets a
	// 400 instead of a silent sweep.
	expected := expectedRevokePhrase(wantLower)
	if strings.TrimSpace(req.Confirm) != expected {
		writeError(w, http.StatusBadRequest,
			fmt.Sprintf("confirm phrase mismatch; expected %q", expected))
		return
	}

	// Enumerate matching subscriptions before mutating anything. A
	// concurrent issuance can race in between but only adds rows we
	// were not going to touch anyway — the snapshot we revoke is
	// internally consistent.
	matches := s.matchingSubscriptionsByTag(wantLower)

	revoked := make([]RevokedSubscription, 0, len(matches))
	matchedTagsLabel := strings.Join(wantLower, ",")
	for _, m := range matches {
		if !s.tokenStrategy.Revoke(m.Token) {
			// Token went missing between enumeration and revoke — skip
			// silently; the revoke contract is "no longer active",
			// which is satisfied either way.
			continue
		}
		revoked = append(revoked, RevokedSubscription{
			Token:     m.Token,
			Username:  m.Username,
			Repo:      m.Repo,
			Abilities: m.Abilities,
		})
		s.recordRepoBulkRevokeAudit(m.Repo, caller, matchedTagsLabel, m.Username)
	}

	writeJSON(w, http.StatusOK, RevokeByTagResponse{
		Revoked: revoked,
		Count:   len(revoked),
	})
}

// normaliseRevokeByTagInput trims, lower-cases, drops empties, dedupes,
// and sorts the tag list so two callers with different orderings hit
// the same expected confirm phrase.
func normaliseRevokeByTagInput(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		v := strings.ToLower(strings.TrimSpace(raw))
		if v == "" {
			continue
		}
		if _, dup := seen[v]; dup {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// expectedRevokePhrase builds the deterministic confirm string the UI
// shows and the server checks. wantLower must already be sorted +
// deduped (normaliseRevokeByTagInput).
func expectedRevokePhrase(wantLower []string) string {
	return "revoke " + strings.Join(wantLower, ",")
}

// matchingSubscriptionsByTag returns the subscription entries whose
// effective tag set covers every wantLower tag (AND). The slice is a
// snapshot — callers iterate it independently of the live token store.
func (s *Server) matchingSubscriptionsByTag(wantLower []string) []revokeCandidate {
	entries := s.tokenStrategy.List()
	out := make([]revokeCandidate, 0, len(entries))
	for _, e := range entries {
		provider, identifier, perr := parseTokenUsername(e.Username)
		accountKey := ""
		if perr == nil && s.accounts.Has(provider, identifier) {
			accountKey = provider + ":" + identifier
		}
		effective := s.tags.EffectiveSubscriptionTags(e.Token, e.Repo, accountKey)
		if !effectiveCoversAny(effective, wantLower) {
			continue
		}
		abil := append([]string(nil), e.Abilities...)
		out = append(out, revokeCandidate{
			Token:     e.Token,
			Username:  e.Username,
			Repo:      e.Repo,
			Abilities: abil,
		})
	}
	return out
}

// revokeCandidate is the internal snapshot row the bulk-revoke handler
// passes between enumeration and execution. Keeps the response builder
// independent of the live token entry struct.
type revokeCandidate struct {
	Token     string
	Username  string
	Repo      string
	Abilities []string
}

// recordRepoBulkRevokeAudit appends one tag.revoked.bulk event onto
// the subscription's repo audit chain. Per design §7.1, every
// tag-driven admin action is auditable; bulk revoke is the most
// destructive of the lot, so the per-row event must carry the
// matching tag set + the account so a forensic reader sees why each
// revoke fired.
func (s *Server) recordRepoBulkRevokeAudit(repo string, id *auth.Identity, matchedTags, username string) {
	notes, err := json.Marshal(map[string]any{
		"matched_tags": matchedTags,
		"username":     username,
	})
	if err != nil {
		return
	}
	if id == nil {
		return
	}
	_, _ = s.git.AppendAudit(repo, gitmanager.AuditEvent{
		Type:  "tag.revoked.bulk",
		Actor: gitmanager.AuditActor{ID: id.ID, Username: id.Username, Provider: id.AccountProvider},
		Notes: string(notes),
	})
}

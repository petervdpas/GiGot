package server

import (
	"fmt"
	"strings"

	"github.com/petervdpas/GiGot/internal/auth"
)

// normalizeAbilities trims, de-duplicates and drops empty entries from an
// ability list, matching the shape of normalizeRepos. Returns nil for an
// empty result so the persisted TokenEntry doesn't carry an empty JSON
// array.
func normalizeAbilities(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if _, dup := seen[a]; dup {
			continue
		}
		seen[a] = struct{}{}
		out = append(out, a)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateAbilities rejects ability names the server does not recognise.
// Admins attempting to assign a typo'd or not-yet-introduced ability get
// a 400 at the boundary rather than a token carrying a permanently-inert
// entry.
func validateAbilities(abilities []string) error {
	if len(abilities) == 0 {
		return nil
	}
	var unknown []string
	for _, a := range abilities {
		if !auth.IsKnownAbility(a) {
			unknown = append(unknown, a)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown ability: %s", strings.Join(unknown, ", "))
	}
	return nil
}

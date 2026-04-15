package server

import (
	"fmt"
	"strings"
)

// normalizeRepos trims, de-duplicates and drops empty entries from a repo
// list. Returns nil for an empty result so the persisted TokenEntry doesn't
// carry an empty JSON array.
func normalizeRepos(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// validateRepos rejects a repo allowlist that references a repository which
// doesn't exist on disk. This stops admins from issuing keys bound to
// typo'd or not-yet-created repos.
func (s *Server) validateRepos(repos []string) error {
	if len(repos) == 0 {
		return nil
	}
	existing, err := s.git.List()
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	known := make(map[string]struct{}, len(existing))
	for _, e := range existing {
		known[e] = struct{}{}
	}
	var missing []string
	for _, r := range repos {
		if _, ok := known[r]; !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("unknown repo(s): %s", strings.Join(missing, ", "))
	}
	return nil
}

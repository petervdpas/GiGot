package server

import (
	"fmt"
	"strings"
)

// validateRepo rejects a repo reference that doesn't match a
// repository on disk. Subscription keys are bound to exactly one
// repo and the admin must not be able to mint a key pointing at a
// typo or a not-yet-created repo.
func (s *Server) validateRepo(repo string) error {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return fmt.Errorf("repo is required")
	}
	existing, err := s.git.List()
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	for _, e := range existing {
		if e == repo {
			return nil
		}
	}
	return fmt.Errorf("unknown repo: %s", repo)
}

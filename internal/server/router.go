package server

import (
	"net/http"
	"strings"
)

// handleRepoRouter dispatches /api/repos/* to the appropriate handler.
func (s *Server) handleRepoRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/repos/")

	switch {
	case strings.HasSuffix(path, "/head"):
		s.handleRepoHead(w, r)
	case strings.HasSuffix(path, "/tree"):
		s.handleRepoTree(w, r)
	case strings.HasSuffix(path, "/snapshot"):
		s.handleRepoSnapshot(w, r)
	case strings.Contains(path, "/files/"):
		s.handleRepoFile(w, r)
	case strings.Contains(path, "/records/"):
		s.handleRepoRecords(w, r)
	case strings.HasSuffix(path, "/destinations") || strings.Contains(path, "/destinations/"):
		s.handleRepoDestinations(w, r)
	case strings.HasSuffix(path, "/commits"):
		s.handleRepoCommits(w, r)
	case strings.HasSuffix(path, "/changes"):
		s.handleRepoChanges(w, r)
	case strings.HasSuffix(path, "/status"):
		s.handleRepoStatus(w, r)
	case strings.HasSuffix(path, "/branches"):
		s.handleRepoBranches(w, r)
	case strings.HasSuffix(path, "/log"):
		s.handleRepoLog(w, r)
	default:
		s.handleRepo(w, r)
	}
}

// handleGitRouter dispatches /git/* to the appropriate git protocol handler.
func (s *Server) handleGitRouter(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case strings.HasSuffix(path, "/info/refs"):
		s.handleGitInfoRefs(w, r)
	case strings.HasSuffix(path, "/git-upload-pack"):
		s.handleGitUploadPack(w, r)
	case strings.HasSuffix(path, "/git-receive-pack"):
		s.handleGitReceivePack(w, r)
	default:
		http.NotFound(w, r)
	}
}

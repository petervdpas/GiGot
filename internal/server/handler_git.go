package server

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"

	gitmanager "github.com/petervdpas/GiGot/internal/git"
	"github.com/petervdpas/GiGot/internal/policy"
)

// handleGit handles the git smart HTTP protocol.
// This enables git clone, fetch, and push over HTTP.
//
// Routes:
//   GET  /git/{name}.git/info/refs?service=git-upload-pack    (clone/fetch discovery)
//   GET  /git/{name}.git/info/refs?service=git-receive-pack   (push discovery)
//   POST /git/{name}.git/git-upload-pack                      (clone/fetch data)
//   POST /git/{name}.git/git-receive-pack                     (push data)

// handleGitInfoRefs godoc
// @Summary      Git refs discovery
// @Description  Git smart HTTP protocol — advertise refs for clone/fetch/push
// @Tags        git
// @Param        name     path   string  true  "Repository name"
// @Param        service  query  string  true  "git-upload-pack or git-receive-pack"
// @Produce      octet-stream
// @Success      200
// @Failure      400  {object}  ErrorResponse
// @Failure     401   {object}  ErrorResponse  "Missing or invalid bearer token"
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Security     BearerAuth
// @Security     BasicAuth
// @Router       /git/{name}.git/info/refs [get]
func (s *Server) handleGitInfoRefs(w http.ResponseWriter, r *http.Request) {
	name, err := s.extractGitRepoName(r.URL.Path, "/info/refs")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
		return
	}

	service := r.URL.Query().Get("service")
	var act policy.Action
	switch service {
	case "git-upload-pack":
		act = policy.ActionReadRepo
	case "git-receive-pack":
		act = policy.ActionWriteRepo
	default:
		writeError(w, http.StatusBadRequest, "invalid service")
		return
	}
	if !s.requireAllow(w, r, act, name) {
		return
	}

	repoPath := s.git.RepoPath(name)
	cmd := exec.Command("git", service[4:], "--stateless-rpc", "--advertise-refs", repoPath)
	out, err := cmd.Output()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "git error")
		return
	}

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-%s-advertisement", service))
	w.Header().Set("Cache-Control", "no-cache")

	// Smart HTTP preamble.
	pktLine := fmt.Sprintf("# service=%s\n", service)
	pktLen := fmt.Sprintf("%04x", len(pktLine)+4)
	w.Write([]byte(pktLen))
	w.Write([]byte(pktLine))
	w.Write([]byte("0000"))
	w.Write(out)
}

// handleGitUploadPack godoc
// @Summary      Git clone/fetch
// @Description  Git smart HTTP protocol — serves packfile data for clone and fetch operations
// @Tags        git
// @Param        name  path  string  true  "Repository name"
// @Accept       octet-stream
// @Produce      octet-stream
// @Success      200
// @Failure     401   {object}  ErrorResponse  "Missing or invalid bearer token"
// @Failure      404  {object}  ErrorResponse
// @Security     BearerAuth
// @Security     BasicAuth
// @Router       /git/{name}.git/git-upload-pack [post]
func (s *Server) handleGitUploadPack(w http.ResponseWriter, r *http.Request) {
	s.handleGitService(w, r, "upload-pack")
}

// handleGitReceivePack godoc
// @Summary      Git push
// @Description  Git smart HTTP protocol — receives packfile data for push operations
// @Tags        git
// @Param        name  path  string  true  "Repository name"
// @Accept       octet-stream
// @Produce      octet-stream
// @Success      200
// @Failure     401   {object}  ErrorResponse  "Missing or invalid bearer token"
// @Failure      404  {object}  ErrorResponse
// @Security     BearerAuth
// @Security     BasicAuth
// @Router       /git/{name}.git/git-receive-pack [post]
func (s *Server) handleGitReceivePack(w http.ResponseWriter, r *http.Request) {
	s.handleGitService(w, r, "receive-pack")
}

func (s *Server) handleGitService(w http.ResponseWriter, r *http.Request, service string) {
	suffix := "/git-" + service
	name, err := s.extractGitRepoName(r.URL.Path, suffix)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	act := policy.ActionReadRepo
	if service == "receive-pack" {
		act = policy.ActionWriteRepo
	}
	if !s.requireAllow(w, r, act, name) {
		return
	}

	if !s.git.Exists(name) {
		writeError(w, http.StatusNotFound, "repository not found")
		return
	}

	repoPath := s.git.RepoPath(name)
	cmd := exec.Command("git", service, "--stateless-rpc", repoPath)

	// Handle gzip-encoded request bodies.
	var body io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid gzip body")
			return
		}
		defer gz.Close()
		body = gz
	}

	cmd.Stdin = body
	cmd.Stderr = nil

	// For pushes, snapshot refs around the subprocess so we can emit one
	// push_received audit entry per ref that actually moved. Snapshot
	// failures are logged but never block the push — audit is observability
	// for an operation that already took its user-facing write.
	var preRefs map[string]string
	if service == "receive-pack" {
		snap, snapErr := s.git.RefSnapshot(name)
		if snapErr != nil {
			log.Printf("audit: pre-push ref snapshot failed on repo %q: %v", name, snapErr)
		} else {
			preRefs = snap
		}
	}

	out, err := cmd.Output()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "git error")
		return
	}

	if service == "receive-pack" && preRefs != nil {
		anyMoved := s.auditPushedRefs(r, name, preRefs)
		// Fan out to mirror destinations only when at least one ref
		// actually moved. A receive-pack that rejected every update
		// leaves the tree exactly as the mirrors already have it; no
		// point firing a no-op push. Worker is optional — tests that
		// want deterministic behavior can nil it out.
		if anyMoved && s.mirrorWorker != nil {
			s.mirrorWorker.enqueue(name)
		}
	}

	w.Header().Set("Content-Type", fmt.Sprintf("application/x-git-%s-result", service))
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(out)
}

// auditPushedRefs diffs the post-push ref snapshot against the supplied
// pre-push snapshot and appends one push_received audit entry per ref that
// the client actually moved. A receive-pack that rejected every update
// (non-ff, hook refusal) produces an empty diff and so no audit noise.
// Returns true when at least one ref moved — the caller uses that to
// decide whether to enqueue a mirror fan-out.
func (s *Server) auditPushedRefs(r *http.Request, name string, preRefs map[string]string) bool {
	postRefs, err := s.git.RefSnapshot(name)
	if err != nil {
		log.Printf("audit: post-push ref snapshot failed on repo %q: %v", name, err)
		return false
	}
	actor := auditActor(r)
	moved := false
	for _, change := range gitmanager.DiffRefSnapshots(preRefs, postRefs) {
		moved = true
		sha := change.NewSHA
		if change.Kind == gitmanager.RefDeleted {
			sha = change.OldSHA
		}
		s.appendAudit(name, gitmanager.AuditEvent{
			Type:  AuditTypePushReceived,
			Actor: actor,
			Ref:   change.Ref,
			SHA:   sha,
			Notes: string(change.Kind),
		})
	}
	return moved
}

// extractGitRepoName extracts the repo name from a git URL path.
// Path format: /git/{name}.git/{suffix}
func (s *Server) extractGitRepoName(path, suffix string) (string, error) {
	trimmed := strings.TrimPrefix(path, "/git/")
	trimmed = strings.TrimSuffix(trimmed, suffix)
	trimmed = strings.TrimSuffix(trimmed, ".git")
	if trimmed == "" || strings.Contains(trimmed, "/") || strings.Contains(trimmed, "..") {
		return "", fmt.Errorf("invalid repository name")
	}
	return trimmed, nil
}

package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// commitWriteTimeout bounds the non-streaming POST /commits handler. Kept
// below the Formidable client's request timeout so a wedged commit surfaces
// as a clean 503 here before the client gives up on its end.
const commitWriteTimeout = 60 * time.Second

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
	case strings.Contains(path, "/formidable/records/"):
		s.handleRepoRecords(w, r)
	case strings.HasSuffix(path, "/formidable"):
		s.handleRepoFormidable(w, r)
	case strings.HasSuffix(path, "/destinations") || strings.Contains(path, "/destinations/"):
		s.handleRepoDestinations(w, r)
	case strings.HasSuffix(path, "/commits"):
		// A commit is non-streaming JSON, so unlike /snapshot it can carry a
		// write deadline. If the handler wedges, the client gets a clean 503
		// instead of hanging until its own transport timeout fires. The git
		// work is atomic, so a commit that finishes after the deadline lands
		// harmlessly even though this response is already a timeout.
		http.TimeoutHandler(http.HandlerFunc(s.handleRepoCommits),
			commitWriteTimeout, "commit timed out").ServeHTTP(w, r)
	case strings.HasSuffix(path, "/changes"):
		s.handleRepoChanges(w, r)
	case strings.HasSuffix(path, "/status"):
		s.handleRepoStatus(w, r)
	case strings.HasSuffix(path, "/branches"):
		s.handleRepoBranches(w, r)
	case strings.HasSuffix(path, "/log"):
		s.handleRepoLog(w, r)
	case strings.HasSuffix(path, "/context"):
		s.handleRepoContext(w, r)
	default:
		s.handleRepo(w, r)
	}
}

// handleGitRouter dispatches /git/* to the appropriate git protocol
// handler. All three protocol verbs are bracketed by the load
// tracker (Begin/End) so concurrent pushes / pulls / discovery
// requests show up in the in-flight count and the rolling-window
// duration histogram. The 404 fall-through is left untracked —
// it isn't a real GiGot operation.
//
// receive-pack additionally passes through the push-slot admission
// gate. When all configured slots are busy, the request is rejected
// with `429 Too Many Requests` + `Retry-After: <cfg.Limits.PushRetryAfterSec>`
// — clients (Formidable) honor the header and back off. Reads
// (upload-pack, info-refs) bypass the gate so a push storm doesn't
// stall clones / fetches behind it.
func (s *Server) handleGitRouter(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	var inner http.HandlerFunc
	isReceive := false
	switch {
	case strings.HasSuffix(path, "/info/refs"):
		inner = s.handleGitInfoRefs
	case strings.HasSuffix(path, "/git-upload-pack"):
		inner = s.handleGitUploadPack
	case strings.HasSuffix(path, "/git-receive-pack"):
		inner = s.handleGitReceivePack
		isReceive = true
	default:
		http.NotFound(w, r)
		return
	}

	if isReceive && s.pushSlots != nil {
		if !s.pushSlots.TryAcquire() {
			retry := s.cfg.Limits.PushRetryAfterSec
			if retry < 1 {
				retry = 5
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeError(w, http.StatusTooManyRequests,
				"push slots full, retry after "+strconv.Itoa(retry)+"s")
			return
		}
		defer s.pushSlots.Release()
	}

	if s.load != nil {
		start := s.load.Begin()
		defer s.load.End(start)
	}
	inner(w, r)
}

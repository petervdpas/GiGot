package server

import (
	"sort"
	"testing"
)

// TestParseLsRemoteOutput covers the canonical tab-separated form,
// the legacy space-separated form, blank lines, and the peeled-tag
// suffix that --refs is supposed to strip. The parser is the
// load-bearing surface between git's wire format and our compare
// logic — getting the wrong refname here would silently report every
// destination as diverged.
func TestParseLsRemoteOutput(t *testing.T) {
	in := []byte(
		"abc123def\trefs/heads/main\n" +
			"\n" +
			"deadbeef refs/heads/develop\n" +
			"feedface\trefs/audit/main\n" +
			// Should be filtered even though --refs normally hides it.
			"cafebabe\trefs/tags/v1^{}\n" +
			"facefeed\trefs/tags/v1\n",
	)
	got, err := parseLsRemoteOutput(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	want := map[string]string{
		"refs/heads/main":    "abc123def",
		"refs/heads/develop": "deadbeef",
		"refs/audit/main":    "feedface",
		"refs/tags/v1":       "facefeed",
	}
	if len(got) != len(want) {
		t.Fatalf("len: want %d, got %d (%v)", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: want %q, got %q", k, v, got[k])
		}
	}
}

// TestCompareRefs_AllSame is the happy path: identical local +
// remote → in_sync, every per-ref state=same.
func TestCompareRefs_AllSame(t *testing.T) {
	local := map[string]string{
		"refs/heads/main":    "aaa",
		"refs/heads/develop": "bbb",
		"refs/audit/main":    "ccc",
	}
	remote := map[string]string{
		"refs/heads/main":    "aaa",
		"refs/heads/develop": "bbb",
		"refs/audit/main":    "ccc",
	}
	status, refs := compareRefs(local, remote)
	if status != remoteStatusInSync {
		t.Fatalf("status: want %q, got %q", remoteStatusInSync, status)
	}
	if len(refs) != 3 {
		t.Fatalf("ref count: want 3, got %d", len(refs))
	}
	for _, r := range refs {
		if r.State != remoteRefSame {
			t.Errorf("%s: want state=same, got %q", r.Ref, r.State)
		}
	}
}

// TestCompareRefs_DivergedShapes covers all three diverged states in
// one matrix: different (both sides have ref, SHAs differ),
// only_local (we have it, remote doesn't), only_remote (remote has
// it, we don't). Each of these flips the summary to diverged on its
// own. Refs outside refs/heads/* + refs/audit/* must be ignored —
// e.g. refs/pull/* on GitHub-hosted mirrors must not move the badge.
func TestCompareRefs_DivergedShapes(t *testing.T) {
	local := map[string]string{
		"refs/heads/main":     "aaa",
		"refs/heads/develop":  "bbb", // only_local
		"refs/audit/main":     "ccc", // different
		"refs/notes/internal": "xxx", // out of mirror namespace; ignored
	}
	remote := map[string]string{
		"refs/heads/main":  "aaa",
		"refs/heads/spike": "ddd", // only_remote
		"refs/audit/main":  "ZZZ", // different
		"refs/pull/42":     "yyy", // out of mirror namespace; ignored
	}
	status, refs := compareRefs(local, remote)
	if status != remoteStatusDiverged {
		t.Fatalf("status: want %q, got %q", remoteStatusDiverged, status)
	}
	byRef := make(map[string]string)
	names := make([]string, 0, len(refs))
	for _, r := range refs {
		byRef[r.Ref] = r.State
		names = append(names, r.Ref)
	}
	// The out-of-namespace refs must not appear in the result.
	if _, ok := byRef["refs/notes/internal"]; ok {
		t.Errorf("refs/notes/internal should be filtered; appeared with state %q", byRef["refs/notes/internal"])
	}
	if _, ok := byRef["refs/pull/42"]; ok {
		t.Errorf("refs/pull/42 should be filtered; appeared with state %q", byRef["refs/pull/42"])
	}
	want := map[string]string{
		"refs/heads/main":    remoteRefSame,
		"refs/heads/develop": remoteRefOnlyLocal,
		"refs/heads/spike":   remoteRefOnlyRemote,
		"refs/audit/main":    remoteRefDifferent,
	}
	for k, v := range want {
		if got := byRef[k]; got != v {
			t.Errorf("%s: want state %q, got %q", k, v, got)
		}
	}
	// And the result is alphabetically ordered for stable rendering.
	sortedNames := append([]string(nil), names...)
	sort.Strings(sortedNames)
	for i := range names {
		if names[i] != sortedNames[i] {
			t.Errorf("ref ordering not stable at %d: got %v", i, names)
			break
		}
	}
}

// TestCompareRefs_RemoteOnlyRefsOutOfNamespaceDoNotDiverge — fences
// the regression the previous test asserts in passing: when the only
// difference is in non-mirrored namespaces (e.g. GitHub's refs/pull/*)
// we MUST report in_sync. Otherwise every GitHub mirror would look
// permanently diverged.
func TestCompareRefs_RemoteOnlyRefsOutOfNamespaceDoNotDiverge(t *testing.T) {
	local := map[string]string{
		"refs/heads/main": "aaa",
		"refs/audit/main": "bbb",
	}
	remote := map[string]string{
		"refs/heads/main": "aaa",
		"refs/audit/main": "bbb",
		"refs/pull/1":     "111",
		"refs/pull/2":     "222",
		"refs/tags/v1":    "ttt",
	}
	status, _ := compareRefs(local, remote)
	if status != remoteStatusInSync {
		t.Fatalf("status: want in_sync (non-mirror refs ignored), got %q", status)
	}
}

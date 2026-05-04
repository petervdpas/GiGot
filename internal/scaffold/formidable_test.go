package scaffold

import (
	"strings"
	"testing"
	"time"
)

// TestFormidableFiles_GitignorePatterns pins the .gitignore patterns
// shipped in the Formidable scaffold. The ledger entry must always
// be present (the autofix path also relies on it); the two log
// patterns were added 2026-05-04 because Formidable runtime logs
// were ending up in team repos and there's no good reason for one
// user's per-machine `*.log` files to travel via git.
//
// Pinning each pattern individually (rather than a whole-file diff)
// lets the comments evolve without breaking the test — what matters
// is the rules, not the prose around them.
func TestFormidableFiles_GitignorePatterns(t *testing.T) {
	files, err := FormidableFiles(time.Now())
	if err != nil {
		t.Fatalf("FormidableFiles: %v", err)
	}
	var gitignore string
	for _, f := range files {
		if f.Path == ".gitignore" {
			gitignore = string(f.Content)
			break
		}
	}
	if gitignore == "" {
		t.Fatal("scaffold output missing .gitignore")
	}
	// Each pattern must be on its own line — `git check-ignore`
	// matches whole lines, so a substring check that matched a
	// comment containing the pattern would silently let a regression
	// through.
	wantLines := []string{
		".formidable/sync.json",
		"*.log",
		"**/*.log",
		".changes.*",
		"**/.changes.*",
	}
	for _, want := range wantLines {
		if !hasGitignoreLine(gitignore, want) {
			t.Errorf(".gitignore missing line %q; got:\n%s", want, gitignore)
		}
	}
}

// hasGitignoreLine returns true when body contains line as its own
// line, ignoring leading/trailing whitespace and skipping comment
// lines (which start with `#` in gitignore syntax). Mirrors the
// shape of gitignoreHasEntry in internal/server but keeps this
// package free of that dependency direction.
func hasGitignoreLine(body, line string) bool {
	for _, raw := range strings.Split(body, "\n") {
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if t == line {
			return true
		}
	}
	return false
}

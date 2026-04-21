package server

import (
	"encoding/base64"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestFormidableScaffoldFiles_ExpectedPayload(t *testing.T) {
	fixed := time.Date(2026, 4, 16, 12, 30, 0, 0, time.UTC)
	files, err := formidableScaffoldFiles(fixed)
	if err != nil {
		t.Fatal(err)
	}

	paths := make(map[string][]byte, len(files))
	for _, f := range files {
		paths[f.Path] = f.Content
	}

	// Every starter file the Formidable context needs must survive the walk.
	for _, required := range []string{"README.md", "templates/basic.yaml", "storage/.gitkeep", ".formidable/context.json"} {
		if _, ok := paths[required]; !ok {
			t.Fatalf("scaffold missing %q (got %v)", required, keys(paths))
		}
	}

	// basic.yaml must actually describe a Formidable template with a collection.
	yaml := string(paths["templates/basic.yaml"])
	if !strings.Contains(yaml, "enable_collection: true") {
		t.Fatalf("basic.yaml should enable the collection; got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "type: guid") || !strings.Contains(yaml, "type: text") {
		t.Fatalf("basic.yaml should contain guid + text fields; got:\n%s", yaml)
	}
	if !strings.Contains(yaml, "collection:") {
		t.Fatalf("basic.yaml should set a collection on at least one field; got:\n%s", yaml)
	}

	// .gitkeep must be empty — its presence is what matters.
	if len(paths["storage/.gitkeep"]) != 0 {
		t.Fatalf("storage/.gitkeep should be empty; got %d bytes", len(paths["storage/.gitkeep"]))
	}

	// .formidable/context.json must decode and carry the Phase-0 shape.
	var marker struct {
		Version      int    `json:"version"`
		ScaffoldedBy string `json:"scaffolded_by"`
		ScaffoldedAt string `json:"scaffolded_at"`
	}
	if err := json.Unmarshal(paths[".formidable/context.json"], &marker); err != nil {
		t.Fatalf("marker file is not valid JSON: %v", err)
	}
	if marker.Version != 1 {
		t.Fatalf("marker version: want 1, got %d", marker.Version)
	}
	if marker.ScaffoldedBy != "gigot" {
		t.Fatalf("marker scaffolded_by: want %q, got %q", "gigot", marker.ScaffoldedBy)
	}
	if marker.ScaffoldedAt != "2026-04-16T12:30:00Z" {
		t.Fatalf("marker scaffolded_at: want RFC3339 of fixed time, got %q", marker.ScaffoldedAt)
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestResolveShouldStamp(t *testing.T) {
	tru := true
	fal := false
	cases := []struct {
		name          string
		serverDefault bool
		requested     *bool
		want          bool
	}{
		{"generic server, omitted", false, nil, false},
		{"generic server, explicit true", false, &tru, true},
		{"generic server, explicit false", false, &fal, false},
		{"formidable server, omitted", true, nil, true},
		{"formidable server, explicit true", true, &tru, true},
		{"formidable server, explicit false", true, &fal, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveShouldStamp(c.serverDefault, c.requested); got != c.want {
				t.Errorf("want %v, got %v", c.want, got)
			}
		})
	}
}

func TestIsValidFormidableMarker(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"valid v1", `{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`, true},
		{"valid v2 (forward-compatible)", `{"version":2}`, true},
		{"zero version", `{"version":0}`, false},
		{"missing version", `{"scaffolded_by":"gigot"}`, false},
		{"empty", ``, false},
		{"not json", `hello world`, false},
		{"null", `null`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isValidFormidableMarker([]byte(c.body)); got != c.want {
				t.Errorf("want %v, got %v", c.want, got)
			}
		})
	}
}

func TestStampFormidableMarker_AddsMarkerOnBareClone(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("stamp-fresh")
	seedFile(t, srv, "stamp-fresh", "README.md", "hi\n", "seed")
	beforeHead, _ := srv.git.Head("stamp-fresh")

	stamped, err := stampFormidableMarker(srv.git, "stamp-fresh", time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if !stamped {
		t.Fatal("stamp should have written a commit")
	}

	after, err := srv.git.Head("stamp-fresh")
	if err != nil {
		t.Fatalf("head: %v", err)
	}
	if after.Version == beforeHead.Version {
		t.Error("HEAD should advance after stamp")
	}

	file, err := srv.git.File("stamp-fresh", "", formidableMarkerPath)
	if err != nil {
		t.Fatalf("marker should be at HEAD: %v", err)
	}
	if file.ContentB64 == "" {
		t.Error("marker content should be non-empty")
	}
}

func TestStampFormidableMarker_SkipsWhenValidMarkerPresent(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("stamp-skip")
	seedFile(t, srv, "stamp-skip", formidableMarkerPath,
		`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`+"\n",
		"seed with marker")
	before, _ := srv.git.Head("stamp-skip")

	stamped, err := stampFormidableMarker(srv.git, "stamp-skip", time.Now())
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if stamped {
		t.Error("stamp should have skipped (valid marker already present)")
	}
	after, _ := srv.git.Head("stamp-skip")
	if after.Version != before.Version {
		t.Error("HEAD must not advance when skipping")
	}
}

// TestStampFormidableMarker_CommitShape pins down the audit-load-bearing
// properties of the stamp commit itself: who wrote it, what it says, and
// what it actually changed in the tree. Weak tests here would let a
// regression stamp repos under the wrong identity or accidentally bundle
// other changes into the marker commit.
func TestStampFormidableMarker_CommitShape(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape")
	seedFile(t, srv, "shape", "README.md", "pre-existing\n", "seed")
	seedFile(t, srv, "shape", "docs/notes.md", "untouched\n", "add docs")
	parent, _ := srv.git.Head("shape")

	stamped, err := stampFormidableMarker(srv.git, "shape",
		time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if !stamped {
		t.Fatal("expected a stamp commit to be written")
	}
	head, _ := srv.git.Head("shape")
	repoPath := srv.git.RepoPath("shape")

	// Author / committer / subject, in a single git log call.
	out, err := exec.Command("git", "-C", repoPath, "log", "-1",
		"--format=%an|%ae|%cn|%ce|%s", head.Version).Output()
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	fields := strings.SplitN(strings.TrimSpace(string(out)), "|", 5)
	if len(fields) != 5 {
		t.Fatalf("unexpected log format: %q", out)
	}
	an, ae, cn, ce, subj := fields[0], fields[1], fields[2], fields[3], fields[4]
	if an != scaffoldCommitterName || ae != scaffoldCommitterEmail {
		t.Errorf("author: want %s <%s>, got %s <%s>", scaffoldCommitterName, scaffoldCommitterEmail, an, ae)
	}
	if cn != scaffoldCommitterName || ce != scaffoldCommitterEmail {
		t.Errorf("committer: want %s <%s>, got %s <%s>", scaffoldCommitterName, scaffoldCommitterEmail, cn, ce)
	}
	if subj != markerStampMessage {
		t.Errorf("subject: want %q, got %q", markerStampMessage, subj)
	}

	// Exactly one parent — never a merge commit.
	parents, err := exec.Command("git", "-C", repoPath, "rev-list",
		"--parents", "-n", "1", head.Version).Output()
	if err != nil {
		t.Fatalf("rev-list: %v", err)
	}
	parts := strings.Fields(strings.TrimSpace(string(parents)))
	if len(parts) != 2 {
		t.Errorf("want exactly 1 parent, got %d: %v", len(parts)-1, parts)
	}
	if len(parts) >= 2 && parts[1] != parent.Version {
		t.Errorf("parent: want %s, got %s", parent.Version, parts[1])
	}

	// Tree delta must be exactly the marker — nothing else gets smuggled
	// into the stamp commit.
	diff, err := exec.Command("git", "-C", repoPath, "diff-tree",
		"--no-commit-id", "-r", "--name-only", parent.Version, head.Version).Output()
	if err != nil {
		t.Fatalf("diff-tree: %v", err)
	}
	changed := strings.Fields(strings.TrimSpace(string(diff)))
	if len(changed) != 1 || changed[0] != formidableMarkerPath {
		t.Errorf("stamp commit changed unexpected files: %v", changed)
	}

	// The untouched files must still resolve at HEAD with their original
	// contents — sanity that `WriteFile`'s tree composition didn't drop
	// anything.
	for _, p := range []string{"README.md", "docs/notes.md"} {
		if _, err := srv.git.File("shape", "", p); err != nil {
			t.Errorf("%s should still exist at HEAD: %v", p, err)
		}
	}
}

func TestStampFormidableMarker_OverwritesBrokenMarker(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("stamp-broken")
	seedFile(t, srv, "stamp-broken", formidableMarkerPath,
		`this is not json`, "seed with broken marker")

	stamped, err := stampFormidableMarker(srv.git, "stamp-broken",
		time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("stamp: %v", err)
	}
	if !stamped {
		t.Error("stamp should overwrite a broken marker")
	}
	file, err := srv.git.File("stamp-broken", "", formidableMarkerPath)
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(file.ContentB64)
	if !isValidFormidableMarker(raw) {
		t.Errorf("marker after stamp should be valid; got:\n%s", raw)
	}
}

// TestEnsureFormidableShape_AddsAllWhenOnlyReadme covers the headline
// case the user hit on BrainDamage: a converted repo that had only a
// README.md ended up with just the marker, no templates/ or storage/.
// After the fix, one convert call must leave the tree with marker +
// templates/basic.yaml + storage/.gitkeep alongside the untouched
// README.
func TestEnsureFormidableShape_AddsAllWhenOnlyReadme(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-readme")
	seedFile(t, srv, "shape-readme", "README.md", "pre-existing\n", "seed readme")

	added, err := ensureFormidableShape(srv.git, "shape-readme",
		time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	want := []string{formidableMarkerPath, "templates/basic.yaml", "storage/.gitkeep", gitignorePath}
	if len(added) != len(want) {
		t.Fatalf("added: want %v, got %v", want, added)
	}
	for _, w := range want {
		if !contains(added, w) {
			t.Errorf("expected %q in added list %v", w, added)
		}
	}
	for _, p := range append(want, "README.md") {
		if _, err := srv.git.File("shape-readme", "", p); err != nil {
			t.Errorf("%s should exist at HEAD after ensure: %v", p, err)
		}
	}
	// README must NOT have been overwritten — we own ours.
	readme, _ := srv.git.File("shape-readme", "", "README.md")
	raw, _ := base64.StdEncoding.DecodeString(readme.ContentB64)
	if string(raw) != "pre-existing\n" {
		t.Errorf("README.md should be untouched; got %q", raw)
	}
}

// TestEnsureFormidableShape_SkipsExistingTemplates proves the
// "don't trample user content" half of the contract: if the repo
// already has something under templates/, we do NOT add the
// starter basic.yaml on top of it.
func TestEnsureFormidableShape_SkipsExistingTemplates(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-keep-tpl")
	seedFile(t, srv, "shape-keep-tpl", "README.md", "hi\n", "seed readme")
	seedFile(t, srv, "shape-keep-tpl", "templates/myform.yaml", "name: Mine\n", "seed custom template")

	added, err := ensureFormidableShape(srv.git, "shape-keep-tpl",
		time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if contains(added, "templates/basic.yaml") {
		t.Errorf("basic.yaml should NOT be added when templates/ already has content; got %v", added)
	}
	// Marker + storage starter should still land — they were missing.
	for _, p := range []string{formidableMarkerPath, "storage/.gitkeep"} {
		if !contains(added, p) {
			t.Errorf("expected %q in added list %v", p, added)
		}
	}
	if _, err := srv.git.File("shape-keep-tpl", "", "templates/myform.yaml"); err != nil {
		t.Errorf("user's custom template should survive: %v", err)
	}
	if _, err := srv.git.File("shape-keep-tpl", "", "templates/basic.yaml"); err == nil {
		t.Error("basic.yaml should not have been planted next to the user's template")
	}
}

// TestEnsureFormidableShape_SkipsExistingStorage mirror of the above
// for the storage/ half.
func TestEnsureFormidableShape_SkipsExistingStorage(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-keep-storage")
	seedFile(t, srv, "shape-keep-storage", "README.md", "hi\n", "seed readme")
	seedFile(t, srv, "shape-keep-storage", "storage/basic/row.meta.json", "{}\n", "seed record")

	added, err := ensureFormidableShape(srv.git, "shape-keep-storage",
		time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if contains(added, "storage/.gitkeep") {
		t.Errorf(".gitkeep should NOT be added when storage/ already has content; got %v", added)
	}
	if _, err := srv.git.File("shape-keep-storage", "", "storage/basic/row.meta.json"); err != nil {
		t.Errorf("user's record should survive: %v", err)
	}
}

// TestEnsureFormidableShape_AlreadyCompleteIsNoop — a repo that has
// marker + templates/ + storage/ already must not produce a new commit.
// Critical so repeat convert invocations stay quiet.
func TestEnsureFormidableShape_AlreadyCompleteIsNoop(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-complete")
	seedFile(t, srv, "shape-complete", "README.md", "hi\n", "seed readme")
	seedFile(t, srv, "shape-complete", "templates/basic.yaml", "name: Basic\n", "seed tpl")
	seedFile(t, srv, "shape-complete", "storage/.gitkeep", "", "seed storage")
	seedFile(t, srv, "shape-complete", formidableMarkerPath,
		`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`+"\n",
		"seed marker")
	seedFile(t, srv, "shape-complete", gitignorePath,
		gitignoreLedgerEntry+"\n", "seed gitignore")
	before, _ := srv.git.Head("shape-complete")

	added, err := ensureFormidableShape(srv.git, "shape-complete", time.Now())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(added) != 0 {
		t.Errorf("complete repo should add nothing; got %v", added)
	}
	after, _ := srv.git.Head("shape-complete")
	if after.Version != before.Version {
		t.Error("HEAD must not advance when repo is already complete")
	}
}

// TestEnsureFormidableShape_OverwritesBrokenMarker keeps the same
// broken-marker-is-replaced semantics stampFormidableMarker already
// enforces — otherwise a corrupt JSON would silently keep a repo
// in half-converted limbo.
func TestEnsureFormidableShape_OverwritesBrokenMarker(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-broken-marker")
	seedFile(t, srv, "shape-broken-marker", formidableMarkerPath,
		`garbage`, "seed broken marker")
	seedFile(t, srv, "shape-broken-marker", "templates/basic.yaml", "ok\n", "seed tpl")
	seedFile(t, srv, "shape-broken-marker", "storage/.gitkeep", "", "seed storage")

	added, err := ensureFormidableShape(srv.git, "shape-broken-marker", time.Now())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !contains(added, formidableMarkerPath) {
		t.Errorf("broken marker should be rewritten; added=%v", added)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// ── .gitignore hygiene — unit coverage for the helpers ────────────

func TestGitignoreHasEntry(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"exact match on own line", ".formidable/sync.json\n", true},
		{"with leading whitespace", "  .formidable/sync.json  \n", true},
		{"in middle of file", "*.tmp\n.formidable/sync.json\n*.log\n", true},
		{"missing entirely", "*.tmp\n*.log\n", false},
		{"only as comment — not a rule", "# .formidable/sync.json\n", false},
		{"empty file", "", false},
		{"prefix-of-other-line is not a match", ".formidable/sync.jsonx\n", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gitignoreHasEntry([]byte(c.body), ".formidable/sync.json")
			if got != c.want {
				t.Errorf("gitignoreHasEntry(%q) = %v, want %v", c.body, got, c.want)
			}
		})
	}
}

func TestAppendGitignoreEntry(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "empty file gets a clean entry line",
			in:   "",
			want: ".formidable/sync.json\n",
		},
		{
			name: "existing newline-terminated body — append cleanly",
			in:   "*.tmp\n",
			want: "*.tmp\n.formidable/sync.json\n",
		},
		{
			name: "body missing trailing newline — insert one",
			in:   "*.tmp",
			want: "*.tmp\n.formidable/sync.json\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := appendGitignoreEntry([]byte(c.in), ".formidable/sync.json")
			if string(got) != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// ── .gitignore hygiene — integration with ensureFormidableShape ────

// TestEnsureFormidableShape_AddsGitignoreWhenMissing proves a repo that
// has everything else but no .gitignore gets one scaffolded. Covers the
// "convert" path: an existing plain repo that already has templates/ and
// storage/ still needs .gitignore to protect against accidental
// commits of .formidable/sync.json via git CLI.
func TestEnsureFormidableShape_AddsGitignoreWhenMissing(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-nogi")
	seedFile(t, srv, "shape-nogi", "README.md", "hi\n", "seed readme")
	seedFile(t, srv, "shape-nogi", "templates/basic.yaml", "name: Basic\n", "seed tpl")
	seedFile(t, srv, "shape-nogi", "storage/.gitkeep", "", "seed storage")
	seedFile(t, srv, "shape-nogi", formidableMarkerPath,
		`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`+"\n",
		"seed marker")

	added, err := ensureFormidableShape(srv.git, "shape-nogi", time.Now())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !contains(added, gitignorePath) {
		t.Fatalf("expected %q in added list %v", gitignorePath, added)
	}
	f, err := srv.git.File("shape-nogi", "", gitignorePath)
	if err != nil {
		t.Fatalf("gitignore should exist at HEAD after ensure: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(f.ContentB64)
	if !gitignoreHasEntry(raw, gitignoreLedgerEntry) {
		t.Errorf("new .gitignore should list %q; got:\n%s", gitignoreLedgerEntry, raw)
	}
}

// TestEnsureFormidableShape_AppendsToExistingGitignore proves the
// "preserve user content" half: a repo whose .gitignore already lists
// other patterns gets only the missing ledger line appended — never a
// wholesale overwrite.
func TestEnsureFormidableShape_AppendsToExistingGitignore(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-gi-preserve")
	seedFile(t, srv, "shape-gi-preserve", "README.md", "hi\n", "seed readme")
	seedFile(t, srv, "shape-gi-preserve", "templates/basic.yaml", "name: Basic\n", "seed tpl")
	seedFile(t, srv, "shape-gi-preserve", "storage/.gitkeep", "", "seed storage")
	seedFile(t, srv, "shape-gi-preserve", formidableMarkerPath,
		`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`+"\n",
		"seed marker")
	existing := "*.tmp\nnode_modules/\n"
	seedFile(t, srv, "shape-gi-preserve", gitignorePath, existing, "seed custom gitignore")

	added, err := ensureFormidableShape(srv.git, "shape-gi-preserve", time.Now())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if !contains(added, gitignorePath) {
		t.Fatalf("expected %q in added list %v (entry was missing)", gitignorePath, added)
	}
	f, _ := srv.git.File("shape-gi-preserve", "", gitignorePath)
	raw, _ := base64.StdEncoding.DecodeString(f.ContentB64)
	got := string(raw)
	if !strings.Contains(got, "*.tmp") || !strings.Contains(got, "node_modules/") {
		t.Errorf("existing entries lost; got:\n%s", got)
	}
	if !gitignoreHasEntry(raw, gitignoreLedgerEntry) {
		t.Errorf("ledger entry not appended; got:\n%s", got)
	}
}

// TestEnsureFormidableGitignore_AutofixOnExistingFormidableRepo proves
// the post-write self-heal hook: a Formidable-first repo that predates
// the .gitignore fix gets the entry injected on the next REST write
// without the user doing anything.
func TestEnsureFormidableGitignore_AutofixOnExistingFormidableRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("autofix-exists")
	seedFile(t, srv, "autofix-exists", "README.md", "hi\n", "seed readme")
	seedFile(t, srv, "autofix-exists", "templates/basic.yaml", "name: Basic\n", "seed tpl")
	seedFile(t, srv, "autofix-exists", "storage/.gitkeep", "", "seed storage")
	seedFile(t, srv, "autofix-exists", formidableMarkerPath,
		`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`+"\n",
		"seed marker")

	sha, err := ensureFormidableGitignore(srv.git, "autofix-exists", time.Now())
	if err != nil {
		t.Fatalf("autofix: %v", err)
	}
	if sha == "" {
		t.Fatal("expected autofix to advance HEAD; got empty sha")
	}
	f, err := srv.git.File("autofix-exists", "", gitignorePath)
	if err != nil {
		t.Fatalf("gitignore should exist after autofix: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(f.ContentB64)
	if !gitignoreHasEntry(raw, gitignoreLedgerEntry) {
		t.Errorf("autofix left .gitignore without the ledger entry; got:\n%s", raw)
	}
}

// TestEnsureFormidableGitignore_SkipsNonFormidableRepo proves the
// narrow-scope guard: a repo without a valid Formidable marker is not
// a formidable-able target, so autofix does nothing. Without this
// guard a plain repo would accidentally gain a .gitignore on its next
// write — silent conversion.
func TestEnsureFormidableGitignore_SkipsNonFormidableRepo(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("autofix-plain")
	seedFile(t, srv, "autofix-plain", "README.md", "hi\n", "seed readme")

	sha, err := ensureFormidableGitignore(srv.git, "autofix-plain", time.Now())
	if err != nil {
		t.Fatalf("autofix: %v", err)
	}
	if sha != "" {
		t.Errorf("expected no-op on non-Formidable repo; got sha %q", sha)
	}
	if _, err := srv.git.File("autofix-plain", "", gitignorePath); err == nil {
		t.Error(".gitignore should not have been added to a non-Formidable repo")
	}
}

// TestEnsureFormidableGitignore_NoopWhenEntryPresent — idempotent path.
func TestEnsureFormidableGitignore_NoopWhenEntryPresent(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("autofix-noop")
	seedFile(t, srv, "autofix-noop", formidableMarkerPath,
		`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`+"\n",
		"seed marker")
	seedFile(t, srv, "autofix-noop", gitignorePath,
		gitignoreLedgerEntry+"\n", "seed gitignore")
	before, _ := srv.git.Head("autofix-noop")

	sha, err := ensureFormidableGitignore(srv.git, "autofix-noop", time.Now())
	if err != nil {
		t.Fatalf("autofix: %v", err)
	}
	if sha != "" {
		t.Errorf("expected no-op when entry present; got sha %q", sha)
	}
	after, _ := srv.git.Head("autofix-noop")
	if after.Version != before.Version {
		t.Error("HEAD must not advance when .gitignore already has the entry")
	}
}

// TestEnsureFormidableShape_SkipsGitignoreWhenEntryPresent proves the
// "don't spam commits" half: when the .gitignore already lists the
// ledger entry, ensure does not add or rewrite it — no-op for that
// file even if other shape pieces are missing.
func TestEnsureFormidableShape_SkipsGitignoreWhenEntryPresent(t *testing.T) {
	srv := testServer(t)
	srv.git.InitBare("shape-gi-present")
	seedFile(t, srv, "shape-gi-present", "README.md", "hi\n", "seed readme")
	seedFile(t, srv, "shape-gi-present", "templates/basic.yaml", "name: Basic\n", "seed tpl")
	seedFile(t, srv, "shape-gi-present", "storage/.gitkeep", "", "seed storage")
	seedFile(t, srv, "shape-gi-present", formidableMarkerPath,
		`{"version":1,"scaffolded_by":"gigot","scaffolded_at":"2024-01-01T00:00:00Z"}`+"\n",
		"seed marker")
	seedFile(t, srv, "shape-gi-present", gitignorePath,
		"*.tmp\n"+gitignoreLedgerEntry+"\n", "seed gitignore with entry")

	added, err := ensureFormidableShape(srv.git, "shape-gi-present", time.Now())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if contains(added, gitignorePath) {
		t.Errorf(".gitignore should not be added when entry is already present; added=%v", added)
	}
}

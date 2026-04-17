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

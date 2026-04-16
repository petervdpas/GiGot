package server

import (
	"encoding/json"
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

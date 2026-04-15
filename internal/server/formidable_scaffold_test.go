package server

import (
	"strings"
	"testing"
)

func TestFormidableScaffoldFiles_ExpectedPayload(t *testing.T) {
	files, err := formidableScaffoldFiles()
	if err != nil {
		t.Fatal(err)
	}

	paths := make(map[string][]byte, len(files))
	for _, f := range files {
		paths[f.Path] = f.Content
	}

	// Every starter file the Formidable context needs must survive the walk.
	for _, required := range []string{"README.md", "templates/basic.yaml", "storage/.gitkeep"} {
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
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

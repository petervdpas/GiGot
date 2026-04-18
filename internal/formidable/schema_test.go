package formidable

import (
	"errors"
	"testing"
)

func TestParseRecord_HappyPath(t *testing.T) {
	raw := []byte(`{"meta":{"id":"x","updated":"2025-01-01T00:00:00Z"},"data":{"name":"Oak"}}`)
	rec, err := ParseRecord(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.Meta["id"] != "x" {
		t.Errorf("meta.id = %v, want x", rec.Meta["id"])
	}
	if rec.Data["name"] != "Oak" {
		t.Errorf("data.name = %v, want Oak", rec.Data["name"])
	}
}

func TestParseRecord_EmptyInputYieldsEmptyMaps(t *testing.T) {
	rec, err := ParseRecord(nil)
	if err != nil {
		t.Fatalf("nil input err: %v", err)
	}
	if rec.Meta == nil || rec.Data == nil {
		t.Error("empty Record should have non-nil maps")
	}

	rec, err = ParseRecord([]byte("   \n "))
	if err != nil {
		t.Fatalf("whitespace input err: %v", err)
	}
	if len(rec.Meta) != 0 || len(rec.Data) != 0 {
		t.Error("whitespace input should yield empty maps")
	}
}

func TestParseRecord_MissingSectionsYieldEmptyMaps(t *testing.T) {
	rec, err := ParseRecord([]byte(`{"meta":{"id":"x"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Data == nil {
		t.Error("missing data should default to empty map, not nil")
	}
}

func TestParseRecord_MalformedJSON(t *testing.T) {
	_, err := ParseRecord([]byte(`{"meta":`))
	if err == nil {
		t.Fatal("expected error on malformed input")
	}
	if !errors.Is(err, ErrMalformedRecord) {
		t.Errorf("error should wrap ErrMalformedRecord, got %v", err)
	}
}

func TestCanonicalJSON_SortsKeysAtEveryDepth(t *testing.T) {
	rec := Record{
		Meta: map[string]any{"z": 1, "a": 2},
		Data: map[string]any{
			"b": map[string]any{"y": 1, "x": 2},
			"a": []any{3, 1, 2},
		},
	}
	got, err := rec.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"data":{"a":[3,1,2],"b":{"x":2,"y":1}},"meta":{"a":2,"z":1}}`
	if string(got) != want {
		t.Errorf("canonical json =\n%s\nwant\n%s", got, want)
	}
}

func TestCanonicalJSON_Deterministic(t *testing.T) {
	rec := Record{
		Meta: map[string]any{"updated": "2025-01-01T00:00:00Z"},
		Data: map[string]any{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5},
	}
	a, _ := rec.CanonicalJSON()
	for range 20 {
		b, _ := rec.CanonicalJSON()
		if string(a) != string(b) {
			t.Fatalf("non-deterministic canonical json: %s vs %s", a, b)
		}
	}
}

func TestCanonicalJSON_EmptyRecord(t *testing.T) {
	rec := Record{}
	got, err := rec.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"data":{},"meta":{}}` {
		t.Errorf("empty record canonical = %s", got)
	}
}

package formidable

import (
	"encoding/json"
	"strings"
	"testing"
)

func recordFrom(t *testing.T, jsonStr string) Record {
	t.Helper()
	r, err := ParseRecord([]byte(jsonStr))
	if err != nil {
		t.Fatalf("ParseRecord(%q): %v", jsonStr, err)
	}
	return r
}

func extractData(t *testing.T, merged []byte) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(merged, &envelope); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	return envelope.Data
}

func TestMerge_DisjointDataFieldsAutoMerge(t *testing.T) {
	base := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"name":"Oak","country":"nl"}}`)
	theirs := recordFrom(t, `{"meta":{"updated":"2025-02-01T00:00:00Z"},"data":{"name":"Oak Rd","country":"nl"}}`)
	yours := recordFrom(t, `{"meta":{"updated":"2025-03-01T00:00:00Z"},"data":{"name":"Oak","country":"uk"}}`)

	res, err := Merge("storage/x/r.meta.json", base, theirs, yours)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict != nil {
		t.Fatalf("unexpected conflict: %+v", res.Conflict)
	}
	data := extractData(t, res.Merged)
	if data["name"] != "Oak Rd" {
		t.Errorf("name = %v, want Oak Rd (theirs)", data["name"])
	}
	if data["country"] != "uk" {
		t.Errorf("country = %v, want uk (yours)", data["country"])
	}
}

func TestMerge_SameFieldDifferentValuesLWW_YoursNewer(t *testing.T) {
	base := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"name":"Old"}}`)
	theirs := recordFrom(t, `{"meta":{"updated":"2025-02-01T00:00:00Z"},"data":{"name":"Theirs"}}`)
	yours := recordFrom(t, `{"meta":{"updated":"2025-03-01T00:00:00Z"},"data":{"name":"Yours"}}`)

	res, err := Merge("p", base, theirs, yours)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict != nil {
		t.Fatal("unexpected conflict")
	}
	if extractData(t, res.Merged)["name"] != "Yours" {
		t.Errorf("expected yours to win, got %v", extractData(t, res.Merged)["name"])
	}
}

func TestMerge_SameFieldDifferentValuesLWW_TheirsNewer(t *testing.T) {
	base := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"name":"Old"}}`)
	theirs := recordFrom(t, `{"meta":{"updated":"2025-06-01T00:00:00Z"},"data":{"name":"Theirs"}}`)
	yours := recordFrom(t, `{"meta":{"updated":"2025-03-01T00:00:00Z"},"data":{"name":"Yours"}}`)

	res, _ := Merge("p", base, theirs, yours)
	if extractData(t, res.Merged)["name"] != "Theirs" {
		t.Errorf("expected theirs to win, got %v", extractData(t, res.Merged)["name"])
	}
}

func TestMerge_SameValueBothSidesNoOp(t *testing.T) {
	base := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"name":"Old"}}`)
	theirs := recordFrom(t, `{"meta":{"updated":"2025-02-01T00:00:00Z"},"data":{"name":"Same"}}`)
	yours := recordFrom(t, `{"meta":{"updated":"2025-03-01T00:00:00Z"},"data":{"name":"Same"}}`)

	res, _ := Merge("p", base, theirs, yours)
	if extractData(t, res.Merged)["name"] != "Same" {
		t.Errorf("same value should resolve to that value")
	}
}

func TestMerge_ImmutableMetaViolationReturnsConflict(t *testing.T) {
	base := recordFrom(t, `{"meta":{"created":"2025-01-01T00:00:00Z","updated":"2025-01-01T00:00:00Z"},"data":{}}`)
	theirs := recordFrom(t, `{"meta":{"created":"2025-01-01T00:00:00Z","updated":"2025-02-01T00:00:00Z"},"data":{"x":1}}`)
	yours := recordFrom(t, `{"meta":{"created":"2030-01-01T00:00:00Z","updated":"2025-03-01T00:00:00Z"},"data":{"y":2}}`)

	res, err := Merge("storage/x/r.meta.json", base, theirs, yours)
	if err != nil {
		t.Fatal(err)
	}
	if res.Conflict == nil {
		t.Fatal("expected conflict, got merged")
	}
	if res.Conflict.Path != "storage/x/r.meta.json" {
		t.Errorf("conflict path = %v", res.Conflict.Path)
	}
	if len(res.Conflict.FieldConflicts) != 1 {
		t.Fatalf("conflicts = %+v", res.Conflict.FieldConflicts)
	}
	if res.Conflict.FieldConflicts[0].Key != "created" {
		t.Errorf("expected created conflict, got %+v", res.Conflict.FieldConflicts[0])
	}
	if res.Merged != nil {
		t.Error("Merged should be nil on conflict")
	}
}

func TestMerge_NestedStructureAtomic(t *testing.T) {
	base := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"addr":{"city":"NYC","zip":"10001"}}}`)
	theirs := recordFrom(t, `{"meta":{"updated":"2025-02-01T00:00:00Z"},"data":{"addr":{"city":"NYC","zip":"10002"}}}`)
	yours := recordFrom(t, `{"meta":{"updated":"2025-03-01T00:00:00Z"},"data":{"addr":{"city":"LA","zip":"10001"}}}`)

	res, _ := Merge("p", base, theirs, yours)
	addr := extractData(t, res.Merged)["addr"].(map[string]any)
	// yours is newer → yours wins the whole sub-object, not a deep merge.
	if addr["city"] != "LA" || addr["zip"] != "10001" {
		t.Errorf("nested object should resolve atomically via LWW, got %+v", addr)
	}
}

func TestMerge_OneSideRemovesField(t *testing.T) {
	base := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"name":"Old"}}`)
	theirs := recordFrom(t, `{"meta":{"updated":"2025-02-01T00:00:00Z"},"data":{}}`)
	yours := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"name":"Old"}}`)

	// theirs removed, yours unchanged → removal wins.
	res, _ := Merge("p", base, theirs, yours)
	data := extractData(t, res.Merged)
	if _, present := data["name"]; present {
		t.Errorf("theirs removed and yours unchanged — expected key gone, got %v", data["name"])
	}
}

func TestMerge_CanonicalOutputIsDeterministic(t *testing.T) {
	base := recordFrom(t, `{"meta":{"updated":"2025-01-01T00:00:00Z"},"data":{"a":1}}`)
	theirs := recordFrom(t, `{"meta":{"updated":"2025-02-01T00:00:00Z"},"data":{"a":1,"b":2}}`)
	yours := recordFrom(t, `{"meta":{"updated":"2025-03-01T00:00:00Z"},"data":{"a":1,"c":3}}`)

	var prev []byte
	for range 10 {
		res, _ := Merge("p", base, theirs, yours)
		if prev != nil && string(prev) != string(res.Merged) {
			t.Fatalf("non-deterministic output: %s vs %s", prev, res.Merged)
		}
		prev = res.Merged
	}
	if !strings.Contains(string(prev), `"a":1`) || !strings.Contains(string(prev), `"b":2`) || !strings.Contains(string(prev), `"c":3`) {
		t.Errorf("expected a,b,c in merged output: %s", prev)
	}
}

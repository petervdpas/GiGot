package formidable

import (
	"encoding/json"
	"testing"
)

func mustRecord(t *testing.T, raw string) Record {
	t.Helper()
	rec, err := ParseRecord([]byte(raw))
	if err != nil {
		t.Fatalf("ParseRecord %q: %v", raw, err)
	}
	return rec
}

func TestParseCondition_Operators(t *testing.T) {
	cases := []struct {
		in     string
		wantOp string
		wantK  string
		wantV  string
	}{
		{"city=London", "=", "city", "London"},
		{"name!=Oak", "!=", "name", "Oak"},
		{"range>5", ">", "range", "5"},
		{"range>=5", ">=", "range", "5"},
		{"range<5", "<", "range", "5"},
		{"range<=5", "<=", "range", "5"},
	}
	for _, c := range cases {
		got, err := ParseCondition(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if got.Key != c.wantK || got.Op != c.wantOp || got.Value != c.wantV {
			t.Errorf("%q = %+v, want {%s %s %s}", c.in, got, c.wantK, c.wantOp, c.wantV)
		}
	}
}

func TestParseCondition_Invalid(t *testing.T) {
	for _, in := range []string{"", "city", "=London", ">"} {
		if _, err := ParseCondition(in); err == nil {
			t.Errorf("%q: expected error, got nil", in)
		}
	}
}

func TestCondition_MatchEquality(t *testing.T) {
	rec := mustRecord(t, `{"data":{"city":"London","count":7}}`)
	c, _ := ParseCondition("city=London")
	if !c.Match(rec) {
		t.Error("expected match on city=London")
	}
	c, _ = ParseCondition("city=Paris")
	if c.Match(rec) {
		t.Error("expected no match on city=Paris")
	}
	c, _ = ParseCondition("city!=Paris")
	if !c.Match(rec) {
		t.Error("expected match on city!=Paris")
	}
}

func TestCondition_MatchNumeric(t *testing.T) {
	rec := mustRecord(t, `{"data":{"count":7}}`)
	cases := []struct {
		expr string
		want bool
	}{
		{"count>5", true},
		{"count>=7", true},
		{"count>7", false},
		{"count<10", true},
		{"count<=7", true},
		{"count<7", false},
	}
	for _, c := range cases {
		cond, _ := ParseCondition(c.expr)
		if got := cond.Match(rec); got != c.want {
			t.Errorf("%q: got %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestCondition_MatchMissingKey(t *testing.T) {
	rec := mustRecord(t, `{"data":{"city":"London"}}`)
	c, _ := ParseCondition("country=nl")
	if c.Match(rec) {
		t.Error("missing key should not match")
	}
}

func TestCondition_NumericAgainstString(t *testing.T) {
	rec := mustRecord(t, `{"data":{"city":"London"}}`)
	c, _ := ParseCondition("city>5")
	if c.Match(rec) {
		t.Error("non-numeric field should not match numeric op")
	}
}

func TestFilterRecords_WhereSortLimit(t *testing.T) {
	recs := []Record{
		mustRecord(t, `{"data":{"city":"London","count":7}}`),
		mustRecord(t, `{"data":{"city":"Paris","count":3}}`),
		mustRecord(t, `{"data":{"city":"London","count":12}}`),
	}
	where, _ := ParseCondition("city=London")
	out := FilterRecords(recs, &where, "count", 0)
	if len(out) != 2 {
		t.Fatalf("filter: got %d, want 2", len(out))
	}
	// Ascending by count → 7 then 12.
	first, _ := out[0].Data["count"].(json.Number).Int64()
	if first != 7 {
		t.Errorf("first count: got %d, want 7", first)
	}
	// Descending.
	out = FilterRecords(recs, &where, "-count", 1)
	if len(out) != 1 {
		t.Fatalf("limit: got %d, want 1", len(out))
	}
	top, _ := out[0].Data["count"].(json.Number).Int64()
	if top != 12 {
		t.Errorf("top count: got %d, want 12", top)
	}
}

func TestFilterRecords_NoWhereReturnsAll(t *testing.T) {
	recs := []Record{
		mustRecord(t, `{"data":{"city":"London"}}`),
		mustRecord(t, `{"data":{"city":"Paris"}}`),
	}
	out := FilterRecords(recs, nil, "", 0)
	if len(out) != 2 {
		t.Errorf("got %d, want 2", len(out))
	}
}

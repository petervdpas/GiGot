package formidable

import (
	"testing"
)

func TestMergeMeta_UpdatedTakesMax(t *testing.T) {
	theirs := map[string]any{"updated": "2025-01-01T00:00:00Z"}
	yours := map[string]any{"updated": "2025-06-01T00:00:00Z"}
	merged, conflicts := MergeMeta(nil, theirs, yours)
	if len(conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", conflicts)
	}
	if merged["updated"] != "2025-06-01T00:00:00Z" {
		t.Errorf("updated = %v, want yours (newer)", merged["updated"])
	}
}

func TestMergeMeta_UpdatedWithOneMissing(t *testing.T) {
	theirs := map[string]any{"updated": "2025-01-01T00:00:00Z"}
	merged, _ := MergeMeta(nil, theirs, map[string]any{})
	if merged["updated"] != "2025-01-01T00:00:00Z" {
		t.Errorf("updated should take theirs when yours missing: %v", merged["updated"])
	}
}

func TestMergeMeta_TagsSetUnionNormalised(t *testing.T) {
	theirs := map[string]any{"tags": []any{"Home", " office ", "home"}}
	yours := map[string]any{"tags": []any{"OFFICE", "travel", ""}}
	merged, _ := MergeMeta(nil, theirs, yours)
	got, ok := merged["tags"].([]any)
	if !ok {
		t.Fatalf("tags not a slice: %T", merged["tags"])
	}
	want := []string{"home", "office", "travel"}
	if len(got) != len(want) {
		t.Fatalf("tags len %d, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("tags[%d] = %v, want %v", i, got[i], w)
		}
	}
}

func TestMergeMeta_FlaggedTrueWins(t *testing.T) {
	merged, _ := MergeMeta(nil,
		map[string]any{"flagged": false},
		map[string]any{"flagged": true})
	if merged["flagged"] != true {
		t.Errorf("flagged should be true, got %v", merged["flagged"])
	}

	merged, _ = MergeMeta(nil,
		map[string]any{"flagged": false},
		map[string]any{"flagged": false})
	if merged["flagged"] != false {
		t.Errorf("flagged both-false should be false, got %v", merged["flagged"])
	}
}

func TestMergeMeta_ImmutableCreatedDivergenceConflicts(t *testing.T) {
	base := map[string]any{"created": "2025-01-01T00:00:00Z"}
	theirs := map[string]any{"created": "2025-01-01T00:00:00Z"}
	yours := map[string]any{"created": "2030-01-01T00:00:00Z"}
	_, conflicts := MergeMeta(base, theirs, yours)
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	c := conflicts[0]
	if c.Scope != "meta" || c.Key != "created" || c.Reason != "immutable" {
		t.Errorf("conflict = %+v", c)
	}
}

func TestMergeMeta_ImmutableAllThreeKeys(t *testing.T) {
	base := map[string]any{
		"created":  "2025-01-01T00:00:00Z",
		"id":       "abc",
		"template": "addresses.yaml",
	}
	theirs := map[string]any{
		"created":  "2030-01-01T00:00:00Z",
		"id":       "abc",
		"template": "addresses.yaml",
	}
	yours := map[string]any{
		"created":  "2025-01-01T00:00:00Z",
		"id":       "xyz",
		"template": "other.yaml",
	}
	_, conflicts := MergeMeta(base, theirs, yours)
	if len(conflicts) != 3 {
		t.Fatalf("expected 3 conflicts, got %d: %+v", len(conflicts), conflicts)
	}
	seen := map[string]bool{}
	for _, c := range conflicts {
		seen[c.Key] = true
		if c.Reason != "immutable" || c.Scope != "meta" {
			t.Errorf("unexpected conflict shape: %+v", c)
		}
	}
	for _, k := range []string{"created", "id", "template"} {
		if !seen[k] {
			t.Errorf("missing conflict for %s", k)
		}
	}
}

func TestMergeMeta_NoBaseAllowsFirstWrite(t *testing.T) {
	theirs := map[string]any{"created": "2025-01-01T00:00:00Z", "id": "x"}
	yours := map[string]any{"created": "2025-01-01T00:00:00Z", "id": "x"}
	merged, conflicts := MergeMeta(nil, theirs, yours)
	if len(conflicts) != 0 {
		t.Fatalf("no base should not produce conflicts: %+v", conflicts)
	}
	if merged["created"] != "2025-01-01T00:00:00Z" || merged["id"] != "x" {
		t.Errorf("merged = %+v", merged)
	}
}

func TestMergeMeta_AuthorFollowsUpdatedWinner(t *testing.T) {
	theirs := map[string]any{
		"updated":     "2025-01-01T00:00:00Z",
		"author_name": "alice",
	}
	yours := map[string]any{
		"updated":     "2025-06-01T00:00:00Z",
		"author_name": "bob",
	}
	merged, _ := MergeMeta(nil, theirs, yours)
	if merged["author_name"] != "bob" {
		t.Errorf("author_name should follow updated winner (yours): %v", merged["author_name"])
	}
}

func TestMergeMeta_GigotEnabledFollowsUpdatedWinner(t *testing.T) {
	theirs := map[string]any{
		"updated":       "2025-06-01T00:00:00Z",
		"gigot_enabled": true,
	}
	yours := map[string]any{
		"updated":       "2025-01-01T00:00:00Z",
		"gigot_enabled": false,
	}
	merged, _ := MergeMeta(nil, theirs, yours)
	if merged["gigot_enabled"] != true {
		t.Errorf("gigot_enabled should follow updated winner (theirs=true): %v", merged["gigot_enabled"])
	}
}

func TestMergeMeta_ArbitraryExtraKeyFollowsUpdatedWinner(t *testing.T) {
	theirs := map[string]any{
		"updated":     "2025-01-01T00:00:00Z",
		"custom_note": "t-note",
	}
	yours := map[string]any{
		"updated":     "2025-06-01T00:00:00Z",
		"custom_note": "y-note",
	}
	merged, _ := MergeMeta(nil, theirs, yours)
	if merged["custom_note"] != "y-note" {
		t.Errorf("arbitrary meta key should follow updated winner: %v", merged["custom_note"])
	}
}

func TestUpdatedWinner_EqualTimestampsPrefersYours(t *testing.T) {
	theirs := map[string]any{"updated": "2025-01-01T00:00:00Z"}
	yours := map[string]any{"updated": "2025-01-01T00:00:00Z"}
	if got := UpdatedWinner(theirs, yours); got != "yours" {
		t.Errorf("equal timestamps should prefer yours, got %s", got)
	}
}

func TestUpdatedWinner_BothMissingPrefersYours(t *testing.T) {
	if got := UpdatedWinner(map[string]any{}, map[string]any{}); got != "yours" {
		t.Errorf("both missing should prefer yours, got %s", got)
	}
}

func TestUpdatedWinner_UnparsableFallsBackToPresentSide(t *testing.T) {
	theirs := map[string]any{"updated": "not-a-date"}
	yours := map[string]any{"updated": "2025-01-01T00:00:00Z"}
	if got := UpdatedWinner(theirs, yours); got != "yours" {
		t.Errorf("unparsable theirs should lose to parsable yours, got %s", got)
	}
}

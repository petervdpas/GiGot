package formidable

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// Immutable meta keys that may not change after record creation.
// Any divergence between base and theirs/yours on these keys emits a
// FieldConflict{Reason:"immutable"} and blocks the merge.
var immutableMetaKeys = []string{"created", "id", "template"}

// MergeMeta applies the §10.2 rules for the meta map. base may be nil
// (first-ever write of a record) — in that case immutability checks
// degrade to "take whichever side has the value". The returned merged
// map is independent of all inputs (safe to mutate).
func MergeMeta(base, theirs, yours map[string]any) (map[string]any, []FieldConflict) {
	merged := map[string]any{}
	var conflicts []FieldConflict

	winner := UpdatedWinner(theirs, yours)

	// Immutability check on fixed keys, against base when available.
	if base != nil {
		for _, k := range immutableMetaKeys {
			bv, bok := base[k]
			tv, tok := theirs[k]
			yv, yok := yours[k]
			divergent := false
			if bok && tok && !deepEqual(bv, tv) {
				divergent = true
			}
			if bok && yok && !deepEqual(bv, yv) {
				divergent = true
			}
			if divergent {
				conflicts = append(conflicts, FieldConflict{
					Scope:  "meta",
					Key:    k,
					Reason: "immutable",
				})
			}
		}
	}

	// Walk the union of keys and apply per-key rules. Even if an
	// immutability conflict fired above we still populate merged —
	// the caller decides whether to use merged or emit the conflicts.
	for _, k := range unionKeys(base, theirs, yours) {
		switch k {
		case "updated":
			merged[k] = mergeUpdated(theirs[k], yours[k])
		case "tags":
			merged[k] = mergeTags(theirs[k], yours[k])
		case "flagged":
			merged[k] = mergeFlagged(theirs[k], yours[k])
		case "created", "id", "template":
			// Immutable: take base if present, else whichever side has it.
			// If both sides match each other (normal case), take that.
			if base != nil {
				if v, ok := base[k]; ok {
					merged[k] = v
					continue
				}
			}
			if v, ok := theirs[k]; ok {
				merged[k] = v
			} else if v, ok := yours[k]; ok {
				merged[k] = v
			}
		default:
			// Follow the updated winner for every other key.
			merged[k] = pickWinner(theirs, yours, k, winner)
		}
	}

	return merged, conflicts
}

// UpdatedWinner returns "theirs" or "yours" based on max(meta.updated).
// Non-RFC3339 or missing values fall back to "yours" so a write with
// a fresher clock on either side still deterministically resolves.
// Equal timestamps resolve to "yours" (the incoming side) — arbitrary
// but stable.
func UpdatedWinner(theirs, yours map[string]any) string {
	tt, tok := parseUpdated(theirs)
	yt, yok := parseUpdated(yours)
	switch {
	case tok && yok:
		if tt.After(yt) {
			return "theirs"
		}
		return "yours"
	case tok && !yok:
		return "theirs"
	default:
		return "yours"
	}
}

func parseUpdated(m map[string]any) (time.Time, bool) {
	if m == nil {
		return time.Time{}, false
	}
	v, ok := m["updated"]
	if !ok {
		return time.Time{}, false
	}
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func mergeUpdated(theirs, yours any) any {
	ts, tok := asTime(theirs)
	ys, yok := asTime(yours)
	switch {
	case tok && yok:
		if ts.After(ys) {
			return theirs
		}
		return yours
	case tok:
		return theirs
	case yok:
		return yours
	default:
		// Neither parsed — pick whichever is non-nil (prefer yours).
		if yours != nil {
			return yours
		}
		return theirs
	}
}

func asTime(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func mergeTags(theirs, yours any) any {
	set := map[string]struct{}{}
	collect := func(v any) {
		arr, ok := v.([]any)
		if !ok {
			return
		}
		for _, el := range arr {
			s, ok := el.(string)
			if !ok {
				continue
			}
			n := strings.ToLower(strings.TrimSpace(s))
			if n == "" {
				continue
			}
			set[n] = struct{}{}
		}
	}
	collect(theirs)
	collect(yours)

	out := make([]any, 0, len(set))
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k)
	}
	return out
}

func mergeFlagged(theirs, yours any) any {
	if asBool(theirs) || asBool(yours) {
		return true
	}
	return false
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func pickWinner(theirs, yours map[string]any, key, winner string) any {
	tv, tok := theirs[key]
	yv, yok := yours[key]
	switch {
	case tok && yok:
		if winner == "theirs" {
			return tv
		}
		return yv
	case tok:
		return tv
	case yok:
		return yv
	default:
		return nil
	}
}

func unionKeys(maps ...map[string]any) []string {
	seen := map[string]struct{}{}
	for _, m := range maps {
		for k := range m {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// deepEqual compares decoded JSON values for semantic equality. It
// normalises json.Number to its string form so two decoders that
// differ on number strategy still compare equal when the underlying
// text is the same.
func deepEqual(a, b any) bool {
	ab, errA := json.Marshal(canonicaliseForCompare(a))
	bb, errB := json.Marshal(canonicaliseForCompare(b))
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}

func canonicaliseForCompare(v any) any {
	switch t := v.(type) {
	case json.Number:
		return t.String()
	case map[string]any:
		out := map[string]any{}
		for k, val := range t {
			out[k] = canonicaliseForCompare(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, el := range t {
			out[i] = canonicaliseForCompare(el)
		}
		return out
	default:
		return v
	}
}

package formidable

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Condition is a parsed filter expression like `city=London` or
// `range>5`. Scope in F4 is a single condition over a scalar
// `data.<key>`; richer grammars can come later.
type Condition struct {
	Key   string
	Op    string
	Value string
}

// ParseCondition splits "key<op>value" into its parts. Operators are
// recognised longest-first so `>=` doesn't mis-parse as `>`. Returns
// an error on empty key or unknown operator.
func ParseCondition(expr string) (Condition, error) {
	for _, op := range []string{">=", "<=", "!=", "=", ">", "<"} {
		i := strings.Index(expr, op)
		if i <= 0 {
			continue
		}
		key := strings.TrimSpace(expr[:i])
		val := strings.TrimSpace(expr[i+len(op):])
		if key == "" {
			continue
		}
		return Condition{Key: key, Op: op, Value: val}, nil
	}
	return Condition{}, fmt.Errorf("formidable: invalid filter %q", expr)
}

// Match returns true when rec.Data[c.Key] satisfies the condition.
// Numeric comparisons try to parse both sides as float64; otherwise
// comparisons fall back to string form. A missing key is treated as
// never matching.
func (c Condition) Match(rec Record) bool {
	raw, ok := rec.Data[c.Key]
	if !ok {
		return false
	}
	gotStr := valueToString(raw)

	switch c.Op {
	case "=":
		return gotStr == c.Value
	case "!=":
		return gotStr != c.Value
	}

	gotF, gotOK := parseFloat(gotStr)
	wantF, wantOK := parseFloat(c.Value)
	if !gotOK || !wantOK {
		return false
	}
	switch c.Op {
	case ">":
		return gotF > wantF
	case ">=":
		return gotF >= wantF
	case "<":
		return gotF < wantF
	case "<=":
		return gotF <= wantF
	}
	return false
}

// FilterRecords applies an optional condition, sorts by the given
// data key (ascending; prefix "-" for descending), and applies a
// non-negative limit. A zero limit means "no limit". The returned
// slice is a newly-allocated copy.
func FilterRecords(records []Record, where *Condition, sortKey string, limit int) []Record {
	out := make([]Record, 0, len(records))
	for _, r := range records {
		if where != nil && !where.Match(r) {
			continue
		}
		out = append(out, r)
	}
	if sortKey != "" {
		desc := false
		key := sortKey
		if strings.HasPrefix(key, "-") {
			desc = true
			key = key[1:]
		}
		sortRecords(out, key, desc)
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func sortRecords(records []Record, key string, desc bool) {
	less := func(i, j int) bool {
		a := valueToString(records[i].Data[key])
		b := valueToString(records[j].Data[key])
		af, aOK := parseFloat(a)
		bf, bOK := parseFloat(b)
		if aOK && bOK {
			if desc {
				return af > bf
			}
			return af < bf
		}
		if desc {
			return a > b
		}
		return a < b
	}
	// Simple insertion sort keeps this file stdlib-free of `sort`
	// wrappers and is plenty fast for realistic record counts.
	for i := 1; i < len(records); i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			records[j], records[j-1] = records[j-1], records[j]
		}
	}
}

func valueToString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String()
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	}
	return fmt.Sprintf("%v", v)
}

func parseFloat(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

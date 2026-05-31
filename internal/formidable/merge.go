package formidable

// MergeResult is the outcome of a record-level merge. Exactly one of
// Merged or Conflict is non-nil on success; Conflict is reserved for
// immutable-meta violations, which short-circuit the data merge.
type MergeResult struct {
	Merged   []byte
	Conflict *RecordConflict
}

// Merge applies §10.2 + §10.3 to produce a merged record. The field is the
// atomic unit (a nested object included): neither-side-changed keeps base;
// exactly-one-side-changed takes that side (a removal included); both sides
// changing the same field to the same value is a no-op. Both sides changing
// the same field to different values, or one changing while the other removes,
// is a conflict surfaced to the user, never silently last-writer-wins. Meta
// conflicts (immutable fields) and data conflicts are reported together.
func Merge(path string, base, theirs, yours Record) (MergeResult, error) {
	mergedMeta, conflicts := MergeMeta(base.Meta, theirs.Meta, yours.Meta)

	mergedData := map[string]any{}
	for _, key := range unionKeys(base.Data, theirs.Data, yours.Data) {
		bv, bok := base.Data[key]
		tv, tok := theirs.Data[key]
		yv, yok := yours.Data[key]

		tChanged := !sameField(bok, bv, tok, tv)
		yChanged := !sameField(bok, bv, yok, yv)

		switch {
		case !tChanged && !yChanged:
			if bok {
				mergedData[key] = bv
			}
		case tChanged && !yChanged:
			if tok {
				mergedData[key] = tv
			}
		case !tChanged && yChanged:
			if yok {
				mergedData[key] = yv
			}
		default:
			// Both sides changed the field. Identical changes converge;
			// anything else (different values, or modify vs remove) is a
			// conflict the user resolves.
			if sameField(tok, tv, yok, yv) {
				if tok {
					mergedData[key] = tv
				}
				continue
			}
			conflicts = append(conflicts, FieldConflict{Scope: "data", Key: key})
		}
	}

	if len(conflicts) > 0 {
		return MergeResult{
			Conflict: &RecordConflict{
				Path:           path,
				FieldConflicts: conflicts,
			},
		}, nil
	}

	out := Record{Meta: mergedMeta, Data: mergedData}
	bytes, err := out.CanonicalJSON()
	if err != nil {
		return MergeResult{}, err
	}
	return MergeResult{Merged: bytes}, nil
}

// sameField reports whether a key has identical presence and value on two
// sides. Absent-on-both counts as same; present-vs-absent never does.
func sameField(aok bool, av any, bok bool, bv any) bool {
	if aok != bok {
		return false
	}
	if !aok {
		return true
	}
	return deepEqual(av, bv)
}

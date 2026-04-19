package git

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// RefChangeKind tags how a ref moved between two snapshots.
type RefChangeKind string

const (
	RefCreated RefChangeKind = "created"
	RefUpdated RefChangeKind = "updated"
	RefDeleted RefChangeKind = "deleted"
)

// RefChange describes one ref's movement across a git-receive-pack boundary.
// OldSHA is "" for created refs; NewSHA is "" for deleted refs.
type RefChange struct {
	Ref    string
	OldSHA string
	NewSHA string
	Kind   RefChangeKind
}

// RefSnapshot captures every non-audit ref's current SHA. Used on either
// side of a git-receive-pack run so the push_received audit path can name
// exactly which refs moved. refs/audit/* are excluded so the audit writer's
// own advance never shows up as a pushed change.
func (m *Manager) RefSnapshot(name string) (map[string]string, error) {
	if !m.Exists(name) {
		return nil, fmt.Errorf("repository %q does not exist", name)
	}
	out, err := exec.Command("git", "-C", m.RepoPath(name),
		"for-each-ref", "--format=%(refname) %(objectname)").Output()
	if err != nil {
		return nil, fmt.Errorf("for-each-ref: %w", err)
	}
	snap := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}
		ref, sha := parts[0], parts[1]
		if strings.HasPrefix(ref, "refs/audit/") {
			continue
		}
		snap[ref] = sha
	}
	return snap, nil
}

// DiffRefSnapshots returns one entry per ref whose SHA differs (or which
// appeared / disappeared) between before and after. Order is alphabetical
// by ref name so audit output is stable across runs.
func DiffRefSnapshots(before, after map[string]string) []RefChange {
	seen := make(map[string]struct{}, len(before)+len(after))
	for ref := range before {
		seen[ref] = struct{}{}
	}
	for ref := range after {
		seen[ref] = struct{}{}
	}
	refs := make([]string, 0, len(seen))
	for ref := range seen {
		refs = append(refs, ref)
	}
	sort.Strings(refs)

	var changes []RefChange
	for _, ref := range refs {
		old, hadOld := before[ref]
		new, hasNew := after[ref]
		switch {
		case !hadOld && hasNew:
			changes = append(changes, RefChange{Ref: ref, NewSHA: new, Kind: RefCreated})
		case hadOld && !hasNew:
			changes = append(changes, RefChange{Ref: ref, OldSHA: old, Kind: RefDeleted})
		case old != new:
			changes = append(changes, RefChange{Ref: ref, OldSHA: old, NewSHA: new, Kind: RefUpdated})
		}
	}
	return changes
}

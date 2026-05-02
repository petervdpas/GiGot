// Package tags stores the server-wide tag catalogue plus the three
// assignment sets that link tags to repos, subscriptions, and
// accounts. One sealed file `data/tags.enc` carries all four — the
// catalogue is the source of truth for tag identity (rename through
// the catalogue propagates everywhere because assignments reference
// tags by ID, not name).
//
// Slice 1 of the tags rollout (see docs/design/tags.md §10) only
// exercises the catalogue write paths from the API; the assignment
// helpers below are wired up here so slice 2 can land assignment
// endpoints without a schema migration. Reads of effective tag sets
// also rely on these helpers being present from day one — the sealed
// file format does not need to change between slices.
package tags

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/petervdpas/GiGot/internal/crypto"
)

var (
	ErrNotFound       = errors.New("tags: not found")
	ErrNameRequired   = errors.New("tags: name required")
	ErrNameInvalid    = errors.New("tags: name invalid")
	ErrNameDuplicate  = errors.New("tags: name already exists")
	ErrAssignmentMiss = errors.New("tags: assignment not found")
)

// maxNameLen mirrors §8 of the design doc — long enough for
// `team:marketing-emea` shapes, short enough to keep the URL-path
// surface clean.
const maxNameLen = 64

// Tag is one row in the catalogue. ID is server-generated so renames
// don't break existing assignments — every assignment row references
// a tag by ID.
type Tag struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	CreatedBy string    `json:"created_by,omitempty"`
}

// Assignment is one (entity, tag) pair. EntityID is opaque to this
// package — the caller picks the format (repo name, subscription ID,
// account ID) and the store just round-trips it.
type Assignment struct {
	EntityID  string    `json:"entity_id"`
	TagID     string    `json:"tag_id"`
	TaggedAt  time.Time `json:"tagged_at"`
	TaggedBy  string    `json:"tagged_by,omitempty"`
}

// fileLayout is the on-disk JSON shape. Kept as a private struct so
// the public API (Tag, Assignment) doesn't leak the storage layout.
type fileLayout struct {
	Tags                    []*Tag        `json:"tags"`
	RepoAssignments         []*Assignment `json:"repo_assignments"`
	SubscriptionAssignments []*Assignment `json:"subscription_assignments"`
	AccountAssignments      []*Assignment `json:"account_assignments"`
}

// Store holds the catalogue + three assignment sets, persisted to a
// single sealed file on disk.
type Store struct {
	file *crypto.SealedFile

	mu                      sync.RWMutex
	tags                    map[string]*Tag        // by ID
	nameIndex               map[string]string      // lower(name) → ID
	repoAssignments         []*Assignment
	subscriptionAssignments []*Assignment
	accountAssignments      []*Assignment
}

// Open loads (or initialises) the tags store at path, sealed to enc.
func Open(path string, enc *crypto.Encryptor) (*Store, error) {
	f, err := crypto.NewSealedFile(path, enc)
	if err != nil {
		return nil, fmt.Errorf("tags: %w", err)
	}
	s := &Store{
		file:      f,
		tags:      make(map[string]*Tag),
		nameIndex: make(map[string]string),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	plain, err := s.file.Load()
	if err != nil {
		return fmt.Errorf("tags: %w", err)
	}
	if plain == nil {
		return nil
	}
	var layout fileLayout
	if err := json.Unmarshal(plain, &layout); err != nil {
		return fmt.Errorf("tags: parse: %w", err)
	}
	for _, t := range layout.Tags {
		s.tags[t.ID] = t
		s.nameIndex[strings.ToLower(t.Name)] = t.ID
	}
	s.repoAssignments = layout.RepoAssignments
	s.subscriptionAssignments = layout.SubscriptionAssignments
	s.accountAssignments = layout.AccountAssignments
	return nil
}

func (s *Store) persist() error {
	layout := fileLayout{
		Tags:                    make([]*Tag, 0, len(s.tags)),
		RepoAssignments:         s.repoAssignments,
		SubscriptionAssignments: s.subscriptionAssignments,
		AccountAssignments:      s.accountAssignments,
	}
	for _, t := range s.tags {
		layout.Tags = append(layout.Tags, t)
	}
	plain, err := json.Marshal(layout)
	if err != nil {
		return fmt.Errorf("tags: marshal: %w", err)
	}
	return s.file.Save(plain)
}

// newID returns a short URL-safe opaque ID. 12 random bytes = 16
// chars after base64 URL encoding with padding stripped — same
// pattern destinations.go uses, plenty of entropy for the catalogue.
func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// validateName trims, then enforces the length / character rules from
// the design doc §8: not empty, ≤ 64 chars, no path-segment-breaking
// characters. The trimmed name is what gets stored — leading /
// trailing whitespace is silently dropped on the way in.
func validateName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", ErrNameRequired
	}
	if len(trimmed) > maxNameLen {
		return "", fmt.Errorf("%w: max %d chars", ErrNameInvalid, maxNameLen)
	}
	for _, r := range trimmed {
		if r < 0x20 || r == '/' || r == '?' || r == '#' {
			return "", fmt.Errorf("%w: contains forbidden character %q", ErrNameInvalid, r)
		}
	}
	return trimmed, nil
}

// Create adds a new tag to the catalogue. Names are case-insensitive
// unique — `Team:Marketing` collides with `team:marketing`. The
// display casing of the first creator wins; subsequent collisions
// return ErrNameDuplicate.
func (s *Store) Create(name, createdBy string) (*Tag, error) {
	name, err := validateName(name)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, dup := s.nameIndex[strings.ToLower(name)]; dup {
		return nil, ErrNameDuplicate
	}

	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("tags: id: %w", err)
	}
	t := &Tag{
		ID:        id,
		Name:      name,
		CreatedAt: time.Now().UTC(),
		CreatedBy: createdBy,
	}
	s.tags[id] = t
	s.nameIndex[strings.ToLower(name)] = id
	if err := s.persist(); err != nil {
		delete(s.tags, id)
		delete(s.nameIndex, strings.ToLower(name))
		return nil, err
	}
	cp := *t
	return &cp, nil
}

// Get returns a single tag by ID.
func (s *Store) Get(id string) (*Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tags[id]
	if !ok {
		return nil, ErrNotFound
	}
	cp := *t
	return &cp, nil
}

// GetByName returns a tag by case-insensitive name lookup.
func (s *Store) GetByName(name string) (*Tag, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.nameIndex[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return nil, ErrNotFound
	}
	t := s.tags[id]
	cp := *t
	return &cp, nil
}

// All returns a snapshot of every tag in the catalogue.
func (s *Store) All() []*Tag {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Tag, 0, len(s.tags))
	for _, t := range s.tags {
		cp := *t
		out = append(out, &cp)
	}
	return out
}

// Rename updates a tag's display name. Case-insensitive collision
// against any *other* tag is rejected. Renaming a tag to a different
// casing of its existing name (`team:mktg` → `Team:Mktg`) is allowed
// — the lower-case index entry stays the same so the rename is just
// a display-string update.
func (s *Store) Rename(id, newName string) (*Tag, error) {
	newName, err := validateName(newName)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tags[id]
	if !ok {
		return nil, ErrNotFound
	}
	oldNameLower := strings.ToLower(t.Name)
	newNameLower := strings.ToLower(newName)

	if oldNameLower != newNameLower {
		if _, dup := s.nameIndex[newNameLower]; dup {
			return nil, ErrNameDuplicate
		}
	}

	oldName := t.Name
	t.Name = newName
	delete(s.nameIndex, oldNameLower)
	s.nameIndex[newNameLower] = id

	if err := s.persist(); err != nil {
		t.Name = oldName
		delete(s.nameIndex, newNameLower)
		s.nameIndex[oldNameLower] = id
		return nil, err
	}
	cp := *t
	return &cp, nil
}

// DeleteResult reports the cascade sweep counts so callers (audit
// log, UI confirm dialog) can show what just got removed.
type DeleteResult struct {
	RepoAssignments         int
	SubscriptionAssignments int
	AccountAssignments      int
}

// Delete removes a tag and cascade-removes every assignment that
// referenced it across all three join sets. Returns the per-set
// counts that were swept so the audit event can record the blast
// radius (design §6.1).
func (s *Store) Delete(id string) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.tags[id]
	if !ok {
		return DeleteResult{}, ErrNotFound
	}

	repoSwept, repoKept := splitByTag(s.repoAssignments, id)
	subSwept, subKept := splitByTag(s.subscriptionAssignments, id)
	accSwept, accKept := splitByTag(s.accountAssignments, id)

	prevName := t.Name
	prevTag := *t

	delete(s.tags, id)
	delete(s.nameIndex, strings.ToLower(prevName))
	prevRepo := s.repoAssignments
	prevSub := s.subscriptionAssignments
	prevAcc := s.accountAssignments
	s.repoAssignments = repoKept
	s.subscriptionAssignments = subKept
	s.accountAssignments = accKept

	if err := s.persist(); err != nil {
		// Roll back every mutation so the in-memory state still
		// matches what's on disk.
		s.tags[id] = &prevTag
		s.nameIndex[strings.ToLower(prevName)] = id
		s.repoAssignments = prevRepo
		s.subscriptionAssignments = prevSub
		s.accountAssignments = prevAcc
		return DeleteResult{}, err
	}

	return DeleteResult{
		RepoAssignments:         len(repoSwept),
		SubscriptionAssignments: len(subSwept),
		AccountAssignments:      len(accSwept),
	}, nil
}

// splitByTag partitions the slice into rows that match tagID
// (returned first) and rows that don't (returned second).
func splitByTag(in []*Assignment, tagID string) (matched, kept []*Assignment) {
	for _, a := range in {
		if a.TagID == tagID {
			matched = append(matched, a)
		} else {
			kept = append(kept, a)
		}
	}
	return matched, kept
}

// Count returns the number of tags in the catalogue.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.tags)
}

// UsageCounts reports how many direct (non-inherited) assignments
// reference each tag, split by entity type. Used by the catalogue
// list endpoint so the admin sees "team:marketing — 4 repos, 18
// subs, 2 accounts" without a separate API roundtrip.
type UsageCounts struct {
	Repos         int `json:"repos"`
	Subscriptions int `json:"subscriptions"`
	Accounts      int `json:"accounts"`
}

// AssignmentScope picks one of the three join sets so the helpers
// don't repeat themselves once per entity type.
type AssignmentScope int

const (
	ScopeRepo AssignmentScope = iota
	ScopeSubscription
	ScopeAccount
)

// SetResult reports what changed in a SetXxxTags call. Callers (the
// handlers) iterate these to emit one audit event per affected
// catalogue row + assignment row, matching the §7.1 contract.
type SetResult struct {
	// CreatedTags is the catalogue-level diff: tag names that were
	// not in the catalogue before this call and were auto-created
	// to satisfy it. Each lands in the system audit log as a
	// tag.created event before the assignment events fire.
	CreatedTags []*Tag
	// Added is the names of tags newly attached to the entity.
	// Each lands as a tag.assigned.<scope> event.
	Added []string
	// Removed is the names of tags newly detached from the entity.
	// Each lands as a tag.unassigned.<scope> event.
	Removed []string
}

// SetRepoTags replaces the direct tag set on a repo. Unknown tag
// names are auto-created in the catalogue (created_by recorded as
// taggedBy). Returns a SetResult describing every catalogue +
// assignment delta so the caller can emit per-change audit events.
func (s *Store) SetRepoTags(repoName string, tagNames []string, taggedBy string) (SetResult, error) {
	return s.setTags(ScopeRepo, repoName, tagNames, taggedBy)
}

// SetSubscriptionTags replaces the direct tag set on a subscription
// keyed by its opaque token. Same auto-create + diff semantics as
// SetRepoTags.
func (s *Store) SetSubscriptionTags(subID string, tagNames []string, taggedBy string) (SetResult, error) {
	return s.setTags(ScopeSubscription, subID, tagNames, taggedBy)
}

// SetAccountTags replaces the direct tag set on an account keyed by
// "<provider>:<identifier>". Same auto-create + diff semantics as
// SetRepoTags.
func (s *Store) SetAccountTags(accountKey string, tagNames []string, taggedBy string) (SetResult, error) {
	return s.setTags(ScopeAccount, accountKey, tagNames, taggedBy)
}

// setTags is the shared replace-the-set implementation. It locks the
// store once for the entire operation so a concurrent rename or
// delete can't race a half-applied set update.
func (s *Store) setTags(scope AssignmentScope, entityID string, names []string, taggedBy string) (SetResult, error) {
	if entityID == "" {
		return SetResult{}, fmt.Errorf("tags: entity id required")
	}

	// Validate + canonicalise every input name *before* mutating any
	// state. Trim whitespace, dedupe (case-insensitive), and reject
	// the call if any single name is malformed — partial application
	// is the worst possible outcome here.
	wantLower := make(map[string]string, len(names))
	for _, raw := range names {
		clean, err := validateName(raw)
		if err != nil {
			return SetResult{}, err
		}
		lower := strings.ToLower(clean)
		if _, dup := wantLower[lower]; !dup {
			wantLower[lower] = clean
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Snapshot for rollback. Cheap — the slices share underlying
	// arrays with the originals; we replace them wholesale on
	// success or restore them on persist failure.
	prevAssignments := s.assignmentsFor(scope)
	prevTags := make(map[string]*Tag, len(s.tags))
	for k, v := range s.tags {
		cp := *v
		prevTags[k] = &cp
	}
	prevNameIndex := make(map[string]string, len(s.nameIndex))
	for k, v := range s.nameIndex {
		prevNameIndex[k] = v
	}

	// Auto-create any tag that doesn't exist yet, recording the new
	// rows so the caller can emit tag.created audit events for them.
	var created []*Tag
	wantTagIDs := make(map[string]struct{}, len(wantLower))
	for lower, display := range wantLower {
		if id, ok := s.nameIndex[lower]; ok {
			wantTagIDs[id] = struct{}{}
			continue
		}
		id, err := newID()
		if err != nil {
			return SetResult{}, fmt.Errorf("tags: id: %w", err)
		}
		t := &Tag{
			ID:        id,
			Name:      display,
			CreatedAt: time.Now().UTC(),
			CreatedBy: taggedBy,
		}
		s.tags[id] = t
		s.nameIndex[lower] = id
		wantTagIDs[id] = struct{}{}
		cp := *t
		created = append(created, &cp)
	}

	// Diff the existing assignment set for this entity against the
	// desired set, building the kept/removed/added partition.
	var keptOther []*Assignment
	haveTagIDs := make(map[string]struct{})
	for _, a := range prevAssignments {
		if a.EntityID != entityID {
			keptOther = append(keptOther, a)
			continue
		}
		haveTagIDs[a.TagID] = struct{}{}
		if _, want := wantTagIDs[a.TagID]; want {
			keptOther = append(keptOther, a)
		}
	}

	var addedNames, removedNames []string
	now := time.Now().UTC()
	newAssignments := keptOther
	for tagID := range wantTagIDs {
		if _, have := haveTagIDs[tagID]; have {
			continue
		}
		newAssignments = append(newAssignments, &Assignment{
			EntityID: entityID,
			TagID:    tagID,
			TaggedAt: now,
			TaggedBy: taggedBy,
		})
		addedNames = append(addedNames, s.tags[tagID].Name)
	}
	for tagID := range haveTagIDs {
		if _, want := wantTagIDs[tagID]; want {
			continue
		}
		removedNames = append(removedNames, s.tags[tagID].Name)
	}

	s.setAssignmentsFor(scope, newAssignments)

	if err := s.persist(); err != nil {
		// Roll back the catalogue and the join set wholesale.
		s.tags = prevTags
		s.nameIndex = prevNameIndex
		s.setAssignmentsFor(scope, prevAssignments)
		return SetResult{}, err
	}

	return SetResult{
		CreatedTags: created,
		Added:       addedNames,
		Removed:     removedNames,
	}, nil
}

func (s *Store) assignmentsFor(scope AssignmentScope) []*Assignment {
	switch scope {
	case ScopeRepo:
		return s.repoAssignments
	case ScopeSubscription:
		return s.subscriptionAssignments
	case ScopeAccount:
		return s.accountAssignments
	}
	return nil
}

func (s *Store) setAssignmentsFor(scope AssignmentScope, list []*Assignment) {
	switch scope {
	case ScopeRepo:
		s.repoAssignments = list
	case ScopeSubscription:
		s.subscriptionAssignments = list
	case ScopeAccount:
		s.accountAssignments = list
	}
}

// TagsFor returns the tag names directly assigned to one entity,
// sorted for deterministic output. Inheritance is *not* applied —
// callers that need the effective set use EffectiveSubscriptionTags.
func (s *Store) TagsFor(scope AssignmentScope, entityID string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tagsForLocked(scope, entityID)
}

func (s *Store) tagsForLocked(scope AssignmentScope, entityID string) []string {
	out := []string{}
	for _, a := range s.assignmentsFor(scope) {
		if a.EntityID != entityID {
			continue
		}
		if t, ok := s.tags[a.TagID]; ok {
			out = append(out, t.Name)
		}
	}
	sort.Strings(out)
	return out
}

// EffectiveSubscriptionTags returns the union of a subscription's
// own tags, its repo's tags, and its account's tags — the §2 model
// computed read-side. Names are case-folded for the union (so the
// repo's `Team:Marketing` and the account's `team:marketing`
// collapse) but the returned strings preserve the catalogue's
// canonical display casing.
func (s *Store) EffectiveSubscriptionTags(subID, repoName, accountKey string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	seen := make(map[string]string) // lower → display
	pull := func(scope AssignmentScope, id string) {
		if id == "" {
			return
		}
		for _, a := range s.assignmentsFor(scope) {
			if a.EntityID != id {
				continue
			}
			if t, ok := s.tags[a.TagID]; ok {
				lower := strings.ToLower(t.Name)
				if _, dup := seen[lower]; !dup {
					seen[lower] = t.Name
				}
			}
		}
	}
	pull(ScopeSubscription, subID)
	pull(ScopeRepo, repoName)
	pull(ScopeAccount, accountKey)

	out := make([]string, 0, len(seen))
	for _, display := range seen {
		out = append(out, display)
	}
	sort.Strings(out)
	return out
}

// EntitiesTagged returns the EntityIDs of all entities under scope
// that have at least one tag assigned. Used by callers that want to
// pre-populate a tag picker without iterating every entity.
func (s *Store) EntitiesTagged(scope AssignmentScope) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]struct{}{}
	for _, a := range s.assignmentsFor(scope) {
		seen[a.EntityID] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Usage returns counts keyed by tag ID. Missing tags from the
// catalogue are not included.
func (s *Store) Usage() map[string]UsageCounts {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]UsageCounts, len(s.tags))
	for id := range s.tags {
		out[id] = UsageCounts{}
	}
	for _, a := range s.repoAssignments {
		c := out[a.TagID]
		c.Repos++
		out[a.TagID] = c
	}
	for _, a := range s.subscriptionAssignments {
		c := out[a.TagID]
		c.Subscriptions++
		out[a.TagID] = c
	}
	for _, a := range s.accountAssignments {
		c := out[a.TagID]
		c.Accounts++
		out[a.TagID] = c
	}
	return out
}

package tags

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"

	"github.com/petervdpas/GiGot/internal/crypto"
)

func newTestStore(t *testing.T) (*Store, *crypto.Encryptor, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tags.enc")
	priv, _, err := crypto.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	enc, err := crypto.New(priv)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, enc)
	if err != nil {
		t.Fatal(err)
	}
	return s, enc, path
}

func TestCreate_Roundtrip(t *testing.T) {
	s, _, _ := newTestStore(t)
	got, err := s.Create("team:marketing", "peter")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Fatal("Create did not assign an ID")
	}
	if got.Name != "team:marketing" {
		t.Fatalf("Name = %q, want team:marketing", got.Name)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt not stamped on create")
	}
	if got.CreatedBy != "peter" {
		t.Fatalf("CreatedBy = %q, want peter", got.CreatedBy)
	}
}

func TestCreate_TrimsWhitespace(t *testing.T) {
	s, _, _ := newTestStore(t)
	got, err := s.Create("  team:platform  ", "peter")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "team:platform" {
		t.Fatalf("Name = %q, want trimmed team:platform", got.Name)
	}
}

func TestCreate_RejectsEmpty(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Create("", "peter"); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("got %v, want ErrNameRequired", err)
	}
	if _, err := s.Create("   ", "peter"); !errors.Is(err, ErrNameRequired) {
		t.Fatalf("got %v, want ErrNameRequired (whitespace-only)", err)
	}
}

func TestCreate_RejectsForbiddenChars(t *testing.T) {
	s, _, _ := newTestStore(t)
	for _, bad := range []string{"team/marketing", "team?marketing", "team#marketing", "team\nmarketing"} {
		if _, err := s.Create(bad, "peter"); !errors.Is(err, ErrNameInvalid) {
			t.Errorf("name %q: got %v, want ErrNameInvalid", bad, err)
		}
	}
}

func TestCreate_RejectsTooLong(t *testing.T) {
	s, _, _ := newTestStore(t)
	long := make([]byte, maxNameLen+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := s.Create(string(long), "peter"); !errors.Is(err, ErrNameInvalid) {
		t.Fatalf("got %v, want ErrNameInvalid for over-length name", err)
	}
}

func TestCreate_CaseInsensitiveDuplicate(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.Create("Team:Marketing", "peter"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create("team:marketing", "peter"); !errors.Is(err, ErrNameDuplicate) {
		t.Fatalf("got %v, want ErrNameDuplicate", err)
	}
}

func TestGetByName_CaseInsensitive(t *testing.T) {
	s, _, _ := newTestStore(t)
	created, _ := s.Create("Team:Marketing", "peter")
	got, err := s.GetByName("team:marketing")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != created.ID {
		t.Fatalf("GetByName returned different tag: got %q, want %q", got.ID, created.ID)
	}
	if got.Name != "Team:Marketing" {
		t.Fatalf("Name = %q, want first-creator casing 'Team:Marketing'", got.Name)
	}
}

func TestRename_HappyPath(t *testing.T) {
	s, _, _ := newTestStore(t)
	created, _ := s.Create("team:mktg", "peter")
	renamed, err := s.Rename(created.ID, "team:marketing")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "team:marketing" {
		t.Fatalf("Name after rename = %q, want team:marketing", renamed.Name)
	}
	// Old name is gone from the index.
	if _, err := s.GetByName("team:mktg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old name still resolves: got %v, want ErrNotFound", err)
	}
	// New name resolves.
	if _, err := s.GetByName("team:marketing"); err != nil {
		t.Fatalf("new name does not resolve: %v", err)
	}
}

func TestRename_CasingOnlyChange(t *testing.T) {
	s, _, _ := newTestStore(t)
	created, _ := s.Create("team:marketing", "peter")
	renamed, err := s.Rename(created.ID, "Team:Marketing")
	if err != nil {
		t.Fatalf("rename to different casing rejected: %v", err)
	}
	if renamed.Name != "Team:Marketing" {
		t.Fatalf("display casing not updated: got %q", renamed.Name)
	}
}

func TestRename_CollidesWithOther(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.Create("team:marketing", "peter")
	other, _ := s.Create("team:platform", "peter")
	if _, err := s.Rename(other.ID, "TEAM:marketing"); !errors.Is(err, ErrNameDuplicate) {
		t.Fatalf("got %v, want ErrNameDuplicate", err)
	}
}

func TestDelete_RemovesAndIndexClears(t *testing.T) {
	s, _, _ := newTestStore(t)
	created, _ := s.Create("contractor:acme", "peter")
	got, err := s.Delete(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Slice 1 has no assignments yet, so all sweep counts are zero —
	// but the response shape is what slice 2 will populate.
	if got.RepoAssignments != 0 || got.SubscriptionAssignments != 0 || got.AccountAssignments != 0 {
		t.Fatalf("unexpected sweep counts on empty store: %+v", got)
	}
	if _, err := s.Get(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("tag still gettable after delete: %v", err)
	}
	if _, err := s.GetByName("contractor:acme"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("name still resolves after delete: %v", err)
	}
}

func TestDelete_CascadesAssignments(t *testing.T) {
	s, _, _ := newTestStore(t)
	tag, _ := s.Create("team:marketing", "peter")
	// Inject a few assignments directly — slice 1 doesn't expose
	// public assign methods, so we poke the sets to verify the
	// cascade path before slice 2 lands.
	s.mu.Lock()
	s.repoAssignments = []*Assignment{{EntityID: "addresses", TagID: tag.ID}, {EntityID: "other", TagID: "different"}}
	s.subscriptionAssignments = []*Assignment{{EntityID: "sub-1", TagID: tag.ID}}
	s.accountAssignments = []*Assignment{{EntityID: "acc-1", TagID: tag.ID}, {EntityID: "acc-2", TagID: tag.ID}}
	if err := s.persist(); err != nil {
		s.mu.Unlock()
		t.Fatal(err)
	}
	s.mu.Unlock()

	got, err := s.Delete(tag.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RepoAssignments != 1 {
		t.Errorf("RepoAssignments swept = %d, want 1", got.RepoAssignments)
	}
	if got.SubscriptionAssignments != 1 {
		t.Errorf("SubscriptionAssignments swept = %d, want 1", got.SubscriptionAssignments)
	}
	if got.AccountAssignments != 2 {
		t.Errorf("AccountAssignments swept = %d, want 2", got.AccountAssignments)
	}
	// The unrelated assignment on a different tag must survive.
	if len(s.repoAssignments) != 1 || s.repoAssignments[0].TagID != "different" {
		t.Fatalf("cascade swept unrelated assignment: %+v", s.repoAssignments)
	}
}

func TestRoundtrip_AcrossOpen(t *testing.T) {
	s, enc, path := newTestStore(t)
	created, _ := s.Create("team:marketing", "peter")

	reopened, err := Open(path, enc)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := reopened.Get(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "team:marketing" {
		t.Fatalf("Name after reopen = %q", got.Name)
	}
}

func TestSetRepoTags_AssignsAndCreates(t *testing.T) {
	s, _, _ := newTestStore(t)
	res, err := s.SetRepoTags("addresses", []string{"team:marketing", "env:prod"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	// Both tags were unknown → both auto-created.
	if len(res.CreatedTags) != 2 {
		t.Fatalf("CreatedTags = %d, want 2", len(res.CreatedTags))
	}
	if len(res.Added) != 2 {
		t.Fatalf("Added = %v, want 2 entries", res.Added)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v, want empty", res.Removed)
	}
	got := s.TagsFor(ScopeRepo, "addresses")
	if len(got) != 2 {
		t.Fatalf("TagsFor = %v, want 2", got)
	}
}

func TestSetRepoTags_IdempotentNoOp(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.SetRepoTags("addresses", []string{"team:marketing"}, "alice"); err != nil {
		t.Fatal(err)
	}
	res, err := s.SetRepoTags("addresses", []string{"team:marketing"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 || len(res.Removed) != 0 || len(res.CreatedTags) != 0 {
		t.Fatalf("idempotent set should be a no-op, got %+v", res)
	}
}

func TestSetRepoTags_DiffsAddsAndRemoves(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.SetRepoTags("addresses", []string{"team:marketing", "env:prod"}, "alice"); err != nil {
		t.Fatal(err)
	}
	res, err := s.SetRepoTags("addresses", []string{"team:marketing", "env:staging"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(res.Added, "env:staging") {
		t.Errorf("Added = %v, want env:staging", res.Added)
	}
	if !containsString(res.Removed, "env:prod") {
		t.Errorf("Removed = %v, want env:prod", res.Removed)
	}
}

func TestSetRepoTags_RejectsInvalidName(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.SetRepoTags("addresses", []string{"valid", "with/slash"}, "alice"); !errors.Is(err, ErrNameInvalid) {
		t.Fatalf("got %v, want ErrNameInvalid", err)
	}
	// Validation must happen before mutation — the valid tag should
	// not have landed.
	if got := s.TagsFor(ScopeRepo, "addresses"); len(got) != 0 {
		t.Fatalf("partial application: %v", got)
	}
}

func TestSetRepoTags_DedupesCaseInsensitive(t *testing.T) {
	s, _, _ := newTestStore(t)
	res, err := s.SetRepoTags("addresses", []string{"Team:Marketing", "team:marketing"}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.CreatedTags) != 1 {
		t.Fatalf("CreatedTags = %d, want 1 (deduped)", len(res.CreatedTags))
	}
}

func TestSetSubscriptionTags_AndAccountTags(t *testing.T) {
	s, _, _ := newTestStore(t)
	if _, err := s.SetSubscriptionTags("token-xyz", []string{"project:redesign"}, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAccountTags("github:bob", []string{"contractor:acme"}, "alice"); err != nil {
		t.Fatal(err)
	}

	if got := s.TagsFor(ScopeSubscription, "token-xyz"); len(got) != 1 || got[0] != "project:redesign" {
		t.Fatalf("sub tags = %v", got)
	}
	if got := s.TagsFor(ScopeAccount, "github:bob"); len(got) != 1 || got[0] != "contractor:acme" {
		t.Fatalf("account tags = %v", got)
	}
}

func TestEffectiveSubscriptionTags_UnionsThreeSources(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.SetRepoTags("addresses", []string{"team:marketing", "env:prod"}, "alice")
	_, _ = s.SetAccountTags("github:bob", []string{"contractor:acme"}, "alice")
	_, _ = s.SetSubscriptionTags("token-xyz", []string{"project:redesign"}, "alice")

	got := s.EffectiveSubscriptionTags("token-xyz", "addresses", "github:bob")
	want := []string{"contractor:acme", "env:prod", "project:redesign", "team:marketing"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("got[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestEffectiveSubscriptionTags_SameTagFromTwoSources(t *testing.T) {
	s, _, _ := newTestStore(t)
	_, _ = s.SetRepoTags("addresses", []string{"team:marketing"}, "alice")
	_, _ = s.SetAccountTags("github:bob", []string{"team:marketing"}, "alice")

	got := s.EffectiveSubscriptionTags("token-x", "addresses", "github:bob")
	if len(got) != 1 || got[0] != "team:marketing" {
		t.Fatalf("got %v, want exactly [team:marketing]", got)
	}
}

func TestSetRepoTags_RoundtripAcrossOpen(t *testing.T) {
	s, enc, path := newTestStore(t)
	if _, err := s.SetRepoTags("addresses", []string{"team:marketing"}, "alice"); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, enc)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if got := reopened.TagsFor(ScopeRepo, "addresses"); len(got) != 1 || got[0] != "team:marketing" {
		t.Fatalf("after reopen got %v", got)
	}
}

func TestDelete_CascadesAcrossSetTags(t *testing.T) {
	// Slice 1's TestDelete_CascadesAssignments pokes the slices
	// directly to simulate slice 2; this scenario uses the public
	// SetRepoTags / SetSubscriptionTags / SetAccountTags surface,
	// then deletes the tag from the catalogue and confirms every
	// downstream entity has lost it.
	s, _, _ := newTestStore(t)
	_, _ = s.SetRepoTags("addresses", []string{"sweep-me"}, "alice")
	_, _ = s.SetSubscriptionTags("token-x", []string{"sweep-me"}, "alice")
	_, _ = s.SetAccountTags("github:bob", []string{"sweep-me"}, "alice")

	tag, err := s.GetByName("sweep-me")
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Delete(tag.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.RepoAssignments != 1 || res.SubscriptionAssignments != 1 || res.AccountAssignments != 1 {
		t.Fatalf("sweep counts = %+v, want 1/1/1", res)
	}
	if got := s.TagsFor(ScopeRepo, "addresses"); len(got) != 0 {
		t.Errorf("repo tag survived cascade: %v", got)
	}
	if got := s.TagsFor(ScopeSubscription, "token-x"); len(got) != 0 {
		t.Errorf("sub tag survived cascade: %v", got)
	}
	if got := s.TagsFor(ScopeAccount, "github:bob"); len(got) != 0 {
		t.Errorf("account tag survived cascade: %v", got)
	}
}

func containsString(haystack []string, want string) bool {
	return slices.Contains(haystack, want)
}

func TestUsage_ReportsZeroForFreshTag(t *testing.T) {
	s, _, _ := newTestStore(t)
	tag, _ := s.Create("env:prod", "peter")
	usage := s.Usage()
	c, ok := usage[tag.ID]
	if !ok {
		t.Fatal("Usage missing fresh tag")
	}
	if c.Repos != 0 || c.Subscriptions != 0 || c.Accounts != 0 {
		t.Fatalf("fresh tag has non-zero usage: %+v", c)
	}
}

// TestDeleteUnused_KeepsAssignedRemovesOrphans pins the contract:
// only catalogue rows that are not referenced anywhere get swept;
// every assigned tag stays. Returned slice carries the names of
// removed tags so the handler can emit per-row audit events.
func TestDeleteUnused_KeepsAssignedRemovesOrphans(t *testing.T) {
	s, _, _ := newTestStore(t)
	keep, _ := s.Create("team:keep", "peter")
	orphan1, _ := s.Create("team:orphan-a", "peter")
	orphan2, _ := s.Create("team:orphan-b", "peter")
	if _, err := s.SetRepoTags("addresses", []string{keep.Name}, "peter"); err != nil {
		t.Fatal(err)
	}

	removed, err := s.DeleteUnused()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d tags, want 2", len(removed))
	}
	names := []string{removed[0].Name, removed[1].Name}
	for _, want := range []string{orphan1.Name, orphan2.Name} {
		if !slices.Contains(names, want) {
			t.Errorf("expected %q in removed, got %v", want, names)
		}
	}
	if _, err := s.Get(keep.ID); err != nil {
		t.Errorf("kept tag (still assigned) was swept: %v", err)
	}
	if _, err := s.Get(orphan1.ID); err == nil {
		t.Errorf("orphan tag still in catalogue")
	}
}

// TestDeleteUnused_EmptyWhenNothingOrphan returns nil + nil error
// when every tag is referenced — handler can early-return without a
// special "nothing to do" branch.
func TestDeleteUnused_EmptyWhenNothingOrphan(t *testing.T) {
	s, _, _ := newTestStore(t)
	t1, _ := s.Create("a", "peter")
	if _, err := s.SetRepoTags("addresses", []string{t1.Name}, "peter"); err != nil {
		t.Fatal(err)
	}
	removed, err := s.DeleteUnused()
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 0 {
		t.Fatalf("expected zero removals, got %d", len(removed))
	}
}

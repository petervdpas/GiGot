package tags

import (
	"errors"
	"path/filepath"
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

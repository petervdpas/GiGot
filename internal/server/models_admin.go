package server

// AdminLoginRequest is the body for /admin/login.
type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AdminLoginResponse confirms a successful admin login (or reports the
// current session). Username is the account identifier; Provider names
// the account provider (local, microsoft, github, ...) so the UI can
// disambiguate identical identifiers across providers. DisplayName
// and Role are enrichments used by the admin UI.
type AdminLoginResponse struct {
	Username    string `json:"username"`
	Provider    string `json:"provider,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role,omitempty"`
}

// TokenListItem describes one issued subscription key in a listing.
// Repo is the single repository this key grants access to — keys
// are one-repo-per-key by design. HasAccount reports whether the
// stored Username resolves to an account in the store. False for
// tokens issued before the accounts model shipped — the
// subscriptions UI surfaces a "Bind to account" action on those
// rows. See accounts.md §6.
type TokenListItem struct {
	Token      string   `json:"token"`
	Username   string   `json:"username"`
	Repo       string   `json:"repo"`
	Abilities  []string `json:"abilities,omitempty"`
	HasAccount bool     `json:"has_account"`
	// Tags is the subscription's *direct* tag list (assignments
	// stored on the sub itself, not inherited).
	Tags []string `json:"tags,omitempty"`
	// EffectiveTags unions Tags with the subscription's repo's tags
	// and the subscription's account's tags, computed at read time
	// (design §2). The subscription detail UI renders these alongside
	// the explicit ones with `↩ from repo` / `👤 from account`
	// source labels (design §5.4).
	EffectiveTags []string `json:"effective_tags,omitempty"`
}

// TokenListResponse is the body of GET /api/admin/tokens.
type TokenListResponse struct {
	Tokens []TokenListItem `json:"tokens"`
	Count  int             `json:"count"`
}

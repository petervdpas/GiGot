package server

// AdminLoginRequest is the body for /admin/login.
type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// AdminLoginResponse confirms a successful admin login (or reports the
// current session). Username is the account identifier; DisplayName
// and Role are enrichments used by the admin UI.
type AdminLoginResponse struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role,omitempty"`
}

// TokenListItem describes one issued token in an admin listing.
type TokenListItem struct {
	Token     string   `json:"token"`
	Username  string   `json:"username"`
	Repos     []string `json:"repos"`
	Abilities []string `json:"abilities,omitempty"`
}

// TokenListResponse is the body of GET /api/admin/tokens.
type TokenListResponse struct {
	Tokens []TokenListItem `json:"tokens"`
	Count  int             `json:"count"`
}

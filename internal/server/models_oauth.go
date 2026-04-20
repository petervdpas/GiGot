package server

// OAuthProviderView is one enabled provider, as seen by the login
// page. LoginURL is the path the button's anchor points at —
// typically /admin/login/<name>, but kept explicit so the handler
// is free to relocate later without the front-end having to care.
type OAuthProviderView struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	LoginURL    string `json:"login_url"`
}

// OAuthProvidersResponse is the body of GET /api/admin/providers.
// Always has a "providers" array (possibly empty) so the login page
// can render an "only local allowed" branch cleanly.
type OAuthProvidersResponse struct {
	Providers []OAuthProviderView `json:"providers"`
}

package oauth

// RegistryForTest builds a *Registry whose provider map is exactly
// the one supplied. Intended for unit tests that want to drive
// Registry.Get / .Providers without running live OIDC discovery.
// Exported so tests in other packages (e.g. internal/server) can
// call it without duplicating the struct.
func RegistryForTest(providers map[string]Provider) *Registry {
	r := &Registry{providers: make(map[string]Provider, len(providers))}
	for k, v := range providers {
		r.providers[k] = v
	}
	return r
}

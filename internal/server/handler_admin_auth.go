package server

import (
	"encoding/json"
	"net/http"

	"github.com/petervdpas/GiGot/internal/config"
)

// AuthRuntimeView is the read projection of cfg.Auth that the
// /admin/auth page consumes. Intentionally scoped: client_secret_ref
// and secret_ref values themselves are fine to expose (they're names,
// not secrets — the vault holds the actual bytes), but nothing else
// from the vault leaks. See docs/design/accounts.md §8, §9.
type AuthRuntimeView struct {
	AllowLocal bool                      `json:"allow_local"                                 example:"true"`
	OAuth      OAuthRuntimeView          `json:"oauth"`
	Gateway    GatewayRuntimeView        `json:"gateway"`
	ConfigPath string                    `json:"config_path,omitempty"                       example:"/etc/gigot/gigot.json"`
}

// OAuthRuntimeView mirrors config.OAuthConfig sans per-provider
// secret bytes. All three known providers show up so the UI can
// render their fields even when disabled.
type OAuthRuntimeView struct {
	GitHub    config.OAuthProviderConfig `json:"github"`
	Entra     config.OAuthProviderConfig `json:"entra"`
	Microsoft config.OAuthProviderConfig `json:"microsoft"`
}

// GatewayRuntimeView mirrors config.GatewayConfig directly.
// secret_ref is a lookup name, not the secret — safe to ship.
type GatewayRuntimeView = config.GatewayConfig

// AuthReloadRequest is the body of PATCH /api/admin/auth. The whole
// AuthConfig goes over the wire on every edit — the UI re-POSTs the
// form's full state, server validates + applies atomically. Partial
// merges are intentionally not supported: reasoning about "did the
// caller mean allow_local=false or did they just forget the field?"
// is a well-known fallacy trap for JSON PATCH.
type AuthReloadRequest struct {
	AllowLocal bool                  `json:"allow_local"`
	OAuth      config.OAuthConfig    `json:"oauth"`
	Gateway    config.GatewayConfig  `json:"gateway"`
}

// handleAdminAuth godoc
// @Summary      Read or reload auth runtime state (admin only)
// @Description  GET returns the current allow_local + OAuth + gateway
// @Description  config snapshot. PATCH applies a new snapshot: the
// @Description  OAuth registry and gateway strategy are rebuilt,
// @Description  atomically swapped into place on success, and the
// @Description  change is persisted to the config file. Rejects with
// @Description  400 when a secret ref fails to resolve or a provider
// @Description  discovery URL is unreachable — old state stays live.
// @Tags         admin
// @Accept       json
// @Produce      json
// @Param        body  body      AuthReloadRequest   false  "New auth config (PATCH)"
// @Success      200   {object}  AuthRuntimeView     "Current state (GET + PATCH)"
// @Failure      400   {object}  ErrorResponse
// @Failure      401   {object}  ErrorResponse
// @Failure      405   {object}  ErrorResponse
// @Router       /admin/auth [get]
// @Router       /admin/auth [patch]
func (s *Server) handleAdminAuth(w http.ResponseWriter, r *http.Request) {
	if s.requireAdminSession(w, r) == nil {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.currentAuthView())
	case http.MethodPatch:
		var req AuthReloadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		newCfg := config.AuthConfig{
			// Enabled / Type stay as-is — they're boot-time concerns
			// (wire-level auth toggling, strategy-type selector) and
			// flipping them at runtime would break in-flight sessions.
			Enabled:    s.cfg.Auth.Enabled,
			Type:       s.cfg.Auth.Type,
			AllowLocal: req.AllowLocal,
			OAuth:      req.OAuth,
			Gateway:    req.Gateway,
		}
		if err := s.ReloadAuth(newCfg); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.currentAuthView())
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// currentAuthView takes a snapshot under the read lock so the
// response is internally consistent (allow_local + registry contents
// + gateway state all from the same revision).
func (s *Server) currentAuthView() AuthRuntimeView {
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	return AuthRuntimeView{
		AllowLocal: s.cfg.Auth.AllowLocal,
		OAuth: OAuthRuntimeView{
			GitHub:    s.cfg.Auth.OAuth.GitHub,
			Entra:     s.cfg.Auth.OAuth.Entra,
			Microsoft: s.cfg.Auth.OAuth.Microsoft,
		},
		Gateway:    s.cfg.Auth.Gateway,
		ConfigPath: s.cfg.Path,
	}
}

package server

import "net/http"

// handleServerPubKey godoc
// @Summary      Fetch the server's NaCl public key
// @Description  Returns the base64-encoded curve25519 public key that clients
// @Description  use to seal request bodies.
// @Tags        crypto
// @Produce      json
// @Success      200  {object}  ServerPubKeyResponse
// @Router       /crypto/pubkey [get]
func (s *Server) handleServerPubKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, ServerPubKeyResponse{
		PublicKey: s.encryptor.PublicKey().Encode(),
	})
}

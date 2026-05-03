package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/petervdpas/GiGot/internal/clients"
)

// handleEnroll godoc
// @Summary      Enroll a client's public key
// @Description  Registers a Formidable client so the server can seal responses
// @Description  to it. Returns the server's public key for outgoing requests.
// @Tags        crypto
// @Accept       json
// @Produce      json
// @Param        body  body      EnrollRequest  true  "Enrollment request"
// @Success      201   {object}  EnrollResponse
// @Failure      400   {object}  ErrorResponse
// @Failure      409   {object}  ErrorResponse
// @Router       /clients/enroll [post]
func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var req EnrollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ClientID == "" {
		writeError(w, http.StatusBadRequest, "client_id is required")
		return
	}
	if req.PublicKey == "" {
		writeError(w, http.StatusBadRequest, "public_key is required")
		return
	}

	_, err := s.clients.Enroll(req.ClientID, req.PublicKey)
	if err != nil {
		if errors.Is(err, clients.ErrExists) {
			writeError(w, http.StatusConflict, "client already enrolled with a different key")
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, EnrollResponse{
		ClientID:        req.ClientID,
		ServerPublicKey: s.encryptor.PublicKey().Encode(),
	})
}

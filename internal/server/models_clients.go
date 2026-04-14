package server

// EnrollRequest registers a client's NaCl public key with the server.
type EnrollRequest struct {
	ClientID  string `json:"client_id"  example:"laptop-01"`
	PublicKey string `json:"public_key" example:"base64-encoded-32-byte-key"`
}

// EnrollResponse confirms enrollment and hands back the server's public key
// so the client can seal subsequent requests without a second round trip.
type EnrollResponse struct {
	ClientID        string `json:"client_id"`
	ServerPublicKey string `json:"server_public_key"`
}

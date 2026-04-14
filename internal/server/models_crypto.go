package server

// ServerPubKeyResponse exposes the server's NaCl public key. Clients use this
// to seal request bodies before sending them to the server.
type ServerPubKeyResponse struct {
	PublicKey string `json:"public_key" example:"base64-encoded-32-byte-key"`
}

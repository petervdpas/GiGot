package server

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/petervdpas/GiGot/internal/clients"
)

// SealedContentType marks request/response bodies that are NaCl-sealed and
// base64-encoded. Headers such as X-Client-Id stay plain so gateways can still
// do subscription-key validation, rate-limiting, and routing.
const SealedContentType = "application/vnd.gigot.sealed+b64"

// HeaderClientID identifies which enrolled client sealed the request. The
// server looks up the client's public key to open the body and to seal the
// response.
const HeaderClientID = "X-Client-Id"

// sealedMiddleware transparently unseals incoming request bodies and seals
// outgoing response bodies when the request is marked as sealed
// (Content-Type + X-Client-Id). Otherwise it passes through unchanged.
func (s *Server) sealedMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only /api/* ever gets sealed. Index, swagger, /git/* pass through.
		if !strings.HasPrefix(r.URL.Path, "/api/") || !isSealedRequest(r) {
			next.ServeHTTP(w, r)
			return
		}

		clientID := r.Header.Get(HeaderClientID)
		clientPub, err := s.clients.PublicKey(clientID)
		if err != nil {
			if errors.Is(err, clients.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "client not enrolled")
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}

		// Unseal the request body, if any.
		if r.Body != nil && r.ContentLength != 0 {
			raw, err := io.ReadAll(r.Body)
			if err != nil {
				writeError(w, http.StatusBadRequest, "read body: "+err.Error())
				return
			}
			_ = r.Body.Close()

			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) > 0 {
				sealed, err := base64.StdEncoding.DecodeString(string(trimmed))
				if err != nil {
					writeError(w, http.StatusBadRequest, "body is not valid base64")
					return
				}
				plain, err := s.encryptor.Open(clientPub, sealed)
				if err != nil {
					writeError(w, http.StatusBadRequest, "decrypt body: "+err.Error())
					return
				}
				r.Body = io.NopCloser(bytes.NewReader(plain))
				r.ContentLength = int64(len(plain))
				r.Header.Set("Content-Type", "application/json")
			}
		}

		rw := &sealingResponseWriter{
			ResponseWriter: w,
			encryptor:      s.encryptorForClient(clientPub),
			buf:            &bytes.Buffer{},
		}
		next.ServeHTTP(rw, r)
		rw.flush()
	})
}

func isSealedRequest(r *http.Request) bool {
	if r.Header.Get(HeaderClientID) == "" {
		return false
	}
	ct := r.Header.Get("Content-Type")
	// Strip parameters like charset=.
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(ct)
	return ct == SealedContentType
}

// sealingResponseWriter buffers the handler's response and seals it to the
// client's public key on flush. Status code is written through unchanged.
type sealingResponseWriter struct {
	http.ResponseWriter
	encryptor sealerFunc
	buf       *bytes.Buffer

	statusCode    int
	wroteHeader   bool
	skipSealing   bool // true when handler set a non-JSON content-type we shouldn't rewrap
	headerWritten bool
}

// sealerFunc seals plaintext for a specific client.
type sealerFunc func(plaintext []byte) (string, error)

func (s *Server) encryptorForClient(clientPub [32]byte) sealerFunc {
	return func(plaintext []byte) (string, error) {
		return s.encryptor.SealString(clientPub, plaintext)
	}
}

func (w *sealingResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.wroteHeader = true
	// Defer writing headers until flush so we can set Content-Type/Length.
}

func (w *sealingResponseWriter) Write(p []byte) (int, error) {
	if !w.wroteHeader {
		w.statusCode = http.StatusOK
		w.wroteHeader = true
	}
	return w.buf.Write(p)
}

// Header lets handlers inspect pre-flush headers (e.g. to set Content-Type).
// We intercept Content-Type at flush time anyway.

func (w *sealingResponseWriter) flush() {
	// Seal whatever the handler produced.
	plain := w.buf.Bytes()
	if len(plain) == 0 {
		if w.wroteHeader {
			w.ResponseWriter.WriteHeader(w.statusCode)
		}
		return
	}
	sealed, err := w.encryptor(plain)
	if err != nil {
		// Can't seal — abandon and return 500 with plaintext error. The client
		// will at least get a status code it can react to.
		w.ResponseWriter.Header().Set("Content-Type", "application/json")
		w.ResponseWriter.WriteHeader(http.StatusInternalServerError)
		_, _ = w.ResponseWriter.Write([]byte(`{"error":"seal response: ` + err.Error() + `"}`))
		return
	}

	w.ResponseWriter.Header().Set("Content-Type", SealedContentType)
	w.ResponseWriter.Header().Del("Content-Length")
	status := w.statusCode
	if status == 0 {
		status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(status)
	_, _ = w.ResponseWriter.Write([]byte(sealed))
}

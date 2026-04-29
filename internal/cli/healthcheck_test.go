package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/petervdpas/GiGot/internal/config"
)

// TestProbeHealth covers the loopback probe end-to-end against an
// httptest server: the success path, the non-2xx path, the wildcard
// host substitution, and the unreachable-port path.
func TestProbeHealth(t *testing.T) {
	t.Run("2xx response succeeds", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		host, port := splitHostPort(t, srv.Listener.Addr().String())
		cfg := newCfg(host, port)
		if err := probeHealth(cfg, http.DefaultTransport); err != nil {
			t.Fatalf("probeHealth: %v", err)
		}
	})

	t.Run("5xx response fails with HTTP code in error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		t.Cleanup(srv.Close)
		host, port := splitHostPort(t, srv.Listener.Addr().String())
		cfg := newCfg(host, port)
		err := probeHealth(cfg, http.DefaultTransport)
		if err == nil {
			t.Fatalf("probeHealth: want error on HTTP 500, got nil")
		}
		if !strings.Contains(err.Error(), "HTTP 500") {
			t.Errorf("probeHealth error: want substring %q, got %q", "HTTP 500", err.Error())
		}
	})

	t.Run("0.0.0.0 host is rewritten to 127.0.0.1", func(t *testing.T) {
		// httptest binds to 127.0.0.1 by default; if the rewrite works we
		// reach it even though the config says 0.0.0.0.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		_, port := splitHostPort(t, srv.Listener.Addr().String())
		cfg := newCfg("0.0.0.0", port)
		if err := probeHealth(cfg, http.DefaultTransport); err != nil {
			t.Fatalf("probeHealth: 0.0.0.0 should resolve to loopback, got %v", err)
		}
	})

	t.Run("connection refused fails", func(t *testing.T) {
		// Take a listener, immediately close it — the port is now free
		// but the dial will fail. Avoids hard-coding a port number.
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		port := ln.Addr().(*net.TCPAddr).Port
		ln.Close()
		cfg := newCfg("127.0.0.1", port)
		if err := probeHealth(cfg, http.DefaultTransport); err == nil {
			t.Fatalf("probeHealth: want connection refused, got nil")
		}
	})
}

func newCfg(host string, port int) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Host: host, Port: port},
	}
}

func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("Atoi(%q): %v", portStr, err)
	}
	return host, port
}

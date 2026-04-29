package cli

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/petervdpas/GiGot/internal/config"
)

// healthcheckTimeout caps the probe at a short interval so the
// Dockerfile HEALTHCHECK doesn't sit longer than its --timeout.
const healthcheckTimeout = 2 * time.Second

// runHealthcheck issues a single GET against the loopback view of the
// configured server and returns nil iff the response is 2xx. Any other
// outcome (connection refused, timeout, non-2xx status) is surfaced as
// an error so the caller can exit non-zero.
//
// The probe always dials a loopback address. Operators routinely set
// server.host to 0.0.0.0 / :: so the listener accepts traffic from
// outside the container; those wildcards are not valid connect targets,
// so we map them to 127.0.0.1. A non-wildcard host is honoured verbatim
// in case someone bound to a specific interface.
func runHealthcheck(cfg *config.Config) error {
	return probeHealth(cfg, http.DefaultTransport)
}

func probeHealth(cfg *config.Config, transport http.RoundTripper) error {
	host := cfg.Server.Host
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "127.0.0.1"
	}
	url := "http://" + net.JoinHostPort(host, strconv.Itoa(cfg.Server.Port)) + "/"

	ctx, cancel := context.WithTimeout(context.Background(), healthcheckTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("healthcheck: build request: %w", err)
	}

	client := &http.Client{Transport: transport, Timeout: healthcheckTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("healthcheck: probe %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("healthcheck: probe %s: HTTP %d", url, resp.StatusCode)
	}
	return nil
}

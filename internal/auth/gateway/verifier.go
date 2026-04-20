// Package gateway is the Phase-4 signed-header identity strategy. A
// trusted fronting proxy authenticates the user and forwards three
// GiGot-scoped headers; the Verifier validates the HMAC-SHA256
// signature and the timestamp skew and returns the claimed
// identifier. Account resolution is left to the caller (server
// layer), mirroring how the oauth package separates
// claim-extraction from account-store mutation. See
// docs/design/accounts.md §9.
package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Errors returned by Verify. None of these reach the user verbatim —
// the server-level bridge maps every failure to ErrNoCredentials so
// the gateway can't be used as a user-enumeration oracle.
var (
	ErrHeaderMissing     = errors.New("gateway: required header missing")
	ErrTimestampMalformed = errors.New("gateway: timestamp is not a unix integer")
	ErrTimestampStale    = errors.New("gateway: timestamp outside skew window")
	ErrSignatureMalformed = errors.New("gateway: signature is not valid hex")
	ErrSignatureMismatch = errors.New("gateway: HMAC does not match")
)

// Verifier validates the three gateway headers against a shared HMAC
// secret. Construct once at boot; the zero value is unusable (no
// secret). Safe for concurrent use.
type Verifier struct {
	secret          []byte
	userHeader      string
	sigHeader       string
	timestampHeader string
	maxSkew         time.Duration
	// now is injected so tests can pin wall-clock for skew assertions.
	// Nil means time.Now.
	now func() time.Time
}

// Options is the Verifier constructor input. Everything is required
// except Now (defaults to time.Now).
type Options struct {
	Secret          []byte
	UserHeader      string
	SigHeader       string
	TimestampHeader string
	MaxSkew         time.Duration
	Now             func() time.Time
}

// NewVerifier constructs a Verifier, rejecting empty secrets /
// header names / non-positive skew so operators don't end up with a
// silently-disabled strategy.
func NewVerifier(opts Options) (*Verifier, error) {
	if len(opts.Secret) == 0 {
		return nil, errors.New("gateway: secret is required")
	}
	if strings.TrimSpace(opts.UserHeader) == "" {
		return nil, errors.New("gateway: user_header is required")
	}
	if strings.TrimSpace(opts.SigHeader) == "" {
		return nil, errors.New("gateway: sig_header is required")
	}
	if strings.TrimSpace(opts.TimestampHeader) == "" {
		return nil, errors.New("gateway: timestamp_header is required")
	}
	if opts.MaxSkew <= 0 {
		return nil, errors.New("gateway: max_skew must be > 0")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Verifier{
		secret:          append([]byte(nil), opts.Secret...),
		userHeader:      opts.UserHeader,
		sigHeader:       opts.SigHeader,
		timestampHeader: opts.TimestampHeader,
		maxSkew:         opts.MaxSkew,
		now:             now,
	}, nil
}

// Claim is what a successful verification yields — just the
// identifier the proxy asserted. DisplayName is always empty because
// the gateway contract forwards one claim, not a profile; the
// accounts store carries display names over the long term.
type Claim struct {
	Identifier string
}

// Verify reads the three headers off r. Returns ErrHeaderMissing when
// none of the headers are present (the caller should surface this as
// "no credentials, try the next strategy"), and the specific
// validation error otherwise so server-side logs can distinguish
// "user forgot to configure the proxy" from "someone is actively
// forging requests."
func (v *Verifier) Verify(r *http.Request) (*Claim, error) {
	// Normalise early: the identifier is keyed on (provider, lowercase
	// identifier) in the accounts store, and a case-sensitive HMAC
	// would make "Alice" and "alice" two different principals even
	// though the accounts layer treats them as one. Proxy-side Sign
	// applies the same normalisation so the two sides match.
	user := strings.ToLower(strings.TrimSpace(r.Header.Get(v.userHeader)))
	sig := strings.TrimSpace(r.Header.Get(v.sigHeader))
	ts := strings.TrimSpace(r.Header.Get(v.timestampHeader))

	// If NONE of the headers are present, this request simply isn't
	// gateway-authed — fall through to the next strategy. If SOME are
	// present but not all, that's a misconfigured proxy — hard fail.
	if user == "" && sig == "" && ts == "" {
		return nil, ErrHeaderMissing
	}
	if user == "" || sig == "" || ts == "" {
		return nil, fmt.Errorf("%w: need %q, %q, %q",
			ErrHeaderMissing, v.userHeader, v.sigHeader, v.timestampHeader)
	}

	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, ErrTimestampMalformed
	}
	claimed := time.Unix(tsInt, 0)
	delta := v.now().Sub(claimed)
	if delta < 0 {
		delta = -delta
	}
	if delta > v.maxSkew {
		return nil, fmt.Errorf("%w: |delta|=%s > %s", ErrTimestampStale, delta, v.maxSkew)
	}

	want, err := hex.DecodeString(sig)
	if err != nil {
		return nil, ErrSignatureMalformed
	}

	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(user))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(ts))
	got := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return nil, ErrSignatureMismatch
	}

	return &Claim{Identifier: user}, nil
}

// Sign is the proxy-side counterpart. Exposed so the test suite and
// any Go-written proxy can mint valid headers without reimplementing
// the formula. Returns (hex-signature, unix-timestamp-string).
func Sign(secret []byte, user string, t time.Time) (sig, ts string) {
	ts = strconv.FormatInt(t.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(strings.ToLower(strings.TrimSpace(user))))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(ts))
	sig = hex.EncodeToString(mac.Sum(nil))
	return sig, ts
}

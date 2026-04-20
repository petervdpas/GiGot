package gateway

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testSecret = "so-very-secret"
	testUser   = "alice@contoso.com"
)

func mustVerifier(t *testing.T, now func() time.Time) *Verifier {
	t.Helper()
	v, err := NewVerifier(Options{
		Secret:          []byte(testSecret),
		UserHeader:      "X-GiGot-Gateway-User",
		SigHeader:       "X-GiGot-Gateway-Sig",
		TimestampHeader: "X-GiGot-Gateway-Ts",
		MaxSkew:         5 * time.Minute,
		Now:             now,
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	return v
}

func TestNewVerifier_RejectsBadInput(t *testing.T) {
	base := Options{
		Secret:          []byte("s"),
		UserHeader:      "U",
		SigHeader:       "S",
		TimestampHeader: "T",
		MaxSkew:         time.Minute,
	}
	cases := []struct {
		name string
		mut  func(o *Options)
	}{
		{"empty secret", func(o *Options) { o.Secret = nil }},
		{"blank user header", func(o *Options) { o.UserHeader = "  " }},
		{"blank sig header", func(o *Options) { o.SigHeader = "" }},
		{"blank ts header", func(o *Options) { o.TimestampHeader = "" }},
		{"zero skew", func(o *Options) { o.MaxSkew = 0 }},
		{"negative skew", func(o *Options) { o.MaxSkew = -time.Second }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			opts := base
			c.mut(&opts)
			if _, err := NewVerifier(opts); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}

func TestVerify_HappyPath(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v := mustVerifier(t, func() time.Time { return now })
	sig, ts := Sign([]byte(testSecret), testUser, now)

	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", testUser)
	r.Header.Set("X-GiGot-Gateway-Sig", sig)
	r.Header.Set("X-GiGot-Gateway-Ts", ts)

	claim, err := v.Verify(r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claim.Identifier != strings.ToLower(testUser) {
		t.Errorf("Identifier = %q, want %q", claim.Identifier, strings.ToLower(testUser))
	}
}

func TestVerify_MissingAllHeadersReturnsHeaderMissing(t *testing.T) {
	v := mustVerifier(t, nil)
	r := httptest.NewRequest("GET", "/api/health", nil)
	_, err := v.Verify(r)
	if !errors.Is(err, ErrHeaderMissing) {
		t.Fatalf("err = %v, want ErrHeaderMissing", err)
	}
}

func TestVerify_PartialHeadersAreRejected(t *testing.T) {
	v := mustVerifier(t, nil)
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", testUser)
	// Sig + ts missing — this is a misconfigured proxy, not a
	// non-gateway request.
	_, err := v.Verify(r)
	if !errors.Is(err, ErrHeaderMissing) {
		t.Fatalf("err = %v, want ErrHeaderMissing", err)
	}
}

func TestVerify_StaleTimestamp(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v := mustVerifier(t, func() time.Time { return now })
	// Sign a timestamp 10 minutes in the past — MaxSkew is 5m.
	old := now.Add(-10 * time.Minute)
	sig, ts := Sign([]byte(testSecret), testUser, old)

	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", testUser)
	r.Header.Set("X-GiGot-Gateway-Sig", sig)
	r.Header.Set("X-GiGot-Gateway-Ts", ts)

	_, err := v.Verify(r)
	if !errors.Is(err, ErrTimestampStale) {
		t.Fatalf("err = %v, want ErrTimestampStale", err)
	}
}

func TestVerify_FutureTimestampOutsideSkewRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v := mustVerifier(t, func() time.Time { return now })
	future := now.Add(10 * time.Minute)
	sig, ts := Sign([]byte(testSecret), testUser, future)

	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", testUser)
	r.Header.Set("X-GiGot-Gateway-Sig", sig)
	r.Header.Set("X-GiGot-Gateway-Ts", ts)

	_, err := v.Verify(r)
	if !errors.Is(err, ErrTimestampStale) {
		t.Fatalf("err = %v, want ErrTimestampStale", err)
	}
}

func TestVerify_MalformedTimestamp(t *testing.T) {
	v := mustVerifier(t, nil)
	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", testUser)
	r.Header.Set("X-GiGot-Gateway-Sig", "aa")
	r.Header.Set("X-GiGot-Gateway-Ts", "not-an-int")
	_, err := v.Verify(r)
	if !errors.Is(err, ErrTimestampMalformed) {
		t.Fatalf("err = %v, want ErrTimestampMalformed", err)
	}
}

func TestVerify_MalformedSignature(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v := mustVerifier(t, func() time.Time { return now })
	_, ts := Sign([]byte(testSecret), testUser, now)

	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", testUser)
	r.Header.Set("X-GiGot-Gateway-Sig", "not-hex-ZZZZ")
	r.Header.Set("X-GiGot-Gateway-Ts", ts)
	_, err := v.Verify(r)
	if !errors.Is(err, ErrSignatureMalformed) {
		t.Fatalf("err = %v, want ErrSignatureMalformed", err)
	}
}

func TestVerify_SignatureMismatch(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v := mustVerifier(t, func() time.Time { return now })
	sig, ts := Sign([]byte("different-secret"), testUser, now)

	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", testUser)
	r.Header.Set("X-GiGot-Gateway-Sig", sig)
	r.Header.Set("X-GiGot-Gateway-Ts", ts)
	_, err := v.Verify(r)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("err = %v, want ErrSignatureMismatch", err)
	}
}

func TestVerify_TamperedUserDetected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v := mustVerifier(t, func() time.Time { return now })
	sig, ts := Sign([]byte(testSecret), testUser, now)

	r := httptest.NewRequest("GET", "/api/health", nil)
	// Someone swaps the user header but keeps the original signature.
	r.Header.Set("X-GiGot-Gateway-User", "mallory")
	r.Header.Set("X-GiGot-Gateway-Sig", sig)
	r.Header.Set("X-GiGot-Gateway-Ts", ts)
	_, err := v.Verify(r)
	if !errors.Is(err, ErrSignatureMismatch) {
		t.Fatalf("err = %v, want ErrSignatureMismatch (swapped user must invalidate HMAC)", err)
	}
}

func TestVerify_CaseInsensitiveUserClaim(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	v := mustVerifier(t, func() time.Time { return now })
	// Proxy signs over "Alice" (mixed case).
	sig, ts := Sign([]byte(testSecret), "Alice", now)

	r := httptest.NewRequest("GET", "/api/health", nil)
	r.Header.Set("X-GiGot-Gateway-User", "Alice")
	r.Header.Set("X-GiGot-Gateway-Sig", sig)
	r.Header.Set("X-GiGot-Gateway-Ts", ts)
	claim, err := v.Verify(r)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claim.Identifier != "alice" {
		t.Errorf("Identifier = %q, want lowercased %q", claim.Identifier, "alice")
	}
}

// Package delivery — signer_test.go covers the canonical rules of
// the asset-delivery URL signer:
//
//   - build → verify roundtrip succeeds for the right triad
//     (asset_id, workspace_id, exp),
//   - modified asset_id / workspace_id / exp / sig fails Verify,
//   - expired URLs return ErrExpired,
//   - short secrets (<32 bytes) are rejected at construction time,
//   - constant-time signature comparison prevents timing leaks
//     (covered structurally; we don't time it in the test).
//
// What is NOT covered:
//   - signature replay across different secrets (covered by
//     pkg/hmacsign's own roundtrip tests; this package reuses
//     the same canonicalisation rules).
package delivery

import (
	"context"
	"strings"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef" // exactly 32 bytes

func newTestSigner(t *testing.T) *Signer {
	t.Helper()
	s, err := NewSigner([]byte(testSecret), 5*time.Minute,
		"https://app.example.com", "/api/internal/v1/deliver")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

func TestNewSigner_RejectsShortSecret(t *testing.T) {
	cases := []struct {
		name   string
		secret []byte
	}{
		{"empty", nil},
		{"16_bytes", make([]byte, 16)},
		{"31_bytes", make([]byte, 31)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSigner(tc.secret, 0, "", ""); err == nil || err != ErrSecretTooShort {
				t.Fatalf("got %v, want ErrSecretTooShort", err)
			}
		})
	}
}

func TestNewSigner_AppliesDefaultTTL(t *testing.T) {
	s, err := NewSigner([]byte(testSecret), 0, "", "")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if s.ttl != 5*time.Minute {
		t.Errorf("ttl = %v, want 5m default", s.ttl)
	}
}

func TestNewSigner_NormalisesBaseURLAndPath(t *testing.T) {
	s, err := NewSigner([]byte(testSecret), time.Minute,
		"https://app.example.com/", "api/internal/v1/deliver/")
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	if s.baseURL != "https://app.example.com" {
		t.Errorf("baseURL = %q", s.baseURL)
	}
	if !strings.HasPrefix(s.path, "/") {
		t.Errorf("path = %q, must start with /", s.path)
	}
}

func TestBuildAndVerify_Roundtrip(t *testing.T) {
	s := newTestSigner(t)
	ctx := context.Background()
	w := WorkspaceContext{WorkspaceID: "ws-42"}
	url, err := s.BuildAuthorizedURL(ctx, w, "asset-xyz")
	if err != nil {
		t.Fatalf("BuildAuthorizedURL: %v", err)
	}
	if !strings.HasPrefix(url, "https://app.example.com/api/internal/v1/deliver/asset-xyz?") {
		t.Fatalf("url = %q, unexpected shape", url)
	}

	got := parseURL(t, url)
	if got.aid != "asset-xyz" || got.wid != "ws-42" || got.sig == "" {
		t.Errorf("bad params: aid=%q wid=%q sig=%q", got.aid, got.wid, got.sig)
	}
	if err := s.Verify(got.aid, got.wid, got.exp, got.sig); err != nil {
		t.Errorf("Verify roundtrip: %v", err)
	}
}

func TestVerify_RejectsMismatch(t *testing.T) {
	s := newTestSigner(t)
	ctx := context.Background()
	url, err := s.BuildAuthorizedURL(ctx, WorkspaceContext{WorkspaceID: "ws-1"}, "asset-1")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := parseURL(t, url)

	cases := []struct {
		name   string
		mutate func(p parsedURL) parsedURL
	}{
		{"tampered_asset", func(p parsedURL) parsedURL { p.aid = "asset-2"; return p }},
		{"tampered_workspace", func(p parsedURL) parsedURL { p.wid = "ws-2"; return p }},
		{"tampered_exp", func(p parsedURL) parsedURL { p.exp = p.exp + 1; return p }},
		{"tampered_sig", func(p parsedURL) parsedURL { p.sig = "deadbeef"; return p }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.mutate(got)
			err := s.Verify(p.aid, p.wid, p.exp, p.sig)
			if err == nil {
				t.Fatalf("expected mismatch error for %s", tc.name)
			}
		})
	}
}

func TestVerify_RejectsExpired(t *testing.T) {
	// NewSigner clamps a non-positive TTL to the 5m default, so to
	// exercise the expired path we override ttl post-construction.
	// This is a test-only escape hatch; production never sees negative TTLs.
	s := newTestSigner(t)
	s.ttl = -1 * time.Second

	url, err := s.BuildAuthorizedURL(context.Background(),
		WorkspaceContext{WorkspaceID: "ws-1"}, "asset-1")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := parseURL(t, url)
	err = s.Verify(got.aid, got.wid, got.exp, got.sig)
	if err == nil || err.Error() != "delivery: signed url expired" {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}

func TestBuild_RejectsEmptyAssetID(t *testing.T) {
	s := newTestSigner(t)
	if _, err := s.BuildAuthorizedURL(context.Background(),
		WorkspaceContext{WorkspaceID: "ws-1"}, "  "); err == nil {
		t.Fatal("expected error on empty asset_id")
	}
}

func TestBuild_RejectsDefaultWorkspace(t *testing.T) {
	s := newTestSigner(t)
	if _, err := s.BuildAuthorizedURL(context.Background(),
		WorkspaceContext{WorkspaceID: "default"}, "asset-1"); err == nil {
		t.Fatal("expected error on default workspace")
	}
}

func TestBuild_AllowsArbitraryWorkspace(t *testing.T) {
	s := newTestSigner(t)
	for _, w := range []string{"ws-1", "ws-42", "tenant-A"} {
		if _, err := s.BuildAuthorizedURL(context.Background(),
			WorkspaceContext{WorkspaceID: w}, "asset-1"); err != nil {
			t.Errorf("workspace %q rejected: %v", w, err)
		}
	}
}

// ── helpers ────────────────────────────────────────────────────────────

// parseURL pulls out the four expected fields from a delivered URL
// without pulling in net/url (the test file's import surface is
// already small; `go vet ./...` is sensitive to shadow import cycles
// in this package today).
type parsedURL struct {
	exp int64
	sig string
	wid string
	aid string
}

func parseURL(t *testing.T, raw string) parsedURL {
	t.Helper()
	const delimiter = "/deliver/"
	const sep = "?"
	delimIdx := strings.Index(raw, delimiter)
	qIdx := strings.Index(raw, sep)
	if delimIdx < 0 || qIdx < 0 {
		t.Fatalf("unparseable URL: %q", raw)
	}
	aid := raw[delimIdx+len(delimiter) : qIdx]
	qs := raw[qIdx+1:]
	m := map[string]string{}
	for _, kv := range strings.Split(qs, "&") {
		eq := strings.SplitN(kv, "=", 2)
		if len(eq) != 2 {
			continue
		}
		m[eq[0]] = eq[1]
	}
	var exp int64
	for _, c := range m["exp"] {
		if c < '0' || c > '9' {
			t.Fatalf("non-numeric exp in URL: %q", raw)
		}
		exp = exp*10 + int64(c-'0')
	}
	return parsedURL{exp: exp, sig: m["sig"], wid: m["wid"], aid: aid}
}

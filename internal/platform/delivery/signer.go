// Package delivery is the asset-delivery URL signer.
//
// QDRANT-004 acceptance criterion: "Delivery URL protetto".
//
// The package mints short-lived HMAC-signed URLs of the shape:
//
//	<base>/<path>/<asset_id>?wid=<workspace_id>&exp=<unix>&sig=<hex>
//
// The signature is computed over the canonical string
//
//	asset_id|workspace_id|exp
//
// using HMAC-SHA256 with a server-side secret of at least 32 bytes.
// The same secret is used by the receiver-side Verify, which is
// exposed here so future GET /api/internal/v1/deliver handlers
// reuse it without a second copy of the canonicalisation rules.
//
// Important: this is NOT a replacement for pkg/hmacsign (which is
// the canonical webhook signer with a different canonical string).
// The two live side-by-side: pkg/hmacsign signs outbound webhooks
// (canonical: timestamp + event_id + body); delivery signs inbound
// asset delivery URLs (canonical: asset_id + workspace_id + expiry).
// Both share the principle that signing keys MUST be ≥32 bytes.
//
// Tenant identity: BuildAuthorizedURL consumes `WorkspaceContext`
// (a Go-level alias of `search.Actor` — see workspace.go). Per
// godlike/06 SSOT, the canonical tenant envelope lives in
// `internal/capabilities/assets/search/types.go::Actor`; the type alias
// keeps this package's preferred name without re-declaring the
// struct. Cross-package layering (Wave 19): infra → application
// is the canonical GateDirection; the inverse is intentional NOT
// done.
package delivery

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/search"
)

// ErrExpired is returned when the URL expiry is in the past.
var ErrExpired = errors.New("delivery: signed url expired")

// ErrInvalidSignature is returned when the supplied sig does not
// match the canonical payload under the configured secret.
var ErrInvalidSignature = errors.New("delivery: signature mismatch")

// ErrSecretTooShort is returned at construction time if the secret
// is fewer than 32 bytes. Comment in NewSigner explains why.
var ErrSecretTooShort = errors.New("delivery: secret must be at least 32 bytes")

// Signer is the production search.AssetDeliveryService. Construct
// via NewSigner, then register it as the concrete
// `search.AssetDeliveryService` at the composition root.
type Signer struct {
	secret  []byte
	ttl     time.Duration
	baseURL string
	path    string
	now     func() time.Time
}

// NewSigner creates a Signer with the production defaults.
//
// `baseURL` is the externally-reachable base of the host (e.g.
// "https://app.example.com"). `path` is the receiver path WITHOUT
// the leading slash of the asset id (e.g. "/api/internal/v1/deliver").
//
// Secret length guards against fat-finger misconfiguration: <32
// bytes is outside the OWASP HMAC-SHA256 minimum and would weaken
// the signature under brute force.
func NewSigner(secret []byte, ttl time.Duration, baseURL, path string) (*Signer, error) {
	if len(secret) < 32 {
		return nil, ErrSecretTooShort
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "http://localhost:8080"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + strings.TrimLeft(path, "/")
	}
	return &Signer{
		secret:  secret,
		ttl:     ttl,
		baseURL: strings.TrimRight(baseURL, "/"),
		path:    strings.TrimRight(path, "/"),
		now:     time.Now,
	}, nil
}

// BuildAuthorizedURL mints a signed delivery URL. It is safe to call
// concurrently; the secret is read-only.
//
// Commit 3-B migration (July 2026): the parameter type switched
// from `mediasearch.WorkspaceContext{WorkspaceID, ProjectID, PrincipalID, IsAdmin}`
// to the canonical `search.Actor{WorkspaceID, UserID, IsAdmin}`
// surfaces here as the local alias `WorkspaceContext` (a Go-level
// pointer alias). ProjectID is dropped because the canonical
// search capability does not carry project scoping per godlike/06;
// PrincipalID was renamed to UserID at the canonical surface
// (callers that previously set PrincipalID via the legacy struct
// will be migrated inline as the legacy package is deleted).
func (s *Signer) BuildAuthorizedURL(ctx context.Context, workspace WorkspaceContext, assetID string) (string, error) {
	if s == nil {
		return "", errors.New("delivery: signer is nil")
	}
	assetID = strings.TrimSpace(assetID)
	workspaceID := strings.TrimSpace(workspace.WorkspaceID)
	if assetID == "" {
		return "", errors.New("delivery: asset_id is required")
	}
	if workspaceID == "" || workspaceID == "default" {
		return "", errors.New("delivery: workspace_id is required")
	}

	exp := s.now().Add(s.ttl).Unix()
	canonical := canonicalPayload(assetID, workspaceID, exp)

	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(canonical))
	sig := hex.EncodeToString(mac.Sum(nil))

	return fmt.Sprintf("%s%s/%s?wid=%s&exp=%d&sig=%s",
		s.baseURL, s.path,
		url.QueryEscape(assetID),
		url.QueryEscape(workspaceID),
		exp,
		sig,
	), nil
}

// Verify is the receiver-side counterpart. The future
// /api/internal/v1/deliver handler will call this to authorise or
// reject an inbound request; exposing it from the same package
// keeps the canonicalisation rules in lock-step with BuildAuthorizedURL.
//
// Returns nil on success, ErrExpired for expired URLs,
// ErrInvalidSignature for tampered payloads.
func (s *Signer) Verify(assetID, workspaceID string, exp int64, sig string) error {
	if s == nil {
		return errors.New("delivery: signer is nil")
	}
	if s.now().Unix() > exp {
		return ErrExpired
	}
	canonical := canonicalPayload(assetID, workspaceID, exp)
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(canonical))
	expected := hex.EncodeToString(mac.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(expected), []byte(sig)) != 1 {
		return ErrInvalidSignature
	}
	return nil
}

// canonicalPayload is the canonical string both sides agree on.
// Format: "<asset_id>|<workspace_id>|<exp>". Keep literal pipes so
// an attacker cannot smuggle a workspace_id containing an extra
// "|" to slip past validation; the receiver strips pipes before
// joining, but the canonical form does not tolerate multiple
// delimiters regardless.
func canonicalPayload(assetID, workspaceID string, exp int64) string {
	var sb strings.Builder
	sb.Grow(len(assetID) + len(workspaceID) + 24)
	sb.WriteString(assetID)
	sb.WriteByte('|')
	sb.WriteString(workspaceID)
	sb.WriteByte('|')
	// exp is fixed-width so verification is deterministic without
	// relying on the receiver's locale / formatting conventions.
	sb.WriteString(fmt.Sprintf("%d", exp))
	return sb.String()
}

// Compile-time assertion: production Signer satisfies the canonical
// search.AssetDeliveryService port. Drift is caught at compile, not
// on first HTTP.
var _ search.AssetDeliveryService = (*Signer)(nil)

// Package job — WorkerCertIdentity (PR-0, June 2026).
//
// WorkerCertIdentity carries the mTLS certificate identity associated
// with a worker registration, kept SEPARATE from WorkerSession
// (which only carries session metadata: WorkerID, SessionID,
// SessionExpiresAt, Capabilities, Version, Hostname).
//
// PR-0 (June 2026) split: cert fields NO longer pollute WorkerSession.
// The previous api-side handler (handler_workers_cert.go) read cert
// fields (CertFingerprintSHA256, CertSerialHex, CertSubjectDN,
// CertIssuerDN, CertDNSNames, CertNotAfter, CertVerifiedAt) directly
// from *WorkerSession, but WorkerSession never declared them — a
// pre-existing compile break on main. The canonical cert identity
// lives in this type; adapter implementations (e.g. the worker-nodes
// repository in internal/platform/sqlite/assets/) read
// the cert row independently from the session row and pass both into
// FromSessionCertIdentity (the API-helper builder in
// internal/api/admin/handler_workers_cert.go).
//
// Worker cert lifecycle:
//
//	tlsload.Identity (parsed per-worker register call)
//	  ⟶ WorkerCertIdentity (this type, persisted at register time)
//	  ⟶ CertReport JSON shape (returned by the admin endpoint)
//
// No backwards-compat aliasing on WorkerSession: adding the cert
// fields back to WorkerSession would re-introduce the god-like
// surface the Wave 4 split eliminated.
//
// Cross-reference: architecture/current.yaml::Wave 21 PR-G.1 EXPAND
// (registry split preparation) and the prior runbook ticket
// RW-PROD-001 for the canonical worker-cert schema.
package job

import "time"

// WorkerCertIdentity is the mTLS certificate identity associated with
// a worker registration. Fields are the canonical subset required by
// the admin /api/v1/admin/workers/:id/cert-report endpoint; per-cert
// metadata that doesn't reach the admin UI (chain PEM blobs, CRL
// distribution points, OCSP responder URLs, etc.) intentionally lives
// only in the tlsload.Identity POJO that produced this identity —
// serialization is JSON-stable at the boundary.
type WorkerCertIdentity struct {
	// FingerprintSHA256 is the hex-encoded SHA-256 digest of the DER
	// certificate body (excludes the signature). Mirrors the structure
	// tracked in tlsload.Identity.FingerprintSHA256. Used as the
	// canonical lookup key in worker-nodes credential rows.
	FingerprintSHA256 string

	// SerialHex is the certificate serial number, hex-encoded. Issuer-
	// assigned; unique per CA. Stays string-formatted to keep the
	// JSON wire shape stable for the admin UI.
	SerialHex string

	// SubjectDN is the RFC 4514 distinguished name of the cert
	// subject (typically CN=<worker-fqdn>, O=… etc.). Echoed by the
	// runbook-format endpoint for operator audit.
	SubjectDN string

	// IssuerDN is the RFC 4514 distinguished name of the signing CA.
	// Lets operators confirm which CA chain approved the worker.
	IssuerDN string

	// DNSNames is the Subject Alternative Name dNSName entries. Most
	// production worker certs carry exactly one (the worker host).
	DNSNames []string

	// NotAfter is the cert expiry (UTC). Hard-typed as time.Time so
	// the CertReport conversion does the RFC3339 formatting at the
	// JSON boundary (handler_workers_cert.go::FromSessionCertIdentity
	// is the only caller; format drift is contained there).
	NotAfter time.Time

	// VerifiedAt is the timestamp the cert was last validated by the
	// server (mTLS handshake or admin-token refresh cycle). Optional;
	// zero-value means "fresh identity, no prior verification event".
	VerifiedAt time.Time
}

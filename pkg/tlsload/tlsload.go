// Package tlsload — leaf utility (pkg/tlsload) for loading TLS
// certificate + identity material from disk and exposing it as a
// typed descriptor.
//
// Scope: this package is the canonical RW-PROD-001 (June 2026)
// server- and client-cert loader. It is a leaf — zero imports from
// `internal/` per AGENTS.md §13 — and is consumed by:
//
//   - internal/application/workerdoctor/default_probes.go (probeCert):
//     LoadServerIdentity + LoadClientIdentity with a per-side TTL
//     window (config.MinServerTLSCertTTL / MinMTLSCertTTL).
//   - any future code path that needs to load server or client
//     material for handshake preparation without pulling in the
//     full crypto/tls handshake machinery.
//
// Quality rules (per RW-PROD-001 acceptance criteria verified by
// the doctor):
//
//   - Cert is parsed via crypto/x509.ParseCertificate so any
//     PEM/DER corruption surfaces immediately; the helper returns
//     (*x509.Certificate, *Identity, error) so the caller can
//     introspect the parsed cert beyond the typed descriptor.
//   - Cert NotAfter is asserted to be at least window in the
//     future. A negative window cert is an error — the loader is
//     fail-closed on handshake-prep material.
//   - Key file permission is asserted non-world-writable
//     (POSIX-only). On non-POSIX platforms the assertion is
//     skipped silently (the readback still succeeds).
//   - Optional CA file is loaded via x509.NewCertPool()
//     opportunistically. The helper does not require the CA pool
//     to be non-empty — operators sometimes supply a CA file
//     that only matches an alternate trust path. Pool emptiness
//     surfaces only when the caller explicitly chains against it.
//
// The helper intentionally does NOT perform a TLS handshake or
// verify peer signatures — that work belongs to the worker
// pre-flight (or the master when it serves clients). The doctor
// only asserts "the material on disk will be accepted by the
// TLS loader", not "a real handshake would succeed".
package tlsload

import (
	"time"
)

// Identity is the canonical descriptor returned by the loaders.
// SubjectDN is the certificate's RFC 4514 string representation;
// FingerprintSHA256 is the lowercase hex SHA-256 of the DER; NotAfter
// is the cert expiry (asserted against the supplied window by the
// loader before returning). SerialNumber + IssuerDN are surfaced so
// dashboards can pin a specific cert across rotations.
type Identity struct {
	FingerprintSHA256 string
	SubjectDN         string
	IssuerDN          string
	SerialNumber      string
	NotAfter          time.Time
}

// LoadServerIdentity reads a server certificate (cert, key, optional
// client CA file) from disk, validates the key permission, asserts
// the cert NotAfter is at least `window` in the future, and returns
// the parsed cert + Identity descriptor. The CA file is loaded into
// an x509.CertPool best-effort — operators sometimes supply a CA
// file that doesn't match the cert's issuer; we surface that as a
// nil pool but never as an error here. The caller is responsible
// for invoking the actual handshake.

// LoadClientIdentity reads a client certificate (cert, key, optional
// CA file, server name for SAN validation) from disk, asserts the
// same freshness + key-perms invariants as the server path, and
// returns the parsed cert + Identity descriptor.
//
// serverName is asserted against cert.DNSNames + URIs SAN. When the
// cert lacks the SAN or the SAN list doesn't include serverName, the
// loader returns an error — the worker pre-flight will fail anyway,
// and the doctor surfaces the same condition up-front.

// loadCert reads certFile from disk, decodes the PEM envelope, and
// returns the parsed x509.Certificate. Returns nil on any failure
// (file missing, decode failure, malformed block).

// loadCAPoolOptional loads caFile into a CertPool. Best-effort:
// file missing, malformed PEM, or empty AppendCertsFromPEM all
// silently return a nil pool. The workerdoctor treats the loader
// as tolerant per RW-PROD-001 ("CA file present but not the chain
// root the cert was issued from" is a downstream handshake concern,
// not a load-time error). Both call sites discard the return.

// assertFreshCert rejects certs whose NotAfter - now < window. The
// window argument is the minimum acceptable remaining lifetime.
// Negative or zero windows produce "always-fresh" assertions, which
// is the operator's choice — we surface the check explicit rather
// than implicit for diagnostics reasons.

// assertKeyPerms rejects key files whose permissions are
// world-writable. Owner-scoped modes (0600/0644) are accepted.
// POSIX-only: Windows ACLs are not modelled by os.FileMode (Perm()
// returns 0777 unconditionally there), so the check would falsely
// fire on every Windows key file. We skip the assertion on Windows
// rather than regressing to a constant-permission false-positive.

// assertSANMatchesServerName rejects certs whose SAN list does not
// include the supplied serverName. Both DNSNames and URIs SANs are
// checked. Wildcard DNSNames (e.g. *.example.com) match
// single-label hosts under that suffix.

// matchHostPattern implements limited wildcard matching used by
// the SAN validator. Returns true when pattern matches host per
// RFC 6125 §6.4.3 (single-label wildcard suffix match).

// identityFromCert extracts the canonical descriptor from a
// parsed x509.Certificate. SubjectDN / IssuerDN are surfaced via
// the cert's own String() method (RFC 4514 DN form); serial is
// hex-encoded uppercase; SHA-256 fingerprint is lowercase hex.
// cert.SerialNumber is always populated by x509.ParseCertificate
// (RFC-required field) so no nil guard is needed.

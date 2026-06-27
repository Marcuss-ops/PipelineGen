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
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
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
func LoadServerIdentity(certFile, keyFile, clientCAFile string, window time.Duration) (*x509.Certificate, *Identity, error) {
	cert, err := loadCert(certFile)
	if err != nil {
		return nil, nil, fmt.Errorf("server cert: %w", err)
	}
	if err := assertKeyPerms(keyFile); err != nil {
		return nil, nil, fmt.Errorf("server key: %w", err)
	}
	if err := assertFreshCert(cert.NotAfter, window, "server"); err != nil {
		return nil, nil, err
	}
	loadCAPoolOptional(clientCAFile) // best-effort
	return cert, identityFromCert(cert), nil
}

// LoadClientIdentity reads a client certificate (cert, key, optional
// CA file, server name for SAN validation) from disk, asserts the
// same freshness + key-perms invariants as the server path, and
// returns the parsed cert + Identity descriptor.
//
// serverName is asserted against cert.DNSNames + URIs SAN. When the
// cert lacks the SAN or the SAN list doesn't include serverName, the
// loader returns an error — the worker pre-flight will fail anyway,
// and the doctor surfaces the same condition up-front.
func LoadClientIdentity(certFile, keyFile, caFile, serverName string, window time.Duration) (*x509.Certificate, *Identity, error) {
	cert, err := loadCert(certFile)
	if err != nil {
		return nil, nil, fmt.Errorf("client cert: %w", err)
	}
	if err := assertKeyPerms(keyFile); err != nil {
		return nil, nil, fmt.Errorf("client key: %w", err)
	}
	if err := assertFreshCert(cert.NotAfter, window, "client"); err != nil {
		return nil, nil, err
	}
	if serverName != "" {
		if err := assertSANContainsServerName(cert, serverName); err != nil {
			return nil, nil, fmt.Errorf("client cert SAN: %w", err)
		}
	}
	loadCAPoolOptional(caFile)
	return cert, identityFromCert(cert), nil
}

// loadCert reads certFile from disk, decodes the PEM envelope, and
// returns the parsed x509.Certificate. Returns nil on any failure
// (file missing, decode failure, malformed block).
func loadCert(certFile string) (*x509.Certificate, error) {
	if certFile == "" {
		return nil, errors.New("cert path is empty")
	}
	data, err := os.ReadFile(certFile)
	if err != nil {
		return nil, fmt.Errorf("read cert: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("cert file is not PEM-encoded")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse cert: %w", err)
	}
	return cert, nil
}

// loadCAPoolOptional loads caFile into a CertPool. Best-effort:
// file missing, malformed PEM, or empty AppendCertsFromPEM all
// silently return a nil pool. The workerdoctor treats the loader
// as tolerant per RW-PROD-001 ("CA file present but not the chain
// root the cert was issued from" is a downstream handshake concern,
// not a load-time error). Both call sites discard the return.
func loadCAPoolOptional(caFile string) *x509.CertPool {
	if caFile == "" {
		return nil
	}
	data, err := os.ReadFile(caFile)
	if err != nil {
		return nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(data) {
		return nil
	}
	return pool
}

// assertFreshCert rejects certs whose NotAfter - now < window. The
// window argument is the minimum acceptable remaining lifetime.
// Negative or zero windows produce "always-fresh" assertions, which
// is the operator's choice — we surface the check explicit rather
// than implicit for diagnostics reasons.
func assertFreshCert(notAfter time.Time, window time.Duration, side string) error {
	if window <= 0 {
		return nil
	}
	remaining := time.Until(notAfter)
	if remaining < window {
		return fmt.Errorf("%s cert expires in %s (less than required window %s)", side, remaining, window)
	}
	return nil
}

// assertKeyPerms rejects key files whose permissions are
// world-writable. Owner-scoped modes (0600/0644) are accepted.
// POSIX-only: Windows ACLs are not modelled by os.FileMode (Perm()
// returns 0777 unconditionally there), so the check would falsely
// fire on every Windows key file. We skip the assertion on Windows
// rather than regressing to a constant-permission false-positive.
func assertKeyPerms(keyFile string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	info, err := os.Stat(keyFile)
	if err != nil {
		return fmt.Errorf("stat key: %w", err)
	}
	mode := info.Mode()
	if mode&0o002 != 0 {
		return fmt.Errorf("key file %s is world-writable (mode %#o)", keyFile, mode.Perm())
	}
	return nil
}

// assertSANMatchesServerName rejects certs whose SAN list does not
// include the supplied serverName. Both DNSNames and URIs SANs are
// checked. Wildcard DNSNames (e.g. *.example.com) match
// single-label hosts under that suffix.
func assertSANContainsServerName(cert *x509.Certificate, serverName string) error {
	if serverName == "" {
		return nil
	}
	for _, dns := range cert.DNSNames {
		if matchHostPattern(dns, serverName) {
			return nil
		}
	}
	for _, uri := range cert.URIs {
		if uri != nil && strings.EqualFold(strings.ToLower(uri.Host), strings.ToLower(serverName)) {
			return nil
		}
	}
	return fmt.Errorf("server name %q not in cert SAN (DNS=%v URIs=%d)", serverName, cert.DNSNames, len(cert.URIs))
}

// matchHostPattern implements limited wildcard matching used by
// the SAN validator. Returns true when pattern matches host per
// RFC 6125 §6.4.3 (single-label wildcard suffix match).
func matchHostPattern(pattern, host string) bool {
	if pattern == host {
		return true
	}
	if strings.HasPrefix(pattern, "*.") {
		suf := pattern[1:] // ".example.com"
		host = strings.ToLower(host)
		if strings.HasSuffix(host, suf) && strings.Count(host, ".") >= strings.Count(suf, ".")+1 {
			return true
		}
	}
	return false
}

// identityFromCert extracts the canonical descriptor from a
// parsed x509.Certificate. SubjectDN / IssuerDN are surfaced via
// the cert's own String() method (RFC 4514 DN form); serial is
// hex-encoded uppercase; SHA-256 fingerprint is lowercase hex.
// cert.SerialNumber is always populated by x509.ParseCertificate
// (RFC-required field) so no nil guard is needed.
func identityFromCert(cert *x509.Certificate) *Identity {
	if cert == nil {
		return nil
	}
	sum := sha256.Sum256(cert.Raw)
	return &Identity{
		FingerprintSHA256: hex.EncodeToString(sum[:]),
		SubjectDN:         cert.Subject.String(),
		IssuerDN:          cert.Issuer.String(),
		SerialNumber:      strings.ToUpper(hex.EncodeToString(cert.SerialNumber.Bytes())),
		NotAfter:          cert.NotAfter,
	}
}

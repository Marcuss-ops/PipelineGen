// Package checksum is the single canonical owner of the MD5 algorithm.
//
// MD5 is compatibility-only. It exists for two narrow, named purposes and
// nothing else:
//
//   - LegacyMD5* — compatibility hashes for legacy database columns, legacy
//     import paths, and pre-existing local-file fingerprints. These are
//     NEVER an identity signal and NEVER a dedup key (content identity is
//     owned by internal/kernel/digest's SHA-256).
//   - ProviderMD5Checksum — the md5Checksum value returned by Google Drive.
//     This is a provider-supplied opaque compatibility token, not a content
//     digest we compute.
//
// The names are deliberately NOT generic (no bare `MD5File`/`MD5String`) so a
// developer cannot accidentally reach for MD5 as an identity primitive. Any
// new content identity, dedup, or idempotency key MUST use
// internal/kernel/digest, never this package.
//
// This package is the ONLY production package authorized to import
// crypto/md5 (enforced by cmd/archcheck's percheck_digest_md5_ban).
package checksum

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

// ErrInvalidProviderMD5 is the fail-closed sentinel returned by
// ProviderMD5Checksum when a provider-supplied value is not a well-formed
// 32-character hex digest. Callers must NOT persist an invalid provider
// checksum (godlike/07 NO-FAKE-AVAILABILITY: an unavailable backend is never
// a successful no-op).
var ErrInvalidProviderMD5 = errors.New("invalid provider MD5 checksum: expected 32 hex characters")

// providerMD5Re matches a canonical 32-character hexadecimal MD5 digest.
var providerMD5Re = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// LegacyMD5String returns the lowercase hex MD5 digest of an in-memory string.
// Compatibility-only: never use for identity or dedup.
func LegacyMD5String(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// LegacyMD5Reader streams the MD5 digest of r and returns the lowercase hex
// digest. It never buffers the whole input in memory.
func LegacyMD5Reader(r io.Reader) (string, error) {
	h := md5.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// LegacyMD5File streams the MD5 digest of the file at path and returns the
// lowercase hex digest. Compatibility-only: never use for identity or dedup.
func LegacyMD5File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return LegacyMD5Reader(f)
}

// IsProviderMD5 reports whether s is a well-formed 32-character hex digest
// (the shape Google Drive returns for md5Checksum).
func IsProviderMD5(s string) bool {
	return providerMD5Re.MatchString(s)
}

// ProviderMD5Checksum validates a provider-supplied MD5 checksum (Google
// Drive md5Checksum) and returns its canonical lowercase form. It fails
// closed with ErrInvalidProviderMD5 on malformed input — callers must not
// treat an unavailable or malformed provider value as a valid checksum.
func ProviderMD5Checksum(s string) (string, error) {
	if !IsProviderMD5(s) {
		return "", fmt.Errorf("%w: %q", ErrInvalidProviderMD5, s)
	}
	return lowercaseHex(s), nil
}

// lowercaseHex lowercases a validated hex string without importing strings.
func lowercaseHex(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'F' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

package digest

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidSHA256 is returned by ValidateSHA256 when a non-empty digest is
// not a SHA256HexLength-character hex string.
var ErrInvalidSHA256 = errors.New("digest: invalid sha256")

// IsSHA256 reports whether s is a SHA256HexLength-character hex string (the
// canonical SHA-256 digest shape). Used to distinguish a real byte digest
// from an MD5 (32 chars) or a fabricated value.
func IsSHA256(s string) bool {
	if len(s) != SHA256HexLength {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// IsCanonicalSHA256 reports whether s is a digest in the CANONICAL WIRE FORM:
// a SHA256HexLength-character SHA-256 that is also all-lowercase hex.
//
// It is the rule every sealed plan, checkpoint, replay bundle and resume
// document depends on, and it is deliberately NOT the same question as
// IsSHA256: a digest that differs only in case names the same bytes but is not
// the byte string this tree writes, compares and content-addresses. Accepting
// "AB12…" at a boundary therefore does not normalise it — it lets two spellings
// of one artifact travel, and any consumer that keys on the literal string
// (CAS address, job dedup key, plan digest) then sees two identities for one
// artifact. Boundaries reject the non-canonical spelling instead of rewriting
// it.
//
// This is the single owner of that rule. It was previously re-implemented in
// checkpoint, replay, cliprender and localization — four hand-rolled copies of
// one comparison, kept aligned by a comment, one of them describing itself as
// a "mirror" of another.
func IsCanonicalSHA256(s string) bool {
	if !IsSHA256(s) {
		return false
	}
	return strings.ToLower(s) == s
}

// ValidateSHA256 fails closed on the SHA-256 contract: an empty string is
// allowed (no digest), but any non-empty digest MUST be a SHA256HexLength-hex
// SHA-256. MD5 values and fabricated digests are rejected.
func ValidateSHA256(s string) error {
	if s == "" {
		return nil
	}
	if !IsSHA256(s) {
		return fmt.Errorf("%w: %q is not a %d-hex SHA-256", ErrInvalidSHA256, s, SHA256HexLength)
	}
	return nil
}

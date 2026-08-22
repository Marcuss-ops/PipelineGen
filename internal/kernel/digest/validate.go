package digest

import (
	"encoding/hex"
	"errors"
	"fmt"
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

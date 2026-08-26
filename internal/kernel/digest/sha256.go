package digest

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
	"io"
	"os"
)

// NewSHA256 returns the canonical streaming SHA-256 implementation for
// callers that need to append structured fields before finalizing a digest.
// The algorithm is owned by this package (godlike/06 SSOT).
func NewSHA256() hash.Hash { return sha256.New() }

// SHA256Bytes returns the canonical SHA-256 hex digest (SHA256HexLength hex
// chars) of the given byte slice. It is the single content-identity
// primitive: the digest is computed over the REAL bytes, never over IDs,
// URLs, or metadata.
func SHA256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SHA256String returns the canonical SHA-256 hex digest of the raw bytes of
// the given string.
func SHA256String(text string) string { return SHA256Bytes([]byte(text)) }

// SHA256Reader streams r to completion and returns the canonical SHA-256 hex
// digest of the bytes read. It never buffers the whole stream in memory, so
// it is safe for large media files. A file digest is:
//
//	open → digest.SHA256Reader
func SHA256Reader(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SHA256File opens the file at path, streams it through SHA-256, and
// returns the hex digest plus the byte count transferred. It never buffers
// the whole file in memory, so it is safe for large media files.
func SHA256File(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

package digest

import (
	"hash"
	"io"

	leafdigest "github.com/Marcuss-ops/PipelineGen/pkg/digest"
)

// NewSHA256 returns the canonical streaming SHA-256 implementation for
// callers that need to append structured fields before finalizing a digest.
// The algorithm remains owned by this package.
func NewSHA256() hash.Hash { return leafdigest.NewSHA256() }

// SHA256Bytes returns the canonical SHA-256 hex digest (SHA256HexLength hex
// chars) of the given byte slice. It is the single content-identity
// primitive: the digest is computed over the REAL bytes, never over IDs,
// URLs, or metadata.
func SHA256Bytes(data []byte) string {
	return leafdigest.SHA256Bytes(data)
}

// SHA256String returns the canonical SHA-256 hex digest of the raw bytes of
// the given string.
func SHA256String(text string) string {
	return leafdigest.SHA256String(text)
}

// SHA256Reader streams r to completion and returns the canonical SHA-256 hex
// digest of the bytes read. It never buffers the whole stream in memory, so
// it is safe for large media files. A file digest is:
//
//	open → digest.SHA256Reader
func SHA256Reader(r io.Reader) (string, error) {
	return leafdigest.SHA256Reader(r)
}

// SHA256File opens the file at path, streams it through SHA-256, and
// returns the hex digest plus the byte count transferred. It never buffers
// the whole file in memory, so it is safe for large media files.
func SHA256File(path string) (string, int64, error) {
	return leafdigest.SHA256File(path)
}

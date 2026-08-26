// Package digest is the canonical Single Source Of Truth (SSOT) for the
// SHA-256 hashing algorithm in PipelineGen.
//
// godlike/06 SSOT: this is the ONLY package authorized to import
// crypto/sha256 for application hashing. It owns the ALGORITHM (how bytes
// become a digest); each domain keeps owning the MEANING of the data it
// hashes:
//
//   - content hashes   "Which bytes are these?"   → digest.SHA256Bytes /
//     digest.SHA256Reader over the REAL byte stream. Never over IDs, URLs,
//     or metadata.
//   - fingerprints     "Is this config the same?" → digest.Fingerprint over
//     domain-canonicalized parts.
//   - idempotency keys "Did this op already run?" → digest over composed
//     key shapes (see pkg/idempotency).
//
// Callers MUST NOT roll their own sha256.Sum256 / sha256.New outside this
// package. A file digest is SHA-256 over the materialized bytes: open the
// file, stream it through digest.SHA256Reader, and never buffer whole files
// in memory.
//
// The SEMANTICS of each hash field (content_sha256, semantic_hash,
// embedding_contract_hash, legacy_file_md5) remain owned by
// internal/capabilities/mediaregistry/hashes.go; this package supplies only
// the primitive.
package digest

// SHA256HexLength is the canonical length, in hex characters, of a SHA-256
// digest (256 bits / 4 bits per hex char).
const SHA256HexLength = 64

// HashFunc is the canonical typed port for SHA-256-style string hashing
// (previously owned by internal/capabilities/remote/hashutil). It is a
// `func(s string) string` so any byte-stable string hasher (e.g. the
// canonical digest.SHA256String) satisfies it without an explicit
// implements clause — Go's structural typing on function types.
//
// godlike/07 fail-closed contract: callers that take a HashFunc MUST
// reject nil at construction time (panic-on-missing-dep convention).
type HashFunc func(s string) string

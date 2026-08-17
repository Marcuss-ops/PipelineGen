// Package mediaregistry — hashes.go: the canonical hash semantics (SSOT).
//
// godlike/06 SSOT: this file is the SINGLE owner of the meaning of every
// hash field in the media registry. Five hash "kinds" exist, and they are
// NOT interchangeable — each answers a different question:
//
//	content_sha256          "Which bytes are these?"   — identity of the
//	                        BYTES. Always SHA-256 (64 hex chars), produced
//	                        by hashing the materialized byte stream. Two
//	                        logical assets with identical bytes share ONE
//	                        content_sha256 (CAS invariant). When the bytes
//	                        are not known (e.g. a Drive-only asset without a
//	                        materialized file), the value is the
//	                        ContentSHA256Unknown sentinel — NEVER fabricated
//	                        from a Drive file ID, URL, or provider ID.
//
//	semantic_hash           "What does this mean?"     — fingerprint of the
//	                        semantically-relevant METADATA (title,
//	                        description, summary, entities, tags, transcript,
//	                        visual summary). Changes when the meaning
//	                        changes, not when the bytes change.
//
//	semantic_document_hash  "What text went to the embedder?" — fingerprint
//	                        of the EXACT DocumentText the
//	                        SemanticDocumentComposer produced and that was
//	                        sent to the embedder. One composition, one digest.
//
//	embedding_contract_hash "Which model/space?"       — fingerprint of the
//	                        embedding contract (model id, revision,
//	                        dimension, normalization, distance, prefixes,
//	                        semantic document version). See
//	                        internal/kernel/embedding.Contract.Hash.
//
//	legacy_file_md5         compatibility-only MD5 (32 hex chars). NEVER
//	                        identity, NEVER dedup. Retained only where an
//	                        external system (Google Drive) already reports an
//	                        MD5 and historical tooling needs it.
//
// Invariants:
//   - content_sha256 is ALWAYS a 64-hex SHA-256 when known, otherwise the
//     ContentSHA256Unknown sentinel. It is NEVER fabricated from a Drive
//     file ID, URL, or provider ID.
//   - binary_sha256 is a compatibility projection of content_sha256: the
//     same bytes must yield the same digest in both surfaces.
//   - MD5 (legacy_file_md5) is never used for identity or deduplication.
//   - semantic_hash / semantic_document_hash / embedding_contract_hash are
//     computed with Fingerprint (one canonical algorithm, one owner).
package mediaregistry

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Canonical hash column/field names. Reference these constants instead of
// re-spelling the strings so a future rename or canonicalization has one
// owner (godlike/06 SSOT).
const (
	// ContentSHA256Field names the byte-identity digest column.
	ContentSHA256Field = "content_sha256"
	// SemanticHashField names the metadata fingerprint column.
	SemanticHashField = "semantic_hash"
	// SemanticDocumentHashField names the DocumentText fingerprint column.
	SemanticDocumentHashField = "semantic_document_hash"
	// IndexRevisionField names the index-revision fingerprint key — the
	// snapshot Qdrant must represent. Distinct from content_sha256 (byte
	// identity) and semantic_document_hash (embedder text identity): it
	// folds byte identity + the indexable semantic surface (text tracks,
	// taxonomy, metadata) so the supersede gate fires only when the
	// indexable snapshot actually changed.
	IndexRevisionField = "index_revision"
	// EmbeddingContractHashField names the model-contract fingerprint.
	EmbeddingContractHashField = "embedding_contract_hash"
	// LegacyFileMD5Field names the compatibility-only MD5 surface.
	LegacyFileMD5Field = "legacy_file_md5"
	// BinarySHA256Field names the compatibility SHA-256 projection.
	BinarySHA256Field = "binary_sha256"
)

// ContentSHA256Unknown is the canonical sentinel for "the byte content is not
// known". A Drive-only asset without a materialized file carries this value
// in content_sha256. It is the ONLY value allowed when the bytes have not
// been hashed — a content digest must never be invented from a Drive file ID,
// URL, or provider ID.
const ContentSHA256Unknown = "UNKNOWN"

// ErrInvalidContentSHA256 is returned by ValidateContentSHA256 when a
// non-empty content digest is not a 64-hex SHA-256 (e.g. it is an MD5 or a
// fabricated value). Identity is fail-closed: a digest we cannot prove is a
// SHA-256 is rejected, not trusted.
var ErrInvalidContentSHA256 = errors.New("mediaregistry: invalid content sha256")

// Fingerprint returns the canonical deterministic SHA-256 hex fingerprint of
// the given parts. It is the ONE algorithm used to derive semantic_hash,
// semantic_document_hash and embedding_contract_hash — callers MUST NOT roll
// their own sha256.Sum256 over an ad-hoc string. Parts are joined with a NUL
// byte so "a|b" + "c" and "a" + "b|c" cannot collide; callers must not pass
// parts that themselves contain NUL.
func Fingerprint(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// IsSHA256Hex reports whether s is a 64-character hex string (the canonical
// SHA-256 digest shape). Used to distinguish a real byte digest from an MD5
// (32 chars) or a fabricated value.
func IsSHA256Hex(s string) bool {
	if len(s) != 64 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// IsMD5Hex reports whether s is a 32-character hex string (the legacy MD5
// digest shape). Used only to classify legacy_file_md5 — never to establish
// identity or deduplicate.
func IsMD5Hex(s string) bool {
	if len(s) != 32 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// NormalizeContentSHA256 canonicalizes a content digest: surrounding
// whitespace is trimmed and the empty value (or an explicit UNKNOWN) becomes
// ContentSHA256Unknown. Any other value is returned verbatim for the caller
// to validate with ValidateContentSHA256.
func NormalizeContentSHA256(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || s == ContentSHA256Unknown {
		return ContentSHA256Unknown
	}
	return s
}

// ValidateContentSHA256 fails closed on the content identity contract: an
// unknown digest (empty or UNKNOWN) is allowed, but any non-empty digest MUST
// be a 64-hex SHA-256. MD5 values and fabricated digests are rejected.
func ValidateContentSHA256(s string) error {
	s = NormalizeContentSHA256(s)
	if s == ContentSHA256Unknown {
		return nil
	}
	if !IsSHA256Hex(s) {
		return fmt.Errorf("%w: %q is not a 64-hex SHA-256", ErrInvalidContentSHA256, s)
	}
	return nil
}

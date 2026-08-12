// Package mediaregistry — content_objects.go: canonical contracts for the
// CAS content registry.
//
// CAS design (August 2026): the SHA-256 of the bytes is the PRIMARY KEY of
// every immutable content object. Logical media_assets reference these
// objects through content_sha256; multiple logical assets with identical
// bytes share ONE physical object (global byte deduplication). The physical
// blob lives in the content-addressed store (<dataDir>/blobs/sha256/XX/...);
// this contract describes the registry of what exists, where it is, and
// whether the stored digest has been verified.
//
// Invariant (canonical): filename / URL / Drive folder DO NOT establish
// identity. SHA-256 establishes byte identity. Objects are immutable.
package mediaregistry

import (
	"context"
	"errors"
)

// Integrity status values for ContentObject.IntegrityStatus.
const (
	// IntegrityUnverified is the default: the object was registered but its
	// on-disk bytes have not yet been hashed against the registry digest.
	IntegrityUnverified = "UNVERIFIED"
	// IntegrityVerified means the on-disk bytes matched the registry SHA-256
	// at verified_at.
	IntegrityVerified = "VERIFIED"
	// IntegrityCorrupt means the stored digest no longer matches the bytes
	// (CAS_CORRUPTION_DETECTED event consumers should trigger repair).
	IntegrityCorrupt = "CORRUPT"
)

var (
	// ErrContentObjectInvalid is returned when a ContentObject fails the
	// identity contract (empty sha256 / storage_uri) or has a negative size.
	ErrContentObjectInvalid = errors.New("mediaregistry: invalid content object")
	// ErrContentObjectNotFound is returned by Delete/Verify when the object
	// does not exist in the registry. Get follows the nil-returns-nil-error
	// convention instead (callers branch on presence).
	ErrContentObjectNotFound = errors.New("mediaregistry: content object not found")
)

// ContentObject is the physical content identity in the CAS registry. The
// SHA-256 digest of the byte stream is the primary key: identical bytes
// yield identical identity regardless of filename, URL, or provider.
type ContentObject struct {
	SHA256          string
	SizeBytes       int64
	MimeType        string
	StorageURI      string
	CreatedAt       string // RFC3339 UTC
	VerifiedAt      string // RFC3339 UTC; empty when never verified
	IntegrityStatus string // one of Integrity* constants
}

// ContentObjectStore is the CAS content registry port. It stores only
// registry rows (the bytes live in the content-addressed blob store); the
// adapter owns the SQLite table content_objects.
//
// godlike/07 fail-closed contract:
//   - Get returns (nil, nil) when the object does NOT exist (NOT
//     (nil, ErrNotFound)); callers branch on nil.
//   - Put is idempotent on sha256: re-putting the same digest MERGES the
//     row, never duplicates.
type ContentObjectStore interface {
	// Put upserts a content object row keyed by SHA-256. Idempotent on the
	// digest. Errors: ErrContentObjectInvalid for an empty sha256/storage_uri
	// or negative size_bytes.
	Put(ctx context.Context, obj ContentObject) error

	// Get returns the content object for sha256, or (nil, nil) if no row
	// exists. Errors: ErrContentObjectInvalid for an empty sha256.
	Get(ctx context.Context, sha256 string) (*ContentObject, error)

	// Delete removes the registry row for sha256. Idempotent: deleting a
	// missing object is a no-op success. The physical blob is NOT removed
	// here (orphan detection belongs to the CAS integrity scanner).
	Delete(ctx context.Context, sha256 string) error

	// Verify marks the object as IntegrityVerified at verifiedAt.
	// Errors: ErrContentObjectNotFound if the row is absent.
	Verify(ctx context.Context, sha256 string, verifiedAt string) error

	// Count returns the total number of registered content objects.
	Count(ctx context.Context) (int64, error)
}

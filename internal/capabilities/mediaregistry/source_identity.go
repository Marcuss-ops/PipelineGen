// Package mediaregistry — source_identity.go: canonical contract for the
// source identity registry.
//
// CAS design (August 2026): the acquisition flow asks "do we already know
// what bytes this source resolves to?" BEFORE downloading. This registry
// remembers the source -> content SHA-256 mapping so a repeat acquisition
// of the same Drive file / Artlist asset / URL skips the download entirely
// (CAS lookup before download). See migrations/sqlite/198_source_identity_registry.sql.
//
// Invariant: source identity is metadata used to avoid redundant
// downloads — it never establishes content identity (SHA-256 does, per the
// CAS contract in content_objects.go). A source row may be re-pointed to a
// new digest when provider content changes (version/etag bump).
package mediaregistry

import (
	"context"
	"errors"
)

// Canonical source_type values for SourceIdentity.SourceType.
const (
	SourceIdentityDrive   = "drive"
	SourceIdentityArtlist = "artlist"
	SourceIdentityURL     = "url"
	SourceIdentityYouTube = "youtube"
	SourceIdentityManual  = "manual"
)

// Verification status values for SourceIdentity.VerificationStatus.
const (
	SourceIdentityUnverified = "UNVERIFIED"
	SourceIdentityVerified   = "VERIFIED"
)

var (
	// ErrSourceIdentityInvalid is returned when a SourceIdentity fails the
	// registry contract: empty source_type / source_key / content_sha256 /
	// discovered_at / last_seen_at.
	ErrSourceIdentityInvalid = errors.New("mediaregistry: invalid source identity")
)

// SourceIdentity is one resolved mapping: the external source (type + key)
// currently resolves to ContentSHA256 bytes. The primary key is
// (SourceType, SourceKey).
type SourceIdentity struct {
	SourceType         string // drive | artlist | url | youtube | manual
	SourceKey          string // Drive file ID, Artlist asset ID, canonical URL, ...
	ContentSHA256      string // sha256 of the bytes the source resolves to
	SourceVersion      string // provider etag / modified_time / version (empty when unknown)
	DiscoveredAt       string // RFC3339 UTC — when this mapping was first seen
	LastSeenAt         string // RFC3339 UTC — when it was last confirmed
	VerificationStatus string // one of SourceIdentity* constants
}

// SourceIdentityStore is the source identity registry port. It lets the
// acquisition flow look up a known digest BEFORE downloading and record a
// freshly-discovered digest AFTER the download (streaming SHA-256).
//
// godlike/07 fail-closed contract:
//   - Lookup returns (nil, nil) when the identity is NOT known (NOT
//     (nil, ErrNotFound)); callers branch on nil and proceed to download.
//   - Record is idempotent on (source_type, source_key): re-recording
//     refreshes content_sha256 / source_version / last_seen_at without
//     duplicating the row.
type SourceIdentityStore interface {
	// Lookup returns the identity for (sourceType, sourceKey), or (nil, nil)
	// when the mapping is unknown. Errors: ErrSourceIdentityInvalid for empty
	// inputs; adapter errors for database failures.
	Lookup(ctx context.Context, sourceType, sourceKey string) (*SourceIdentity, error)

	// Record upserts the identity mapping. Idempotent on (source_type,
	// source_key). Errors: ErrSourceIdentityInvalid for empty identity fields.
	Record(ctx context.Context, id SourceIdentity) error

	// Count returns the total number of recorded source identities.
	Count(ctx context.Context) (int64, error)
}

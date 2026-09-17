package assetop

import (
	"context"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ExistingAssetQuery defines the query parameters for finding existing assets.
//
// The query carries TWO different identities on purpose, and conflating them is
// the failure this contract exists to prevent:
//
//	ID            — LOGICAL identity. "Which asset is this?"
//	ContentSHA256 — CONTENT identity. "Which bytes are these?"
//
// Two logical assets may legitimately share one content identity (the same MP4
// used by a YouTube clip and by a movie-quote asset): the bytes are stored
// once, the assets are two rows. A query that answers "these bytes already
// exist" must never be read as "this asset already exists".
type ExistingAssetQuery struct {
	// ID is the logical asset identity (media_assets.id).
	ID string
	// ContentSHA256 is the canonical content identity: the SHA-256 hex digest
	// of the asset bytes (kernel/digest). Empty means UNKNOWN — never "same as
	// whatever the other field holds".
	ContentSHA256 string
	// DriveFileID is the Google Drive file ID.
	DriveFileID string `json:"-"`
	// Filename is the asset filename.
	Filename string
	// Source is the asset source (youtube, artlist, voiceover, etc.)
	Source string
	// LegacyFileMD5 is the legacy MD5-tier digest.
	//
	// Deprecated: compatibility only — migration, diagnostics, and adapters
	// that can only answer the legacy tier. NO decision may read it. An MD5 is
	// not a content address: matching on it makes two different payloads look
	// identical, which is how a "dedup" turns into an asset collapse.
	LegacyFileMD5 string `json:"-"`
}

// AssetRecord represents a common asset record for lifecycle management.
type AssetRecord struct {
	ID          string
	Name        string
	Filename    string
	Source      string
	MediaType   string
	DriveFileID string `json:"-"`
	DriveLink   string `json:"-"`
	// DownloadLink is the direct download URL surfaced by Drive.
	DownloadLink string `json:"-"`
	// ContentHash is the canonical content identity (SHA-256 of the bytes).
	// Empty means UNKNOWN. This is the only hash field a decision may compare.
	ContentHash string
	// LegacyFileMD5 is the compatibility-only digest. No decision reads it.
	LegacyFileMD5 string `json:"-"`
	LocalPath     string `json:"-"`
	Status        string
	Error         string
	Metadata      string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AssetRecordStore defines the interface for storing and querying asset records.
type AssetRecordStore interface {
	// FindExisting finds an existing asset record by the given query. A query
	// carrying ID resolves the LOGICAL identity; a query carrying
	// ContentSHA256 resolves the CONTENT identity. Implementations that cannot
	// answer the content tier must return (nil, nil) — a new asset with new
	// bytes is the safe default — and must NOT fall back to the legacy MD5.
	FindExisting(ctx context.Context, query ExistingAssetQuery) (*AssetRecord, error)
	// ListWithDriveFileID lists all records that have a non-empty Drive file ID.
	ListWithDriveFileID(ctx context.Context, source string) ([]*AssetRecord, error)
	// MarkDriveMissing marks a record as missing Drive file.
	MarkDriveMissing(ctx context.Context, id string) error
	// DeleteAssetRecord deletes an asset record by ID.
	DeleteAssetRecord(ctx context.Context, id string) error
}

// DuplicateKind classifies the evidence a duplicate check actually found.
// SameAsset and SameContent are identity evidence; FilenameMatch is only a
// weak location hint and must never authorize skipping an asset.
type DuplicateKind int

const (
	// DuplicateNone: nothing is known. New logical asset, new bytes.
	DuplicateNone DuplicateKind = iota
	// DuplicateSameAsset: the SAME logical asset (same ID) already exists.
	// SameContent distinguishes an idempotent re-submission from a
	// replacement/version conflict. A conflict MUST NOT be reported as a
	// duplicate: silently skipping it would discard the new bytes.
	DuplicateSameAsset
	// DuplicateSameContent: a DIFFERENT logical asset already owns these
	// bytes. The record must still be created — the caller reuses the storage
	// and keeps its own logical identity. Skipping here is the asset-collapse
	// bug this kind exists to rule out.
	DuplicateSameContent
	// DuplicateFilenameMatch: a filename matched, but neither logical identity
	// nor byte identity is established. This is only a hint for diagnostics and
	// must never be treated as an idempotent duplicate.
	DuplicateFilenameMatch
)

// String renders the kind for logs and test failures.
func (k DuplicateKind) String() string {
	switch k {
	case DuplicateSameAsset:
		return "same_asset"
	case DuplicateSameContent:
		return "same_content"
	case DuplicateFilenameMatch:
		return "filename_match"
	default:
		return "none"
	}
}

// DuplicateMatch is the canonical result of a duplicate check.
type DuplicateMatch struct {
	Kind DuplicateKind
	// Record is the matched row. Nil when Kind is DuplicateNone.
	Record *AssetRecord
	// SameContent reports whether the matched row's content identity equals the
	// queried one. It is true only when BOTH identities are known and equal:
	// an unknown identity is never "the same", because a wrong "same" silently
	// discards bytes.
	SameContent bool
}

// Matched reports whether any duplicate identity was found.
func (m *DuplicateMatch) Matched() bool { return m != nil && m.Kind != DuplicateNone }

// DedupeService provides duplicate checking for assets.
type DedupeService struct {
	store  AssetRecordStore
	policy DuplicatePolicy
	log    *zap.Logger
}

// Policy returns the duplicate policy.
func (s *DedupeService) Policy() DuplicatePolicy {
	return s.policy
}

// NewDedupeService creates a new DedupeService.
func NewDedupeService(store AssetRecordStore, policy DuplicatePolicy, log *zap.Logger) *DedupeService {
	if log == nil {
		log = zap.NewNop()
	}
	return &DedupeService{
		store:  store,
		policy: policy,
		log:    log,
	}
}

// CheckDuplicate resolves what is already known about an asset, separating the
// logical identity from the content identity.
//
// Resolution order is the contract:
//
//  1. The asset ID is asked FIRST, because it is the stronger claim: if the
//     asset exists, the only remaining question is whether its bytes changed.
//     A changed byte string is a conflict/replacement, NOT a duplicate.
//  2. Only when the logical asset is unknown is the content identity asked. A
//     hit there is a DIFFERENT asset sharing these bytes: new logical asset,
//     reused storage.
//  3. Drive file id / filename are locations, not identities; they are checked
//     only when the identity tiers could not answer.
//
// The legacy MD5 tier is never consulted. It is compatibility-only, and using
// it here would let an MD5 collision — or, far more likely, a stale digest from
// another ingest path — decide that two unrelated assets are the same.
func (s *DedupeService) CheckDuplicate(ctx context.Context, query ExistingAssetQuery) (*DuplicateMatch, error) {
	if !s.policy.Enabled {
		return &DuplicateMatch{Kind: DuplicateNone}, nil
	}
	if s.store == nil {
		return nil, ErrAssetStoreMissing
	}

	// ── 1. Logical identity (asset ID) ───────────────────────────────
	if id := strings.TrimSpace(query.ID); id != "" {
		rec, err := s.store.FindExisting(ctx, ExistingAssetQuery{ID: id})
		if err != nil {
			return nil, err
		}
		if rec != nil {
			sameContent := sameContentIdentity(rec.ContentHash, query.ContentSHA256)
			s.log.Info("existing logical asset found",
				zap.String("id", rec.ID),
				zap.String("content_hash", query.ContentSHA256),
				zap.Bool("same_content", sameContent))
			return &DuplicateMatch{Kind: DuplicateSameAsset, Record: rec, SameContent: sameContent}, nil
		}
	}

	// ── 2. Content identity (SHA-256 of the bytes) ───────────────────
	if s.policy.CheckByContentHash {
		if sha := strings.ToLower(strings.TrimSpace(query.ContentSHA256)); sha != "" {
			rec, err := s.store.FindExisting(ctx, ExistingAssetQuery{ContentSHA256: sha})
			if err != nil {
				return nil, err
			}
			if rec != nil && rec.ID != strings.TrimSpace(query.ID) {
				// A different logical asset owns these bytes. This is NOT a
				// duplicate: it is shared physical content.
				s.log.Info("content identity already stored under another asset",
					zap.String("existing_asset_id", rec.ID),
					zap.String("content_hash", sha))
				return &DuplicateMatch{Kind: DuplicateSameContent, Record: rec, SameContent: true}, nil
			}
		}
	}

	// ── 3. Locations (weaker: a location is not an identity) ─────────
	if s.policy.CheckByDriveFileID && strings.TrimSpace(query.DriveFileID) != "" {
		rec, err := s.store.FindExisting(ctx, ExistingAssetQuery{DriveFileID: query.DriveFileID})
		if err != nil {
			return nil, err
		}
		if rec != nil {
			s.log.Info("duplicate found by DriveFileID",
				zap.String("id", rec.ID),
				zap.String("drive_file_id", query.DriveFileID))
			// A Drive file id names one physical object, not one logical asset.
			// Treat a match on another ID as shared content so a second logical
			// asset is still created instead of being collapsed into the first.
			kind := DuplicateSameContent
			if id := strings.TrimSpace(query.ID); id != "" && rec.ID == id {
				kind = DuplicateSameAsset
			}
			return &DuplicateMatch{Kind: kind, Record: rec, SameContent: true}, nil
		}
	}

	if s.policy.CheckByFilename && strings.TrimSpace(query.Filename) != "" {
		rec, err := s.store.FindExisting(ctx, ExistingAssetQuery{Filename: query.Filename, Source: query.Source})
		if err != nil {
			return nil, err
		}
		if rec != nil {
			s.log.Info("duplicate found by filename",
				zap.String("id", rec.ID),
				zap.String("filename", query.Filename))
			// A name is not a content identity: report the match but do not
			// claim the bytes are equal.
			return &DuplicateMatch{Kind: DuplicateFilenameMatch, Record: rec, SameContent: false}, nil
		}
	}

	return &DuplicateMatch{Kind: DuplicateNone}, nil
}

// sameContentIdentity reports whether two content addresses describe the same
// bytes. An unknown address on either side is NOT a match: "I could not
// determine the hash" and "the hashes agree" must never be the same answer.
func sameContentIdentity(existing, candidate string) bool {
	a := strings.ToLower(strings.TrimSpace(existing))
	b := strings.ToLower(strings.TrimSpace(candidate))
	if a == "" || b == "" {
		return false
	}
	return a == b
}

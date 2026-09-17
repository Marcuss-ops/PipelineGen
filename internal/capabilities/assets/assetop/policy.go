package assetop

// DuplicatePolicy defines the policy for checking duplicate assets before upload.
//
// THE SEMANTICS OF "DUPLICATE" ARE SPLIT IN TWO, ON PURPOSE:
//
//	LogicalDuplicate (same asset ID)          → skip / conflict
//	ContentDuplicate (same SHA-256, other ID) → reuse storage, KEEP the asset
//
// The distinction exists because identical bytes are not the same asset. Two
// rows may legitimately point at one stored blob (a YouTube clip and a movie
// quote cut from the same MP4): collapsing them would delete a real asset.
type DuplicatePolicy struct {
	// Enabled enables duplicate checking
	Enabled bool
	// CheckByContentHash checks for duplicates by CONTENT IDENTITY: the
	// SHA-256 of the bytes. This is the ONLY hash tier allowed to decide
	// anything. It answers "are these bytes already stored?", never "does this
	// asset already exist?" — that question belongs to the asset ID.
	CheckByContentHash bool
	// CheckByDriveFileID checks for duplicates by Drive file ID
	CheckByDriveFileID bool
	// CheckByFilename checks for duplicates by filename
	CheckByFilename bool
	// SkipIfExists skips the upload when the SAME LOGICAL ASSET (asset ID)
	// already exists with the SAME content identity — an idempotent retry.
	//
	// It deliberately does NOT skip when a DIFFERENT asset ID merely holds the
	// same bytes: that is shared physical content, and the second logical asset
	// must still be created. It also does not skip when the same asset ID now
	// carries DIFFERENT bytes: that is a replacement/version conflict, and
	// skipping it would silently discard the new bytes.
	SkipIfExists bool
	// CheckByHash is the deprecated legacy-MD5 tier toggle.
	//
	// Deprecated: no decision reads it. The field survives only so pre-existing
	// configuration literals still compile. MD5 is compatibility-only
	// (migration, diagnostics) — an MD5 is NOT a content address, so matching on
	// it makes two different payloads look identical. New code MUST use
	// CheckByContentHash.
	CheckByHash bool
}

// UploadPolicy defines the policy for uploading assets to Drive.
type UploadPolicy struct {
	// Enabled enables uploading
	Enabled bool
	// Strategy is the upload strategy (e.g., "standard", "resumable")
	Strategy string
	// MaxRetries is the maximum number of upload retries
	MaxRetries int
	// TimeoutSeconds is the upload timeout in seconds
	TimeoutSeconds int
}

// PersistPolicy defines the policy for persisting assets to databases.
type PersistPolicy struct {
	// SaveToAssetRegistry saves to the media registry (media.db.sqlite)
	SaveToAssetRegistry bool
	// SaveToAssetIndex saves to the asset index (assets.db.sqlite)
	SaveToAssetIndex bool
	// SaveToDomainDB saves to the domain-specific DB (clips.db, voiceover.db, etc.)
	SaveToDomainDB bool
}

// ReconcilePolicy defines the policy for reconciling database records with Drive files.
type ReconcilePolicy struct {
	// Enabled enables reconciliation
	Enabled bool
	// DeleteDBIfDriveMissing deletes DB records if Drive file is missing
	DeleteDBIfDriveMissing bool
	// MarkMissingInsteadOfDelete marks records as missing instead of deleting
	MarkMissingInsteadOfDelete bool
	// SyncDriveFileID syncs Drive file ID from Drive to DB
	SyncDriveFileID bool
}

// DefaultDuplicatePolicy returns the default duplicate policy.
//
// The content tier is the SHA-256 address (CheckByContentHash), not the legacy
// MD5 tier: MD5 no longer decides anything. CheckByHash is left false so the
// default policy states the rule it actually enforces.
func DefaultDuplicatePolicy() DuplicatePolicy {
	return DuplicatePolicy{
		Enabled:            true,
		CheckByContentHash: true,
		CheckByDriveFileID: true,
		CheckByFilename:    false,
		SkipIfExists:       true,
	}
}

// DefaultUploadPolicy returns the default upload policy.
func DefaultUploadPolicy() UploadPolicy {
	return UploadPolicy{
		Enabled:        true,
		Strategy:       "standard",
		MaxRetries:     3,
		TimeoutSeconds: 300,
	}
}

// DefaultPersistPolicy returns the default persist policy.
func DefaultPersistPolicy() PersistPolicy {
	return PersistPolicy{
		SaveToAssetRegistry: true,
		SaveToAssetIndex:    true,
		SaveToDomainDB:      true,
	}
}

// DefaultReconcilePolicy returns the default reconcile policy.
func DefaultReconcilePolicy() ReconcilePolicy {
	return ReconcilePolicy{
		Enabled:                    true,
		DeleteDBIfDriveMissing:     false,
		MarkMissingInsteadOfDelete: true,
		SyncDriveFileID:            true,
	}
}

package assetop

// DuplicatePolicy defines the policy for checking duplicate assets before upload.
type DuplicatePolicy struct {
	// Enabled enables duplicate checking
	Enabled bool
	// CheckByHash checks for duplicates by file hash
	CheckByHash bool
	// CheckByDriveFileID checks for duplicates by Drive file ID
	CheckByDriveFileID bool
	// CheckByFilename checks for duplicates by filename
	CheckByFilename bool
	// SkipIfExists skips upload if a duplicate is found
	SkipIfExists bool
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
	// SaveToAssetRegistry saves to the media registry (velox.db.sqlite)
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
func DefaultDuplicatePolicy() DuplicatePolicy {
	return DuplicatePolicy{
		Enabled:            true,
		CheckByHash:        true,
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

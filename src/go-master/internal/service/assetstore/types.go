package assetstore

import "context"

type ExistencePolicy string

const (
	ExistencePolicyReplace ExistencePolicy = "replace"
	ExistencePolicySkip    ExistencePolicy = "skip"
	ExistencePolicyVerify  ExistencePolicy = "verify"
)

type ExistingAsset struct {
	ID          string
	DriveLink   string
	FileHash    string
	Metadata    string
	LocalPath   string
}

type ChecksumChecker interface {
	GetMD5Checksum(ctx context.Context, driveLink string) (string, error)
}

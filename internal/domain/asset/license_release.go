package asset

import (
	"context"
	"time"
)

// LicenseType classifies the kind of license granted for an asset.
type LicenseType string

const (
	LicenseTypeStandard        LicenseType = "standard"
	LicenseTypeExtended        LicenseType = "extended"
	LicenseTypeRoyaltyFree     LicenseType = "royalty_free"
	LicenseTypeCC0             LicenseType = "cc0"
	LicenseTypeCreativeCommons LicenseType = "creative_commons"
	LicenseTypeCustom          LicenseType = "custom"
)

// ReleaseType indicates what kind of release is recorded for an asset.
type ReleaseType string

const (
	ReleaseTypeModel    ReleaseType = "model"
	ReleaseTypeProperty ReleaseType = "property"
	ReleaseTypeBoth     ReleaseType = "both"
)

// ReleaseStatus tracks the verification state of a release record.
type ReleaseStatus string

const (
	ReleaseStatusPending     ReleaseStatus = "pending"
	ReleaseStatusVerified    ReleaseStatus = "verified"
	ReleaseStatusRejected    ReleaseStatus = "rejected"
	ReleaseStatusNotRequired ReleaseStatus = "not_required"
)

// AssetLicense is the canonical representation of a license attached to
// an asset. It captures provider/account/project scope, the license terms,
// and links to receipt and certificate artifacts.
type AssetLicense struct {
	ID              string      `json:"id"`
	Provider        string      `json:"provider"`
	AccountID       string      `json:"account_id"`
	ProjectID       string      `json:"project_id,omitempty"`
	AssetID         string      `json:"asset_id"`
	LicenseType     LicenseType `json:"license_type"`
	LicenseName     string      `json:"license_name,omitempty"`
	LicenseURL      string      `json:"license_url,omitempty"`
	LicenseTerms    string      `json:"license_terms,omitempty"`
	ReceiptURL      string      `json:"receipt_url,omitempty"`
	ReceiptPath     string      `json:"receipt_path,omitempty"`
	CertificateURL  string      `json:"certificate_url,omitempty"`
	CertificatePath string      `json:"certificate_path,omitempty"`
	ValidFrom       *time.Time  `json:"valid_from,omitempty"`
	ValidUntil      *time.Time  `json:"valid_until,omitempty"`
	CreatedAt       time.Time   `json:"created_at"`
	UpdatedAt       time.Time   `json:"updated_at"`
}

// AssetRelease is the canonical representation of a model/property release
// attached to an asset. It captures release type, certificate/receipt
// artifacts and verification status.
type AssetRelease struct {
	ID                  string        `json:"id"`
	AssetID             string        `json:"asset_id"`
	ReleaseType         ReleaseType   `json:"release_type"`
	ModelReleaseURL     string        `json:"model_release_url,omitempty"`
	ModelReleasePath    string        `json:"model_release_path,omitempty"`
	PropertyReleaseURL  string        `json:"property_release_url,omitempty"`
	PropertyReleasePath string        `json:"property_release_path,omitempty"`
	CertificateURL      string        `json:"certificate_url,omitempty"`
	CertificatePath     string        `json:"certificate_path,omitempty"`
	ReceiptURL          string        `json:"receipt_url,omitempty"`
	ReceiptPath         string        `json:"receipt_path,omitempty"`
	Status              ReleaseStatus `json:"status"`
	VerifiedAt          *time.Time    `json:"verified_at,omitempty"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
}

// LicenseRepository persists and retrieves AssetLicense records.
type LicenseRepository interface {
	Create(ctx context.Context, license *AssetLicense) (string, error)
	Get(ctx context.Context, id string) (*AssetLicense, error)
	ListByAsset(ctx context.Context, assetID string) ([]*AssetLicense, error)
	ListByProject(ctx context.Context, projectID string) ([]*AssetLicense, error)
	Update(ctx context.Context, license *AssetLicense) error
	Delete(ctx context.Context, id string) error
}

// ReleaseRepository persists and retrieves AssetRelease records.
type ReleaseRepository interface {
	Create(ctx context.Context, release *AssetRelease) (string, error)
	Get(ctx context.Context, id string) (*AssetRelease, error)
	ListByAsset(ctx context.Context, assetID string) ([]*AssetRelease, error)
	Update(ctx context.Context, release *AssetRelease) error
	Delete(ctx context.Context, id string) error
}

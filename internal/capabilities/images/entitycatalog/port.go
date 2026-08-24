// Package entitycatalog owns the application contract for the persistent
// PERSON image catalog. It deliberately separates entity identity, remote
// image candidates, and materialization state so the 48-hour provider cache
// is not mistaken for durable catalog data.
package images

import (
	"context"
	"errors"
	"strings"
	"time"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
)

const (
	EntityTypePerson = "PERSON"

	// CandidateStatusFresh is the current healthy URL state. Active is kept
	// as a legacy read-compatible value for rows created before migration 225.
	CandidateStatusFresh   = "fresh"
	CandidateStatusActive  = "active"
	CandidateStatusStale   = "stale"
	CandidateStatusBroken  = "broken"
	CandidateStatusRetired = "retired"

	CandidateSemanticUnknown  = "unknown"
	CandidateSemanticAccepted = "accepted"
	CandidateSemanticRejected = "rejected"

	RefreshStatusNever     = "never"
	RefreshStatusRunning   = "running"
	RefreshStatusSucceeded = "succeeded"
	RefreshStatusFailed    = "failed"

	MaterializationStatusPending      = "pending"
	MaterializationStatusMaterialized = "materialized"
	MaterializationStatusFailed       = "failed"
)

var (
	ErrEntityNotFound    = errors.New("entity image catalog: entity not found")
	ErrCandidateNotFound = errors.New("entity image catalog: candidate not found")
)

// Entity is the durable identity dimension of the catalog. CanonicalEntityID
// is derived from CanonicalName by the shared canonical entity resolver, for
// example "Michael Jordan" -> "person:michael-jordan". This package does not
// maintain a competing slug algorithm.
type Entity struct {
	CanonicalEntityID string
	EntityType        string
	CanonicalName     string
	FirstSeenAt       time.Time
	LastSeenAt        time.Time
	LastRefreshAt     time.Time
	RefreshStatus     string
	LastError         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// Candidate stores one stable remote image URL for one canonical entity.
// Candidate identity is (CanonicalEntityID, Provider, SourceURL).
type Candidate struct {
	ID                int64
	CanonicalEntityID string
	Provider          string
	Rank              int
	SourceURL         string
	ThumbnailURL      string
	Width             int
	Height            int
	Status            string
	SemanticStatus    string
	SemanticScore     float64
	TechnicalScore    float64
	QualityReason     string
	FirstSeenAt       time.Time
	LastSeenAt        time.Time
	UpdatedAt         time.Time
}

// Materialization is intentionally separate from Candidate. It records the
// local/Drive/content-addressed result without mutating the provider URL row.
type Materialization struct {
	CandidateID    int64
	AssetID        string
	LegacyFileMD5  string
	DriveFileID    string
	DriveLink      string
	LocalPath      string
	Status         string
	MaterializedAt time.Time
	LastVerifiedAt time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Repository is the persistence port for the catalog. Implementations must
// be safe for concurrent callers and must keep all SQL/schema details outside
// the application layer.
type Repository interface {
	UpsertEntity(context.Context, Entity) error
	GetEntity(context.Context, string) (*Entity, error)
	SetRefreshState(context.Context, string, string, time.Time, string) error

	UpsertCandidate(context.Context, Candidate) (int64, error)
	SetCandidateStatus(context.Context, int64, string) error
	ListCandidates(context.Context, string, int) ([]Candidate, error)

	UpsertMaterialization(context.Context, Materialization) error
	GetMaterialization(context.Context, int64) (*Materialization, error)
}

// PersonIdentity is the canonical identity pair used by the catalog.
// CanonicalEntityID is owned by capabilities/entities; CanonicalName only
// normalizes surrounding and internal whitespace while preserving spelling.
type PersonIdentity struct {
	CanonicalEntityID string
	CanonicalName     string
}

// CanonicalizePersonName derives the stable PERSON identity through the
// repository-wide canonical resolver. Casing and whitespace variants collapse
// to one ID, while meaningful name tokens remain part of the slug.
func CanonicalizePersonName(name string) (PersonIdentity, error) {
	canonicalName := strings.Join(strings.Fields(strings.TrimSpace(name)), " ")
	canonicalID := capabilityentities.CanonicalEntityID(EntityTypePerson, canonicalName)
	if canonicalID == "" {
		return PersonIdentity{}, errors.New("entity image catalog: PERSON name cannot produce a canonical entity id")
	}
	return PersonIdentity{CanonicalEntityID: canonicalID, CanonicalName: canonicalName}, nil
}

// CanonicalizePersonIdentity derives the ID from the name and, when a caller
// supplied one, verifies that it is the same identity. An empty supplied ID is
// accepted so callers can safely provide only the canonical display name.
func CanonicalizePersonIdentity(name, suppliedID string) (PersonIdentity, error) {
	identity, err := CanonicalizePersonName(name)
	if err != nil {
		return PersonIdentity{}, err
	}
	if supplied := NormalizePersonEntityID(suppliedID); supplied != "" && supplied != identity.CanonicalEntityID {
		return PersonIdentity{}, errors.New("entity image catalog: supplied PERSON entity id does not match canonical name")
	}
	if strings.TrimSpace(suppliedID) != "" && NormalizePersonEntityID(suppliedID) == "" {
		return PersonIdentity{}, errors.New("entity image catalog: supplied PERSON entity id is invalid")
	}
	return identity, nil
}

// NormalizePersonEntityID validates and normalizes an already-derived ID.
// Slug derivation remains exclusively owned by capabilities/entities.
func NormalizePersonEntityID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !strings.HasPrefix(value, "person:") || strings.TrimSpace(strings.TrimPrefix(value, "person:")) == "" {
		return ""
	}
	return value
}

func ValidateEntity(entity Entity) error {
	if strings.ToUpper(strings.TrimSpace(entity.EntityType)) != EntityTypePerson {
		return errors.New("entity image catalog: entity type must be PERSON")
	}
	_, err := CanonicalizePersonIdentity(entity.CanonicalName, entity.CanonicalEntityID)
	return err
}

func ValidateCandidate(candidate Candidate) error {
	if NormalizePersonEntityID(candidate.CanonicalEntityID) == "" {
		return errors.New("entity image catalog: candidate canonical PERSON entity id is required")
	}
	if strings.TrimSpace(candidate.Provider) == "" {
		return errors.New("entity image catalog: candidate provider is required")
	}
	if candidate.Rank < 1 {
		return errors.New("entity image catalog: candidate rank must be positive")
	}
	url := strings.ToLower(strings.TrimSpace(candidate.SourceURL))
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("entity image catalog: candidate source URL must be absolute HTTP(S)")
	}
	if candidate.Width < 0 || candidate.Height < 0 {
		return errors.New("entity image catalog: candidate dimensions cannot be negative")
	}
	if candidate.SemanticScore < 0 || candidate.SemanticScore > 1 {
		return errors.New("entity image catalog: candidate semantic score must be between 0 and 1")
	}
	if candidate.TechnicalScore < 0 || candidate.TechnicalScore > 1 {
		return errors.New("entity image catalog: candidate technical score must be between 0 and 1")
	}
	if err := ValidateSemanticStatus(candidate.SemanticStatus); err != nil {
		return err
	}
	if candidate.Status == "" {
		return nil
	}
	switch candidate.Status {
	case CandidateStatusFresh, CandidateStatusActive, CandidateStatusStale, CandidateStatusBroken, CandidateStatusRetired:
		return nil
	default:
		return errors.New("entity image catalog: invalid candidate status")
	}
}

func IsUsableCandidateStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", CandidateStatusFresh, CandidateStatusActive, CandidateStatusStale:
		return true
	default:
		return false
	}
}

// IsUsableCandidateQuality fail-closes legacy/unknown rows and candidates
// explicitly rejected by the semantic or technical gate.
func IsUsableCandidateQuality(candidate Candidate) bool {
	semanticStatus := strings.ToLower(strings.TrimSpace(candidate.SemanticStatus))
	// Unknown rows predate the semantic gate and cannot prove that the URL
	// depicts this entity. They stay durable for audit/refresh, but are not
	// eligible for discovery reuse until a provider refresh re-certifies them.
	if semanticStatus != CandidateSemanticAccepted {
		return false
	}
	if candidate.SemanticScore > 0 && candidate.SemanticScore < 0.60 {
		return false
	}
	if candidate.TechnicalScore > 0 && candidate.TechnicalScore < 0.50 {
		return false
	}
	if candidate.Width > 0 && candidate.Height > 0 {
		longSide := candidate.Width
		if candidate.Height > longSide {
			longSide = candidate.Height
		}
		if longSide < 800 || candidate.Width*candidate.Height < 400000 {
			return false
		}
	}
	return true
}

func ValidateSemanticStatus(status string) error {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", CandidateSemanticUnknown, CandidateSemanticAccepted, CandidateSemanticRejected:
		return nil
	default:
		return errors.New("entity image catalog: invalid semantic status")
	}
}

func ValidateCandidateStatus(status string) error {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case CandidateStatusFresh, CandidateStatusActive, CandidateStatusStale, CandidateStatusBroken, CandidateStatusRetired:
		return nil
	default:
		return errors.New("entity image catalog: invalid candidate status")
	}
}

func ValidateMaterialization(materialization Materialization) error {
	if materialization.CandidateID < 1 {
		return errors.New("entity image catalog: materialization candidate id is required")
	}
	if materialization.Status == "" {
		return nil
	}
	switch materialization.Status {
	case MaterializationStatusPending, MaterializationStatusMaterialized, MaterializationStatusFailed:
		return nil
	default:
		return errors.New("entity image catalog: invalid materialization status")
	}
}

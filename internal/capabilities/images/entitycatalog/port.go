// Package entitycatalog owns the application contract for the persistent
// PERSON image catalog. It deliberately separates entity identity, remote
// image candidates, and materialization state so the 48-hour provider cache
// is not mistaken for durable catalog data.
package entitycatalog

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	EntityTypePerson = "PERSON"

	// CatalogImageMinLongSide and CatalogImageMinPixels are the durable
	// catalog quality floor. The acquisition gate may accept smaller bytes for
	// technical inspection, but only these dimensions can enter the reusable
	// identity pool.
	CatalogImageMinLongSide = 800
	CatalogImageMinPixels   = 400000

	// CandidateStatusFresh is the current healthy URL state. Active is kept
	// as a legacy read-compatible value for rows created before migration 225.
	CandidateStatusFresh   = "fresh"
	CandidateStatusActive  = "active"
	CandidateStatusStale   = "stale"
	CandidateStatusBroken  = "broken"
	CandidateStatusRetired = "retired"
	// CandidateStatusUnknown is the classification of a stored status the
	// policy does not recognize. It is never a persisted state and never
	// usable for discovery.
	CandidateStatusUnknown = "unknown"

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

	// CandidateVerifierVersion is the visual policy that produced an
	// accepted/rejected entity-image verdict. Bump this whenever the model, prompt, or
	// scoring thresholds change: a verdict from another version is evidence
	// about different pixels under different rules, so it is not reusable.
	CandidateVerifierVersion = "entity-image-visual/v3"
	// CandidateVerifierVersionPinned is the offline media-manifest verifier.
	// It is trusted for its exact content hash, but it is not a VLM verdict.
	CandidateVerifierVersionPinned = "media-manifest.v1/pinned"
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
	// LegacyTimestamp is true when at least one persisted timestamp was not
	// parseable. Callers must not treat the zero time as "never seen".
	LegacyTimestamp bool
}

// Candidate stores one stable remote image URL for one canonical entity.
// Candidate identity is (CanonicalEntityID, Provider, SourceURL).
type Candidate struct {
	ID                int64
	CanonicalEntityID string
	Provider          string
	Rank              int
	SourceURL         string
	SourcePageURL     string
	ThumbnailURL      string
	Width             int
	Height            int
	Status            string
	SemanticStatus    string
	SemanticScore     float64
	TechnicalScore    float64
	QualityReason     string
	// ContentSHA256 is the digest of the bytes this URL resolved to. Empty
	// means the candidate was only discovered; a changed non-empty digest
	// invalidates the visual verdict attached to the old bytes.
	ContentSHA256 string
	// VerifierVersion names the policy that produced SemanticStatus. Empty
	// is a metadata-only discovery row, never a usable visual verdict.
	VerifierVersion string
	FirstSeenAt     time.Time
	LastSeenAt      time.Time
	UpdatedAt       time.Time
	// LegacyTimestamp marks an unparseable persisted timestamp.
	LegacyTimestamp bool
}

// Materialization is intentionally separate from Candidate. It records which
// provider suggestion has been turned into a canonical asset, without mutating
// the provider URL row.
//
// It holds NO local path. A path is runtime/cache state — it stops being true
// when a tmpfs is swept, a container restarts, or a different host serves the
// request — so it can never be evidence that an asset is durable. Keeping it
// here is what made a media row able to disagree with itself (the reported
// failure: a sha256 that named one file while local_path named another). Paths
// now exist only for the seconds a process needs them, and the two owners are
// the canonical materializer (internal/platform/assets/materializer) for bytes
// and the asset-location registry for locations.
type Materialization struct {
	CandidateID    int64
	AssetID        string
	ContentSHA256  string
	DriveFileID    string
	DriveLink      string
	Status         string
	MaterializedAt time.Time
	LastVerifiedAt time.Time
	LastError      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	// LegacyTimestamp marks an unparseable persisted timestamp.
	LegacyTimestamp bool
}

// MaterializationAttempt is one append-only observation of a materialization
// write. The current materialization row is the latest state; attempts preserve
// the history that produced it, including failures.
type MaterializationAttempt struct {
	ID            int64
	CandidateID   int64
	AssetID       string
	ContentSHA256 string
	Status        string
	LastError     string
	AttemptedAt   time.Time
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

// CandidatePageReader is the optional paged scan. The bounded ListCandidates
// remains for compatibility; production readers page until the requested pool
// is satisfied instead of hiding a valid row after the first 100.
type CandidatePageReader interface {
	ListCandidatesPage(context.Context, string, int, int) ([]Candidate, error)
}

// MaterializationBatchReader removes the N+1 lookup from catalog pool reads.
type MaterializationBatchReader interface {
	GetMaterializations(context.Context, []int64) (map[int64]*Materialization, error)
}

// PersonIdentity is the canonical identity pair used by the catalog.
// CanonicalEntityID is owned by capabilities/entities; CanonicalName only
// normalizes surrounding and internal whitespace while preserving spelling.
type PersonIdentity struct {
	CanonicalEntityID string
	CanonicalName     string
	// Disambiguator is set when the identity was explicitly minted for a
	// homonym (for example "actor" for one of several John Smiths).
	Disambiguator string
	// Unresolved is true when the surface is known to belong to more than one
	// identity and no owner was selected. Such an identity is not selectable.
	Unresolved bool
	// Candidates lists the possible owners when Unresolved is true.
	Candidates []string
}

// CanonicalizePersonName derives the stable PERSON identity through the
// repository-wide canonical resolver. Casing and whitespace variants collapse
// to one ID, while meaningful name tokens remain part of the slug.
func CanonicalizePersonName(name string) (PersonIdentity, error) {
	return CanonicalizePersonNameWithAliases(name, nil)
}

// CanonicalizePersonIdentity derives the ID from the name and, when a caller
// supplied one, verifies that it is the same identity. An empty supplied ID is
// accepted so callers can safely provide only the canonical display name.
func CanonicalizePersonIdentity(name, suppliedID string) (PersonIdentity, error) {
	return CanonicalizePersonIdentityWithAliases(name, suppliedID, nil)
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

// NormalizeSourceURL is the identity form used by the candidate unique key.
// Scheme/host case, default ports, fragments and common tracking parameters
// are retrieval noise: the same image must not mint two catalog rows because
// a provider appended utm_source or changed the host casing.
func NormalizeSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	host := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		parsed.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		parsed.Host = "[" + host + "]"
	} else {
		parsed.Host = host
	}
	parsed.Fragment = ""
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		if strings.HasPrefix(lower, "utm_") || lower == "fbclid" || lower == "gclid" || lower == "mc_cid" || lower == "mc_eid" || lower == "igshid" {
			query.Del(key)
		}
	}
	parsed.RawQuery = query.Encode()
	return parsed.String()
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
	if page := strings.ToLower(strings.TrimSpace(candidate.SourcePageURL)); page != "" && !strings.HasPrefix(page, "http://") && !strings.HasPrefix(page, "https://") {
		return errors.New("entity image catalog: candidate source page URL must be absolute HTTP(S)")
	}
	if hash := strings.ToLower(strings.TrimSpace(candidate.ContentSHA256)); hash != "" {
		if !IsValidContentSHA256(hash) {
			return errors.New("entity image catalog: candidate content_sha256 must be 64 hex characters")
		}
	}
	if len(strings.TrimSpace(candidate.VerifierVersion)) > 128 {
		return errors.New("entity image catalog: candidate verifier version is too long")
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
	// An empty status is an uncertified legacy row, not a usable candidate.
	case CandidateStatusFresh, CandidateStatusActive, CandidateStatusStale:
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
	if !IsCurrentCandidateVerifierVersion(candidate.VerifierVersion) {
		return false
	}
	// The accepted bit is reusable only when its provenance records a supported
	// identity decision: pixel verification, manual review, trusted-encyclopedia
	// verification, or an offline content-addressed pin. Historical catalog
	// writes that used only a query/name match plus dimensions remain audit-only.
	qualityReason := strings.ToLower(strings.TrimSpace(candidate.QualityReason))
	if !strings.Contains(qualityReason, "ollama visual identity verification confirmed") &&
		!strings.Contains(qualityReason, "manual visual identity review confirmed") &&
		!strings.Contains(qualityReason, "deterministic identity verification confirmed (trusted source)") &&
		!strings.Contains(qualityReason, "pinned by media_manifest.v1: offline sha256 verify") {
		return false
	}
	if candidate.SemanticScore > 0 && candidate.SemanticScore < 0.60 {
		return false
	}
	if candidate.TechnicalScore > 0 && candidate.TechnicalScore < 0.50 {
		return false
	}
	if candidate.Width > 0 && candidate.Height > 0 && !ImageMeetsCatalogMinimum(candidate.Width, candidate.Height) {
		return false
	}
	return true
}

// ImageMeetsCatalogMinimum is the one dimension predicate shared by catalog
// admission, discovery filtering and recertification validation.
func ImageMeetsCatalogMinimum(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	longSide := width
	if height > longSide {
		longSide = height
	}
	return longSide >= CatalogImageMinLongSide && width*height >= CatalogImageMinPixels
}

// IsValidContentSHA256 reports whether value is a canonical 64-hex content
// address. Callers use it to distinguish "no content identity yet" from a
// digest about actual bytes.
func IsValidContentSHA256(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// IsCurrentCandidateVerifierVersion reports whether a verdict was produced by
// a policy this build still trusts. Pinned media-manifest rows are trusted by
// their exact content hash; every other verdict must name the current visual
// policy. Legacy/empty versions fail closed.
func IsCurrentCandidateVerifierVersion(version string) bool {
	switch strings.TrimSpace(version) {
	case CandidateVerifierVersion, CandidateVerifierVersionPinned:
		return true
	default:
		return false
	}
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
	// An empty status is an uncertified legacy row, exactly like
	// ValidateCandidate and IsUsableCandidateStatus treat it: durable for
	// audit/refresh, never usable for discovery reuse.
	case "", CandidateStatusFresh, CandidateStatusActive, CandidateStatusStale, CandidateStatusBroken, CandidateStatusRetired:
		return nil
	default:
		return errors.New("entity image catalog: invalid candidate status")
	}
}

func ValidateMaterialization(materialization Materialization) error {
	if materialization.CandidateID < 1 {
		return errors.New("entity image catalog: materialization candidate id is required")
	}
	if hash := strings.TrimSpace(materialization.ContentSHA256); hash != "" && !IsValidContentSHA256(hash) {
		return errors.New("entity image catalog: materialization content_sha256 must be 64 hex characters")
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

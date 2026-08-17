package research

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"
)

const (
	// EvidencePackVersion identifies the JSON schema and validation semantics.
	EvidencePackVersion = "boxing-evidence-pack-v1"
	// TenBoxerPackCount is the required fan-out width for the boxing ranking
	// certification. Replacement candidates still occupy one of these slots.
	TenBoxerPackCount = 10
	// MinimumCredibleSources is the minimum distinct source count per pack.
	MinimumCredibleSources = 2
)

var ErrInvalidEvidencePack = errors.New("research: invalid evidence pack")

// EntityType identifies the researched subject kind. The boxing contract is
// deliberately closed to people so a non-boxer cannot enter the ranking.
type EntityType string

const EntityPerson EntityType = "PERSON"

// SourceCredibility records the evidence quality tier used by ranking. The
// tier is metadata, not a substitute for preserving the original source URL.
type SourceCredibility string

const (
	CredibilityPrimary         SourceCredibility = "primary"
	CredibilityMajorPublisher  SourceCredibility = "major_publisher"
	CredibilitySpecialistPress SourceCredibility = "specialist_press"
	CredibilitySecondary       SourceCredibility = "secondary"
)

// EvidenceSource is one retrievable source retained with the pack. RetrievedAt
// anchors the research snapshot; PublishedAt may be empty when the publisher
// does not expose a publication date.
type EvidenceSource struct {
	ID          string            `json:"id"`
	URL         string            `json:"url"`
	Title       string            `json:"title"`
	Publisher   string            `json:"publisher"`
	SourceType  string            `json:"source_type"`
	Credibility SourceCredibility `json:"credibility"`
	PublishedAt string            `json:"published_at,omitempty"`
	RetrievedAt string            `json:"retrieved_at"`
	ArchiveURL  string            `json:"archive_url,omitempty"`
	AccessNotes string            `json:"access_notes,omitempty"`
}

// FactCategory classifies non-financial claims so the writer can distinguish
// boxing context from money claims and preserve the relevant citation.
type FactCategory string

const (
	FactIdentity       FactCategory = "identity"
	FactAccomplishment FactCategory = "accomplishment"
	FactCareerContext  FactCategory = "career_context"
	FactRankingContext FactCategory = "ranking_context"
	FactOther          FactCategory = "other"
)

// EvidenceFact is a factual claim grounded in one or more source IDs.
type EvidenceFact struct {
	ID         string       `json:"id"`
	Claim      string       `json:"claim"`
	Category   FactCategory `json:"category"`
	SourceIDs  []string     `json:"source_ids"`
	Confidence float64      `json:"confidence"`
	Notes      string       `json:"notes,omitempty"`
}

// MoneyKind distinguishes reported facts from estimates and ranges. An
// undisclosed value retains the publisher's wording without fabricating a
// number.
type MoneyKind string

const (
	MoneyExact       MoneyKind = "exact"
	MoneyEstimate    MoneyKind = "estimate"
	MoneyRange       MoneyKind = "range"
	MoneyUndisclosed MoneyKind = "undisclosed"
)

// MoneyValue preserves the reported wording and, when available, a structured
// amount. USDValue is an optional normalized comparison value and must never
// replace the original reported amount.
type MoneyValue struct {
	Kind         MoneyKind `json:"kind"`
	ReportedText string    `json:"reported_text"`
	Currency     string    `json:"currency,omitempty"`
	Amount       *float64  `json:"amount,omitempty"`
	Low          *float64  `json:"low,omitempty"`
	High         *float64  `json:"high,omitempty"`
	USDValue     *float64  `json:"usd_value,omitempty"`
	USDMethod    string    `json:"usd_method,omitempty"`
}

// FinancialEvidence is used for career earnings, fight paydays, and current
// wealth estimates. A record can remain qualitative when no defensible number
// is disclosed.
type FinancialEvidence struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	Context    string     `json:"context,omitempty"`
	Period     string     `json:"period,omitempty"`
	Value      MoneyValue `json:"value"`
	SourceIDs  []string   `json:"source_ids"`
	Confidence float64    `json:"confidence"`
	Notes      string     `json:"notes,omitempty"`
}

// BusinessEvidence describes ownership, participation, ventures, and exits
// outside the ring. Proceeds, revenue, or valuation are optional and remain
// separately sourced from the business description.
type BusinessEvidence struct {
	ID               string      `json:"id"`
	Name             string      `json:"name"`
	Role             string      `json:"role,omitempty"`
	Description      string      `json:"description"`
	Ownership        string      `json:"ownership,omitempty"`
	FinancialOutcome *MoneyValue `json:"financial_outcome,omitempty"`
	SourceIDs        []string    `json:"source_ids"`
	Confidence       float64     `json:"confidence"`
	Notes            string      `json:"notes,omitempty"`
}

// EndorsementEvidence records a commercial endorsement or sponsorship. Its
// compensation is optional because undisclosed deals must not be guessed.
type EndorsementEvidence struct {
	ID           string      `json:"id"`
	Brand        string      `json:"brand"`
	Description  string      `json:"description"`
	Compensation *MoneyValue `json:"compensation,omitempty"`
	SourceIDs    []string    `json:"source_ids"`
	Confidence   float64     `json:"confidence"`
	Notes        string      `json:"notes,omitempty"`
}

// FinancialEvent captures losses, bankruptcy, debt, settlements, or other
// events that materially change the interpretation of gross earnings.
type FinancialEvent struct {
	ID          string      `json:"id"`
	Kind        string      `json:"kind"`
	Description string      `json:"description"`
	Date        string      `json:"date,omitempty"`
	Impact      *MoneyValue `json:"impact,omitempty"`
	SourceIDs   []string    `json:"source_ids"`
	Confidence  float64     `json:"confidence"`
	Notes       string      `json:"notes,omitempty"`
}

// EvidencePack is the complete source-backed research handoff for one boxer.
// Every quantitative or qualitative claim points to EvidenceSource IDs, so
// ranking and script generation never have to infer provenance from prose.
type EvidencePack struct {
	Version                string                `json:"version"`
	Subject                string                `json:"subject"`
	EntityType             EntityType            `json:"entity_type"`
	CandidateOrdinal       int                   `json:"candidate_ordinal,omitempty"`
	Sources                []EvidenceSource      `json:"sources"`
	Facts                  []EvidenceFact        `json:"facts"`
	CareerEarnings         []FinancialEvidence   `json:"career_earnings"`
	FightPaydays           []FinancialEvidence   `json:"fight_paydays"`
	CurrentWealthEstimates []FinancialEvidence   `json:"current_wealth_estimates,omitempty"`
	Businesses             []BusinessEvidence    `json:"businesses"`
	Endorsements           []EndorsementEvidence `json:"endorsements"`
	FinancialEvents        []FinancialEvent      `json:"financial_events"`
}

// EvidencePackSet is the fan-out result consumed by the post-research ranking
// resolver. Validate requires exactly ten independently researched subjects.
type EvidencePackSet struct {
	Version string         `json:"version"`
	Topic   string         `json:"topic"`
	Packs   []EvidencePack `json:"packs"`
}

// Validate checks one pack fail-closed: malformed sources, unbounded
// confidence, dangling citations, and unsupported money records are rejected.
func (p EvidencePack) Validate() error {
	if p.Version != EvidencePackVersion {
		return invalidPack("unsupported version %q", p.Version)
	}
	if strings.TrimSpace(p.Subject) == "" {
		return invalidPack("subject is required")
	}
	if p.EntityType != EntityPerson {
		return invalidPack("entity_type must be %q", EntityPerson)
	}
	if len(p.Facts) == 0 {
		return invalidPack("at least one fact is required")
	}

	sources := make(map[string]EvidenceSource, len(p.Sources))
	credible := 0
	for _, source := range p.Sources {
		if err := source.validate(); err != nil {
			return err
		}
		if _, exists := sources[source.ID]; exists {
			return invalidPack("duplicate source id %q", source.ID)
		}
		sources[source.ID] = source
		if source.isCredible() {
			credible++
		}
	}
	if credible < MinimumCredibleSources {
		return invalidPack("requires at least %d credible sources, got %d", MinimumCredibleSources, credible)
	}

	for _, fact := range p.Facts {
		if err := validateClaim(fact.ID, fact.Claim, fact.SourceIDs, fact.Confidence, sources); err != nil {
			return fmt.Errorf("%w: fact %q: %v", ErrInvalidEvidencePack, fact.ID, err)
		}
	}
	for _, item := range append(append(append([]FinancialEvidence{}, p.CareerEarnings...), p.FightPaydays...), p.CurrentWealthEstimates...) {
		if err := validateFinancialEvidence(item, sources); err != nil {
			return err
		}
	}
	for _, item := range p.Businesses {
		if err := validateClaim(item.ID, item.Description, item.SourceIDs, item.Confidence, sources); err != nil {
			return fmt.Errorf("%w: business %q: %v", ErrInvalidEvidencePack, item.ID, err)
		}
		if item.FinancialOutcome != nil {
			if err := item.FinancialOutcome.Validate(); err != nil {
				return fmt.Errorf("%w: business %q outcome: %v", ErrInvalidEvidencePack, item.ID, err)
			}
		}
	}
	for _, item := range p.Endorsements {
		if err := validateClaim(item.ID, item.Description, item.SourceIDs, item.Confidence, sources); err != nil {
			return fmt.Errorf("%w: endorsement %q: %v", ErrInvalidEvidencePack, item.ID, err)
		}
		if item.Compensation != nil {
			if err := item.Compensation.Validate(); err != nil {
				return fmt.Errorf("%w: endorsement %q compensation: %v", ErrInvalidEvidencePack, item.ID, err)
			}
		}
	}
	for _, item := range p.FinancialEvents {
		if err := validateClaim(item.ID, item.Description, item.SourceIDs, item.Confidence, sources); err != nil {
			return fmt.Errorf("%w: financial event %q: %v", ErrInvalidEvidencePack, item.ID, err)
		}
		if item.Impact != nil {
			if err := item.Impact.Validate(); err != nil {
				return fmt.Errorf("%w: financial event %q impact: %v", ErrInvalidEvidencePack, item.ID, err)
			}
		}
	}
	return nil
}

func validateFinancialEvidence(item FinancialEvidence, sources map[string]EvidenceSource) error {
	if err := validateClaim(item.ID, item.Label, item.SourceIDs, item.Confidence, sources); err != nil {
		return fmt.Errorf("%w: financial evidence %q: %v", ErrInvalidEvidencePack, item.ID, err)
	}
	if err := item.Value.Validate(); err != nil {
		return fmt.Errorf("%w: financial evidence %q value: %v", ErrInvalidEvidencePack, item.ID, err)
	}
	return nil
}

func validateClaim(id, text string, sourceIDs []string, confidence float64, sources map[string]EvidenceSource) error {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(text) == "" {
		return errors.New("id and claim text are required")
	}
	if err := validateConfidence(confidence); err != nil {
		return err
	}
	if len(sourceIDs) == 0 {
		return errors.New("at least one source citation is required")
	}
	seen := make(map[string]struct{}, len(sourceIDs))
	for _, id := range sourceIDs {
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("duplicate source citation %q", id)
		}
		seen[id] = struct{}{}
		if _, ok := sources[id]; !ok {
			return fmt.Errorf("unknown source citation %q", id)
		}
	}
	return nil
}

func (s EvidenceSource) validate() error {
	if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Title) == "" || strings.TrimSpace(s.Publisher) == "" || strings.TrimSpace(s.SourceType) == "" {
		return invalidPack("source requires id, title, publisher, and source_type")
	}
	if !validHTTPURL(s.URL) {
		return invalidPack("source %q has invalid URL", s.ID)
	}
	if !s.isCredible() {
		return invalidPack("source %q has unsupported credibility %q", s.ID, s.Credibility)
	}
	if !validTimestamp(s.RetrievedAt) {
		return invalidPack("source %q has invalid retrieved_at", s.ID)
	}
	if s.PublishedAt != "" && !validTimestamp(s.PublishedAt) {
		return invalidPack("source %q has invalid published_at", s.ID)
	}
	return nil
}

func (s EvidenceSource) isCredible() bool {
	switch s.Credibility {
	case CredibilityPrimary, CredibilityMajorPublisher, CredibilitySpecialistPress:
		return true
	default:
		return false
	}
}

// Validate checks a monetary value without converting a disputed estimate into
// an exact number.
func (m MoneyValue) Validate() error {
	if strings.TrimSpace(m.ReportedText) == "" {
		return errors.New("reported_text is required")
	}
	if m.Currency == "" && (m.Amount != nil || m.Low != nil || m.High != nil || m.USDValue != nil) {
		return errors.New("currency is required for numeric values")
	}
	if m.Amount != nil && !validMoney(*m.Amount) {
		return errors.New("amount must be finite and non-negative")
	}
	if m.Low != nil && !validMoney(*m.Low) || m.High != nil && !validMoney(*m.High) {
		return errors.New("range bounds must be finite and non-negative")
	}
	if m.Low != nil && m.High != nil && *m.High < *m.Low {
		return errors.New("range high cannot be below low")
	}
	if m.USDValue != nil && !validMoney(*m.USDValue) {
		return errors.New("usd_value must be finite and non-negative")
	}
	switch m.Kind {
	case MoneyExact, MoneyEstimate:
		if m.Amount == nil || m.Low != nil || m.High != nil {
			return fmt.Errorf("%s values require amount only", m.Kind)
		}
	case MoneyRange:
		if m.Low == nil || m.High == nil || m.Amount != nil {
			return errors.New("range values require low and high only")
		}
	case MoneyUndisclosed:
		if m.Amount != nil || m.Low != nil || m.High != nil || m.USDValue != nil {
			return errors.New("undisclosed values cannot contain numeric amounts")
		}
	default:
		return fmt.Errorf("unsupported money kind %q", m.Kind)
	}
	return nil
}

// Validate checks that exactly ten complete packs are available for ranking.
func (s EvidencePackSet) Validate() error {
	if s.Version != EvidencePackVersion {
		return invalidPack("unsupported set version %q", s.Version)
	}
	if strings.TrimSpace(s.Topic) == "" {
		return invalidPack("topic is required")
	}
	if len(s.Packs) != TenBoxerPackCount {
		return invalidPack("requires %d packs, got %d", TenBoxerPackCount, len(s.Packs))
	}
	seen := make(map[string]struct{}, len(s.Packs))
	for _, pack := range s.Packs {
		if err := pack.Validate(); err != nil {
			return err
		}
		key := strings.ToLower(strings.TrimSpace(pack.Subject))
		if _, exists := seen[key]; exists {
			return invalidPack("duplicate subject %q", pack.Subject)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateConfidence(confidence float64) error {
	if math.IsNaN(confidence) || math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return errors.New("confidence must be finite and within [0,1]")
	}
	return nil
}

func validMoney(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func validTimestamp(raw string) bool {
	_, err := time.Parse(time.RFC3339, raw)
	return err == nil
}

func invalidPack(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidEvidencePack, fmt.Sprintf(format, args...))
}

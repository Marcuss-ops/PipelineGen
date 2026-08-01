// internal/application/mediamemory/ports_types.go — port-level data
// shapes (envelopes, options, verdicts) that travel across the port
// surface. Extracted from ports.go; no behavior change.
package mediamemory

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// QueryCacheEntry is the persisted shape of one cache hit. Kept here
// (not in types.go) because it is a port-level envelope, not a
// canonical business entity.
type QueryCacheEntry struct {
	ID                string
	PhraseFingerprint string
	Language          string
	RequestJSON       string
	ResultJSON        string
	ProviderStateJSON string
	HitCount          int
	ExpiresAt         *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// SearchFanOutQuery is the narrow input shape consumed by MediaMemory.
// The production adapter translates it into search.Query.
type SearchFanOutQuery struct {
	Text         string
	Language     string
	MediaTypes   []string
	Sources      []string
	Limit        int
	SearchPolicy media.ResolutionSearchPolicy
}

// SearchFanOutResult is the narrow output shape consumed by
// MediaMemory. The production adapter translates search.Result into
// this shape.
type SearchFanOutResult struct {
	Candidates    []MediaCandidate
	Partial       bool
	BackendNames  []string
	BackendErrors map[string]string
}

// MaterializeOptions configures the acquire call.
type MaterializeOptions struct {
	// TargetSlot hints the stockpipeline which segment quality to
	// prefer ("primary_video" → higher bitrate, "secondary_image"
	// → thumbnail-grade, ...).
	TargetSlot SlotKind
	// HotCache controls whether the bytes are staged locally
	// (Hot) or only stored on Drive (Warm). Cold is the default.
	HotCache bool
	// MaxDurationMs caps the segment download window.
	MaxDurationMs int64
	// ProjectID scopes the materialization for rights enforcement.
	ProjectID string
}

// RightsDecision is the verdict produced by the rights port.
// godlike/07 NO-FAKE-AVAILABILITY: Verdict == AllowConditional
// requires non-empty Conditions, otherwise the ranker MUST apply
// full rights_penalty.
type RightsDecision struct {
	Verdict    RightsVerdict
	Reason     string
	Conditions []string
}

// RightsVerdict enum (godlike/06 closed set).
type RightsVerdict string

const (
	RightsVerdictAllow            RightsVerdict = "allow"
	RightsVerdictAllowConditional RightsVerdict = "allow_conditional"
	RightsVerdictDeny             RightsVerdict = "deny"
)

// IsKnownRightsVerdict reports whether v is in the canonical closed set.
// godlike/06 SSOT: predicate lives NEXT TO its enum (this file), keeping
// every RightsVerdict surface (constant + predicate + future typed-
// sentinel) co-located for grep + drift-pinning.
func IsKnownRightsVerdict(v RightsVerdict) bool {
	switch v {
	case RightsVerdictAllow, RightsVerdictAllowConditional, RightsVerdictDeny:
		return true
	default:
		return false
	}
}

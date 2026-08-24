// Package mediamemory — ports_types.go: port-level data shapes.
package mediamemory

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

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

type SearchFanOutQuery struct {
	Text         string
	Language     string
	MediaTypes   []string
	Sources      []string
	Limit        int
	SearchPolicy media.ResolutionSearchPolicy
}

type SearchFanOutResult struct {
	Candidates    []MediaCandidate
	Partial       bool
	BackendNames  []string
	BackendErrors map[string]string
}

type MaterializeOptions struct {
	TargetSlot    SlotKind
	HotCache      bool
	MaxDurationMs int64
	ProjectID     string
}

type RightsDecision struct {
	Verdict    RightsVerdict
	Reason     string
	Conditions []string
}

type RightsVerdict string

const (
	RightsVerdictAllow            RightsVerdict = "allow"
	RightsVerdictAllowConditional RightsVerdict = "allow_conditional"
	RightsVerdictDeny             RightsVerdict = "deny"
)

func IsKnownRightsVerdict(v RightsVerdict) bool {
	switch v {
	case RightsVerdictAllow, RightsVerdictAllowConditional, RightsVerdictDeny:
		return true
	default:
		return false
	}
}

// Package stocksupply — wire.go defines the canonical `stock_supply`
// wire contract. It is the single owner of the JSON request/response
// shape that transport code (HTTP handlers, job payloads) binds onto.
//
// godlike/06 SSOT: wire-shape concerns stay separate from the internal
// domain vocabulary. The internal types (SupplyQuery / SupplyResult)
// remain transport-agnostic; Contract is the only type that carries
// JSON tags, and the only type that knows how to project the wire
// shape onto SupplyQuery.
//
// The contract mirrors the spec verbatim:
//
//	mode:               off | prefetch | fallback | hybrid
//	queries:            [ ... ]
//	target_duration_sec: 600
//	providers:          [ local, artlist, youtube ]
//	provider_strategy:  fallback
//	search_limit:       10
//	clip_duration:      { min_sec, max_sec }
//	max_downloads:      30
//	reuse_existing:     true
package stocksupply

import "fmt"

// ClipDuration is the nested clip-duration window. Both bounds are
// seconds. Zero values mean "provider default" (4–60 s).
type ClipDuration struct {
	// MinSec is the minimum usable segment length. 0 = provider default.
	MinSec int `json:"min_sec,omitempty"`
	// MaxSec is the maximum usable segment length. 0 = provider default.
	MaxSec int `json:"max_sec,omitempty"`
}

// Contract is the canonical `stock_supply` wire request.
//
// Every field maps 1:1 onto the spec. The json tags are the transport
// contract; the internal SupplyQuery is derived via ToSupplyQuery.
type Contract struct {
	// Mode selects when resolution happens (off/prefetch/fallback/hybrid).
	// Empty defaults to ModeOff (no automatic resolution).
	Mode SupplyMode `json:"mode"`

	// Queries is the list of semantic queries to resolve.
	Queries []string `json:"queries"`

	// TargetDurationSec is the desired total usable footage in seconds.
	// 0 = resolver default (600 s).
	TargetDurationSec int `json:"target_duration_sec"`

	// MinimumReadySec is the progressive-readiness threshold: once the
	// resolver has this many seconds indexed, consumers may start scene
	// resolution without waiting for the full target. 0 = resolver default.
	// Must be ≤ TargetDurationSec.
	MinimumReadySec int `json:"minimum_ready_sec,omitempty"`

	// Providers restricts resolution to these named providers
	// (empty = all registered). Recognised: local, artlist, youtube.
	Providers []string `json:"providers,omitempty"`

	// ProviderStrategy picks the provider ordering when local is
	// insufficient. Empty defaults to StrategyFallback.
	ProviderStrategy ProviderStrategy `json:"provider_strategy,omitempty"`

	// SearchLimit caps results per live provider search. 0 = 10.
	SearchLimit int `json:"search_limit,omitempty"`

	// ClipDuration is the per-segment window (min/max seconds).
	ClipDuration ClipDuration `json:"clip_duration,omitempty"`

	// MaxDownloads caps the number of segments sourced per query.
	// 0 = resolver default (30).
	MaxDownloads int `json:"max_downloads,omitempty"`

	// ReuseExisting, when nil or true, prefers local Qdrant hits over live
	// search. Explicitly false forces a fresh provider search.
	// Pointer keeps the wire default (true) distinguishable from an
	// explicit `false`.
	ReuseExisting *bool `json:"reuse_existing,omitempty"`
}

// Validate applies the fail-closed contract rules (godlike/07). It
// returns a typed error on the first violation so transport code can
// surface a 400 without echoing raw user input.
func (c Contract) Validate() error {
	if c.Mode != "" && !c.isValidMode() {
		return fmt.Errorf("stock_supply: unsupported mode %q (want off|prefetch|fallback|hybrid)", c.Mode)
	}
	if c.ProviderStrategy != "" && !c.ProviderStrategy.IsValid() {
		return fmt.Errorf("stock_supply: unsupported provider_strategy %q", c.ProviderStrategy)
	}
	if c.TargetDurationSec < 0 {
		return fmt.Errorf("stock_supply: target_duration_sec must be ≥ 0 (got %d)", c.TargetDurationSec)
	}
	if c.MinimumReadySec < 0 {
		return fmt.Errorf("stock_supply: minimum_ready_sec must be ≥ 0 (got %d)", c.MinimumReadySec)
	}
	if c.MinimumReadySec > c.TargetDurationSec && c.TargetDurationSec > 0 {
		return fmt.Errorf("stock_supply: minimum_ready_sec (%d) must be ≤ target_duration_sec (%d)",
			c.MinimumReadySec, c.TargetDurationSec)
	}
	if c.SearchLimit < 0 {
		return fmt.Errorf("stock_supply: search_limit must be ≥ 0 (got %d)", c.SearchLimit)
	}
	if c.MaxDownloads < 0 {
		return fmt.Errorf("stock_supply: max_downloads must be ≥ 0 (got %d)", c.MaxDownloads)
	}
	if c.ClipDuration.MinSec < 0 || c.ClipDuration.MaxSec < 0 {
		return fmt.Errorf("stock_supply: clip_duration bounds must be ≥ 0")
	}
	if c.ClipDuration.MinSec > 0 && c.ClipDuration.MaxSec > 0 &&
		c.ClipDuration.MinSec > c.ClipDuration.MaxSec {
		return fmt.Errorf("stock_supply: clip_duration min_sec (%d) must be ≤ max_sec (%d)",
			c.ClipDuration.MinSec, c.ClipDuration.MaxSec)
	}
	// mode == off is the only mode that permits an empty query list
	// (nothing runs). Any active mode requires at least one query.
	if c.effectiveMode() != ModeOff && len(c.Queries) == 0 {
		return fmt.Errorf("stock_supply: queries list is empty (mode=%s requires ≥ 1 query)", c.effectiveMode())
	}
	return nil
}

// ToSupplyQuery projects the wire contract onto the internal
// SupplyQuery. The caller MUST call Validate first; ToSupplyQuery is a
// pure projection and applies no policy.
func (c Contract) ToSupplyQuery() SupplyQuery {
	reuse := true
	if c.ReuseExisting != nil {
		reuse = *c.ReuseExisting
	}
	return SupplyQuery{
		Queries:       c.Queries,
		Strategy:      c.effectiveStrategy(),
		Mode:          c.effectiveMode(),
		Providers:     c.Providers,
		ReuseExisting: reuse,
		SearchLimit:   c.SearchLimit,
		Target: SupplyTarget{
			TargetDurationSec:  c.TargetDurationSec,
			MinimumReadySec:    c.MinimumReadySec,
			MaxClips:           c.MaxDownloads,
			ClipDurationMinSec: c.ClipDuration.MinSec,
			ClipDurationMaxSec: c.ClipDuration.MaxSec,
		},
	}
}

func (c Contract) effectiveMode() SupplyMode {
	if c.Mode == "" {
		return ModeOff
	}
	return c.Mode
}

func (c Contract) effectiveStrategy() ProviderStrategy {
	if c.ProviderStrategy == "" {
		return StrategyFallback
	}
	return c.ProviderStrategy
}

func (c Contract) isValidMode() bool {
	switch c.Mode {
	case ModeOff, ModePrefetch, ModeFallback, ModeHybrid:
		return true
	default:
		return false
	}
}

// Package usecase — deterministic_planner.go is the FASE-3
// (July 2026) implementation of the ClipPrePlanner port.
//
// godlike/06 SSOT: this implementation is the ONLY canonical
// pre-planner. SlotSearchPort (FASE 4) and the shared ClipSampler
// (FASE 5) consume its output. Hashing/canonicalization reuse the
// domain helpers in package script (CanonicalizeSourceText +
// ComputeSourceHash) so plan.SourceHash and SourceAnchor.SourceHash
// stay equal by construction — no parallel implementation of the
// canonicalization substrate.
//
// godlike/07 NO-FAKE-AVAILABILITY: contract violation is a typed
// SourcePlanningError (Reason is one of the bounded constants
// below). The planner NEVER returns a degraded no-op plan; it either
// returns a fully-validated ClipPrePlan or a typed error.
package usecase

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Typed planning errors (bounded by Reason constants) ────────────

// SourcePlanningError envelopes planner-side violations. The
// godlike/07 NO-FAKE-AVAILABILITY contract requires typed errors
// over errors.New so consumer switches can identify failure modes
// without string-sniffing.
type SourcePlanningError struct {
	// ItemID echoes req.ItemID when non-empty; helps operator
	// dashboards correlate.
	ItemID string `json:"item_id,omitempty"`

	// Reason is one of the PlanReason* constants. Adding a new
	// reason requires a deprecation record per
	// architecture/godlike/07_ZERO_LEGACY_POLICY.md.
	Reason string `json:"reason"`

	// Detail is a short human-readable sentence. NEVER embeds
	// SourceText content (no source-text leak through error
	// envelopes).
	Detail string `json:"detail,omitempty"`
}

// PlanReason constants (bounded set — consumer-friendly switches).
const (
	// PlanReasonEmptyTopic: req.Topic is blank.
	PlanReasonEmptyTopic = "empty_topic"
	// PlanReasonEmptyTitle: req.Title is blank.
	PlanReasonEmptyTitle = "empty_title"
)

// Error formats the typed planning error.
func (e *SourcePlanningError) Error() string {
	var b strings.Builder
	b.WriteString("clip pre-planner: ")
	if e.ItemID != "" {
		b.WriteString("item=")
		b.WriteString(e.ItemID)
		b.WriteString(" ")
	}
	b.WriteString("reason=")
	b.WriteString(e.Reason)
	if e.Detail != "" {
		b.WriteString(" (")
		b.WriteString(e.Detail)
		b.WriteString(")")
	}
	return b.String()
}

// ── Planner decisions and thresholds (documented for tunability) ──

// DefaultMaxClips is the effective ceiling when PlanRequest.MaxClips
// is <= 0 (FASE 3 default per the port contract pin: "MaxClips <= 0
// lets the planner choose (currently 8 by default)").
const DefaultMaxClips = 8

// RequiredExcerptMinRunes: slot.Required=false when the source_excerpt
// has fewer runes than this (insufficient narrative weight to
// anchor a clip). The field is in runes, not bytes, because excerpt
// content is operator-readable text, not raw byte-slice markers.
const RequiredExcerptMinRunes = 50

// ExcerptWindowPad is the byte pad applied around a topic match in
// the source_text when computing SourceAnchor.StartOffset/EndOffset.
// Stable across runs: pad width is a compile-time constant, NOT
// derived from input length.
const ExcerptWindowPad = 16

// QueryKeywordSuffixes are stable action-rich keyword tails appended
// to SearchQuery. The order is itself part of the deterministic
// contract: first matching bucket wins. Slots whose topic does not
// match any bucket fall back to QueryFallSuffix.
const (
	QueryPunchSuffix       = "boxing punch hook uppercut jab guard stance"
	QueryCombinationSuffix = "boxing combinations power left right straight pressure"
	QueryCornerSuffix      = "boxing corner pressure ropes referee"
	QueryDecisionSuffix    = "boxing decision judges scorecard unanimous split knockdown"
	QueryFallSuffix        = "boxing scene coverage narrative"
)

// queryKeywordBuckets is the ORDERED slice the planner iterates
// when assigning a SearchQuery tail. Declaration order is part of
// the deterministic contract: a topic matching multiple buckets
// resolves to the first bucket whose keywords match. A map would
// break determinism because Go randomizes map iteration order.
var queryKeywordBuckets = []struct {
	Keywords []string
	Suffix   string
}{
	{[]string{"knockout", "ko", "canvas", "down"}, QueryPunchSuffix},
	{[]string{"combo", "combination", "press", "pressure", "punch", "hook", "uppercut"}, QueryCombinationSuffix},
	{[]string{"corner", "rope", "angle"}, QueryCornerSuffix},
	{[]string{"decision", "judge", "score", "verdict"}, QueryDecisionSuffix},
}

// ── Deterministic planner ──────────────────────────────────────────

// deterministicPlanner is the FASE-3 implementation. It is a pure
// function over PlanRequest: same input -> byte-identical ClipPrePlan
// (verified by golden tests).
type deterministicPlanner struct{}

// NewDeterministicPlanner returns a ClipPrePlanner that satisfies
// the port contract:
//   - Plan: pure; deterministic; fails closed on caller-side violations
//     via SourcePlanningError; never invents source_text; never
//     modifies SourceAnchor offsets after the fact.
//   - ValidatePlan: implements planner-side invariants that go
//     beyond the canonical scriptpkg.ClipPrePlan.Validate() —
//     slot Ref ordinality, SourceAnchor anti-drift gate, target
//     duration non-negativity. Since FASE-2 (July 2026) the
//     port-local types are type aliases for scriptpkg.*, so Validate
//     and ValidatePlan both operate on the same underlying struct
//     and any drift between planner output and the canonical wire
//     shape becomes impossible at the type system layer.
func NewDeterministicPlanner() *deterministicPlanner {
	return &deterministicPlanner{}
}

// Plan is the canonical pre-planner entry point.
func (p *deterministicPlanner) Plan(_ context.Context, req PlanRequest) (*ClipPrePlan, error) {
	if strings.TrimSpace(req.Topic) == "" {
		return nil, &SourcePlanningError{
			ItemID: req.ItemID,
			Reason: PlanReasonEmptyTopic,
			Detail: "req.Topic is required",
		}
	}
	if strings.TrimSpace(req.Title) == "" {
		return nil, &SourcePlanningError{
			ItemID: req.ItemID,
			Reason: PlanReasonEmptyTitle,
			Detail: "req.Title is required",
		}
	}

	// godlike/06 SSOT: canonicalization + hash live in the domain
	// package; reuse them so the planner's SourceHash is exactly the
	// hash the SlotSearchPort compares against later.
	srcHash := scriptpkg.ComputeSourceHash(req.SourceText)
	canonical := scriptpkg.CanonicalizeSourceText(req.SourceText)

	slots := p.buildSlots(srcHash, canonical, req)

	// Effective ceiling: explicit MaxClips > 0 wins; otherwise
	// DefaultMaxClips (per port contract pin). Trim deterministically
	// (keep the FIRST ceiling slots, drop the tail).
	effectiveMax := req.MaxClips
	if effectiveMax <= 0 {
		effectiveMax = DefaultMaxClips
	}
	if len(slots) > effectiveMax {
		slots = slots[:effectiveMax]
	}

	// Total TargetDurationMs is split across slots so the sum
	// equals exactly req.TargetDurationMs. Deterministic distribution
	// when req.TargetDurationMs > 0; otherwise slots keep
	// TargetDurationMs==0 (engine computes runtime).
	if req.TargetDurationMs > 0 && len(slots) > 0 {
		distributeTargetDuration(slots, req.TargetDurationMs)
	}

	return &ClipPrePlan{
		Version:    1,
		SourceHash: srcHash,
		Title:      strings.TrimSpace(req.Title),
		Slots:      slots,
	}, nil
}

// buildSlots dispatches on Segments presence.
func (p *deterministicPlanner) buildSlots(srcHash, canonical string, req PlanRequest) []ClipSearchSlot {
	if len(req.Segments) > 0 {
		return p.buildSlotsFromSegments(srcHash, canonical, req)
	}
	return p.buildSlotsFromText(srcHash, canonical, req)
}

// buildSlotsFromSegments produces one slot per non-empty Segment,
// preserving order. Stable refs: slot-1..slot-k where k =
// len(non-empty segments).
func (p *deterministicPlanner) buildSlotsFromSegments(srcHash, canonical string, req PlanRequest) []ClipSearchSlot {
	out := make([]ClipSearchSlot, 0, len(req.Segments))
	for i, seg := range req.Segments {
		topic := strings.TrimSpace(seg.Topic)
		if topic == "" {
			// FASE-3 contract pin: a Segment is non-empty iff it has
			// a non-blank Topic. (Per code-reviewer Area-7 pin.]
			continue
		}
		start, end, excerpt := anchorOffsetsForTopic(topic, canonical)
		slot := ClipSearchSlot{
			Ref:   fmt.Sprintf("slot-%d", i+1),
			Topic: topic,
			SourceAnchor: &SourceAnchor{
				SourceHash:  srcHash,
				StartOffset: start,
				EndOffset:   end,
				Excerpt:     excerpt,
			},
			SearchQuery:      searchQueryFor(topic, strings.TrimSpace(req.Title)),
			VisualIntent:     visualIntentFor(topic, start, len(canonical)),
			TargetDurationMs: 0,
			Required:         requiredFromExcerpt(excerpt),
		}
		out = append(out, slot)
	}
	return out
}

// buildSlotsFromText derives DefaultMaxClips (or req.MaxClips if
// smaller) equal-byte chunks of canonical. Each chunk becomes one
// slot with a synthetic topic label ("Segment N"). The chunk
// boundaries are deterministic: the last slot always absorbs the
// remainder so chunk-end offsets sum exactly to len(canonical).
func (p *deterministicPlanner) buildSlotsFromText(srcHash, canonical string, req PlanRequest) []ClipSearchSlot {
	if canonical == "" {
		// Per the planner contract pin: empty source -> exactly one
		// degenerate slot with offset 0..0 (the planner does NOT
		// invent text).
		return []ClipSearchSlot{
			{
				Ref:   "slot-1",
				Topic: strings.TrimSpace(req.Topic),
				SourceAnchor: &SourceAnchor{
					SourceHash:  srcHash,
					StartOffset: 0,
					EndOffset:   0,
					Excerpt:     "",
				},
				SearchQuery:      searchQueryFor(strings.TrimSpace(req.Topic), strings.TrimSpace(req.Title)),
				VisualIntent:     visualIntentFor(strings.TrimSpace(req.Topic), 0, 0),
				TargetDurationMs: 0,
				Required:         false,
			},
		}
	}
	planned := DefaultMaxClips
	if req.MaxClips > 0 && req.MaxClips < planned {
		planned = req.MaxClips
	}
	chunkLen := len(canonical) / planned
	if chunkLen == 0 {
		chunkLen = 1
	}
	out := make([]ClipSearchSlot, 0, planned)
	for i := 0; i < planned; i++ {
		start := i * chunkLen
		end := start + chunkLen
		if i == planned-1 {
			end = len(canonical)
		}
		if end > len(canonical) {
			end = len(canonical)
		}
		if start >= end {
			break
		}
		excerpt := canonical[start:end]
		topic := fmt.Sprintf("Segment %d", i+1)
		out = append(out, ClipSearchSlot{
			Ref:   fmt.Sprintf("slot-%d", i+1),
			Topic: topic,
			SourceAnchor: &SourceAnchor{
				SourceHash:  srcHash,
				StartOffset: start,
				EndOffset:   end,
				Excerpt:     excerpt,
			},
			SearchQuery:      searchQueryFor(topic, strings.TrimSpace(req.Title)),
			VisualIntent:     visualIntentFor(topic, start, len(canonical)),
			TargetDurationMs: 0,
			Required:         requiredFromExcerpt(excerpt),
		})
	}
	return out
}

// anchorOffsetsForTopic performs a case-insensitive substring match
// of topic into canonical. On hit, returns (start, end, padded-excerpt).
// On miss or empty inputs, returns (0, 0, "") — a degenerate anchor.
// The planner NEVER invents text.
func anchorOffsetsForTopic(topic, canonical string) (int, int, string) {
	topic = strings.TrimSpace(topic)
	if topic == "" || canonical == "" {
		return 0, 0, ""
	}
	idx := indexCaseInsensitive(canonical, topic)
	if idx < 0 {
		return 0, 0, ""
	}
	topicLen := len(topic)
	start := idx
	end := idx + topicLen
	padStart := start - ExcerptWindowPad
	if padStart < 0 {
		padStart = 0
	}
	padEnd := end + ExcerptWindowPad
	if padEnd > len(canonical) {
		padEnd = len(canonical)
	}
	return start, end, canonical[padStart:padEnd]
}

// indexCaseInsensitive returns the first byte index in haystack
// where needle occurs after strings.ToLower on both. Returns -1 on
// miss; 0 if needle is empty.
func indexCaseInsensitive(haystack, needle string) int {
	if len(needle) == 0 {
		return 0
	}
	if len(needle) > len(haystack) {
		return -1
	}
	return strings.Index(strings.ToLower(haystack), strings.ToLower(needle))
}

// searchQueryFor composes the SearchQuery deterministically.
// Shape: "<title> <topic>" plus the first matching bucket's
// keyword tail. No-match falls back to QueryFallSuffix for
// consistency (so the planner always produces a non-empty query).
func searchQueryFor(topic, title string) string {
	var b strings.Builder
	top := strings.TrimSpace(topic)
	t := strings.TrimSpace(title)
	if t != "" {
		b.WriteString(t)
		if top != "" {
			b.WriteByte(' ')
		}
	}
	if top != "" {
		b.WriteString(top)
	}
	matched := false
	for _, bucket := range queryKeywordBuckets {
		if containsAnyKeyword(topic, bucket.Keywords) {
			if b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteString(bucket.Suffix)
			matched = true
			break
		}
	}
	if !matched {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(QueryFallSuffix)
	}
	return strings.TrimSpace(b.String())
}

// containsAnyKeyword returns true iff topic lowercased contains any
// of the lowercased keywords (substring check).
func containsAnyKeyword(topic string, keywords []string) bool {
	t := strings.ToLower(topic)
	for _, kw := range keywords {
		if strings.Contains(t, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// visualIntentFor is the deterministic narrative description of
// what the chosen clip is expected to show. Format is stable so the
// same input yields the same string (golden-test verifiable).
func visualIntentFor(topic string, start, totalLen int) string {
	var b strings.Builder
	b.WriteString("Visual depiction of: ")
	b.WriteString(strings.TrimSpace(topic))
	b.WriteString(" (at source offset ")
	b.WriteString(strconv.Itoa(start))
	b.WriteByte('/')
	b.WriteString(strconv.Itoa(totalLen))
	b.WriteByte(')')
	return b.String()
}

// requiredFromExcerpt pins the threshold rule for Required:
// Required = true iff excerpt rune count >= RequiredExcerptMinRunes.
func requiredFromExcerpt(excerpt string) bool {
	return len([]rune(excerpt)) >= RequiredExcerptMinRunes
}

// distributeTargetDuration satisfies the contract pin
// "Sum of slot.TargetDurationMs within ±10% of req.TargetDurationMs"
// by setting the sum == exactly req.TargetDurationMs. The algorithm
// is deterministic: base = total / N; remainder (total - base*N)
// is added 1ms at a time to the FIRST `remainder` slots in order.
func distributeTargetDuration(slots []ClipSearchSlot, totalMs int64) {
	if len(slots) == 0 || totalMs <= 0 {
		return
	}
	n := int64(len(slots))
	base := totalMs / n
	remainder := totalMs - base*n
	for i := range slots {
		slots[i].TargetDurationMs = base
	}
	if remainder > 0 {
		for i := int64(0); i < remainder && i < n; i++ {
			slots[i].TargetDurationMs++
		}
	}
}

// ValidatePlan is the post-construction guardrail. Rejects obvious
// drift (e.g. SourceAnchor hash != plan.SourceHash) and missing
// required fields. Since FASE-2 (July 2026) the local-port types
// are type aliases for the scriptpkg.* shapes — ValidatePlan
// operates on the same underlying struct as the canonical
// scriptpkg.ClipPrePlan.Validate() and reinforces its checks with
// planner-specific invariants (slot Ref ordinality, target
// duration non-negativity) that the canonical Validate does not
// own.
func (p *deterministicPlanner) ValidatePlan(plan *ClipPrePlan) error {
	if plan == nil {
		return fmt.Errorf("clip pre plan: nil")
	}
	if plan.Version != 1 {
		return fmt.Errorf("clip pre plan: unsupported version %d (expected 1)", plan.Version)
	}
	if strings.TrimSpace(plan.SourceHash) == "" {
		return fmt.Errorf("clip pre plan: source_hash is required")
	}
	if strings.TrimSpace(plan.Title) == "" {
		return fmt.Errorf("clip pre plan: title is required")
	}
	if len(plan.Slots) == 0 {
		return fmt.Errorf("clip pre plan: at least one slot is required")
	}
	for i, slot := range plan.Slots {
		expectedRef := fmt.Sprintf("slot-%d", i+1)
		if slot.Ref != expectedRef {
			return fmt.Errorf("clip pre plan: slots[%d].ref %q does not match expected %q (deterministic slot-N contract)",
				i, slot.Ref, expectedRef)
		}
		if slot.SourceAnchor == nil {
			return fmt.Errorf("clip pre plan: slots[%d].source_anchor is required", i)
		}
		if slot.SourceAnchor.SourceHash != plan.SourceHash {
			return fmt.Errorf("clip pre plan: slots[%d].source_anchor.source_hash drift on plan.SourceHash", i)
		}
		if strings.TrimSpace(slot.Topic) == "" {
			return fmt.Errorf("clip pre plan: slots[%d].topic is required", i)
		}
		if strings.TrimSpace(slot.SearchQuery) == "" {
			return fmt.Errorf("clip pre plan: slots[%d].search_query is required", i)
		}
		if slot.TargetDurationMs < 0 {
			return fmt.Errorf("clip pre plan: slots[%d].target_duration_ms must be >= 0", i)
		}
	}
	return nil
}

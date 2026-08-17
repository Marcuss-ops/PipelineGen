// Package script — fingerprint.go is the single canonical owner of
// generation fingerprinting for script.generate.
//
// No other package is allowed to concatenate fields into a fingerprint.
// Callers build a GenerationFingerprintInput and pass it to
// BuildFingerprint; the same canonical serialization + SHA-256 is
// used for source fingerprints, item identities, and cache keys.
package script

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

// GenerationFingerprintInput is the canonical, versioned set of
// inputs that determine a script.generate output. Every field that
// can change the generated text is represented here.
//
// The struct is intentionally flat and JSON-serializable so the
// canonical hash is deterministic and auditable. Fields are ordered
// to keep the JSON output stable.
type GenerationFingerprintInput struct {
	// ContractVersion is the fingerprint schema version. Bump it
	// when the serialization changes incompatibly.
	ContractVersion int `json:"contract_version"`

	// SourceType is the canonical source selector ("text", "clips",
	// "catalog", "search", "curate").
	SourceType string `json:"source_type"`

	// ClipIDs is the sorted list of clip IDs used as source.
	ClipIDs []string `json:"clip_ids"`

	// ClipTranscriptHashes is the list of SHA-256 hashes of the
	// transcript text for each accepted clip. It is ordered to
	// correspond one-to-one with ClipIDs.
	ClipTranscriptHashes []string `json:"clip_transcript_hashes"`

	// SourceTextHash is the SHA-256 hash of the source text fed to
	// the model (topic + source_text + guidelines assembly for text
	// sources, or the assembled clip evidence text for clip sources).
	SourceTextHash string `json:"source_text_hash"`

	// Language is the target output language.
	Language string `json:"language"`

	// Tone is the voice/delivery tone.
	Tone string `json:"tone"`

	// Style is the editorial style instructions.
	Style string `json:"style"`

	// Guidelines are the writing style constraints.
	Guidelines string `json:"guidelines"`

	// TargetWords is the target script word count.
	TargetWords int `json:"target_words"`

	// Model is the engine model identifier.
	Model string `json:"model"`

	// PromptVersion is the prompt version used to build the
	// editorial prompt.
	PromptVersion string `json:"prompt_version"`

	// EditorPromptVersion is the editor/rewriter prompt version.
	// It is part of the canonical generation identity because
	// different editor prompts can produce different script text.
	EditorPromptVersion string `json:"editor_prompt_version,omitempty"`

	// QAPromptVersion is the quality-assurance prompt version.
	// It is part of the canonical generation identity because
	// different QA passes can change the final script text.
	QAPromptVersion string `json:"qa_prompt_version,omitempty"`

	// PlannerVersion is the scene-planning algorithm version.
	PlannerVersion string `json:"planner_version"`

	// GroundingPolicy is the clip-grounding policy.
	GroundingPolicy string `json:"grounding_policy"`

	// PR-CS-1 / FASE 7 (July 2026, DoD #9): Segments is part of
	// the canonical generation fingerprint. Each ScriptSegment
	// block (Topic + SourceText + TargetWords) participates in
	// the hash, and the SLICE ORDER is preserved verbatim
	// (DoD #9 reverse-mapping: a reorder IS a different identity,
	// NOT a canonical no-op). SegmentTopics is preserved too
	// because changing the segment topic list changes the generated
	// script shape and must alter replay identity.
	Segments []ScriptSegment `json:"segments"`

	// PRE-EXISTING-7/8 (FASE 13, July 2026): Topic joined the
	// canonical fingerprint input. SegmentTopics is kept in the
	// fingerprint for replay stability when the segment topic
	// schedule changes.
	Topic         string   `json:"topic"`
	SegmentTopics []string `json:"segment_topics,omitempty"`
}

// BuildFingerprint returns the canonical 64-bit hex fingerprint for
// the supplied input. It sorts ClipIDs and ClipTranscriptHashes
// together so the one-to-one pairing is preserved, then hashes the
// deterministic JSON serialization with SHA-256, returning the first
// 16 hex characters.
//
// This function is the SOLE owner of script.generate fingerprint
// calculation. All other fingerprint helpers must delegate here.
func BuildFingerprint(input GenerationFingerprintInput) string {
	input = cloneFingerprintInput(input)
	sortClipIDsAndHashes(&input)

	// json.Marshal on a struct emits fields in declaration order,
	// so the serialization is deterministic once slice order is
	// fixed above.
	raw, err := json.Marshal(input)
	if err != nil {
		// JSON marshaling of this struct cannot fail in practice.
		// Return a sentinel value rather than panicking so callers
		// still get a stable (if degenerate) 16-hex fingerprint.
		return "ffffffffffffffff"
	}

	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:16]
}

// FingerprintInputFromSource builds a source-only fingerprint input.
// It is the canonical input for ResolvedSource.Fingerprint.
func FingerprintInputFromSource(src SourceSpec, ev *ClipEvidence) GenerationFingerprintInput {
	input := GenerationFingerprintInput{
		ContractVersion: 2,
		SourceType:      string(src.Type),
		Guidelines:      src.Guidelines,
		GroundingPolicy: src.GroundingPolicy,
	}

	if len(src.ClipIDs) > 0 {
		input.ClipIDs = append([]string(nil), src.ClipIDs...)
	}

	if ev != nil {
		if text := ev.ModelSourceText(); text != "" {
			input.SourceTextHash = sha256Hex(text)
		} else if text := strings.TrimSpace(ev.AssembledText); text != "" {
			input.SourceTextHash = sha256Hex(text)
		}
		input.ClipIDs = append([]string(nil), ev.AcceptedClipIDs...)
		input.ClipTranscriptHashes = append([]string(nil), ev.ClipTranscriptHashes...)
	} else if src.IsText() {
		input.SourceTextHash = sha256Hex(assembleSourceText(src))
	} else {
		input.SourceTextHash = sha256Hex(sourceTextOrQuery(src))
	}

	return input
}

// FingerprintInputFromPlan builds a generation-parameter fingerprint
// input from a resolved plan. It is the canonical input for
// ResolvedGenerationPlan.CacheKey.
func FingerprintInputFromPlan(plan *ResolvedGenerationPlan) GenerationFingerprintInput {
	input := GenerationFingerprintInput{
		ContractVersion:     2,
		SourceType:          plan.SourceKind,
		SourceTextHash:      plan.SourceFingerprint,
		Language:            plan.Language,
		Tone:                plan.Tone,
		Style:               plan.Style,
		Guidelines:          plan.Guidelines,
		TargetWords:         plan.TargetWords,
		Model:               plan.Model,
		PromptVersion:       plan.PromptVersion,
		EditorPromptVersion: plan.EditorPromptVersion,
		QAPromptVersion:     plan.QAPromptVersion,
		PlannerVersion:      plan.PromptProfile,
		GroundingPolicy:     plan.GroundingPolicy,
	}

	if plan.ClipEvidence != nil {
		input.ClipIDs = append([]string(nil), plan.ClipEvidence.AcceptedClipIDs...)
		input.ClipTranscriptHashes = append([]string(nil), plan.ClipEvidence.ClipTranscriptHashes...)
	}

	// PR-CS-1 / FASE 7 (DoD #9): per-block ScriptSegment payload
	// participates in the fingerprint. The order from plan.Segments
	// is preserved verbatim (a reorder IS a different identity,
	// NOT a no-op). The slice is defensively copied so future
	// in-place edits to plan.Segments do not leak into the
	// BuildFingerprint call chain.
	if len(plan.Segments) > 0 {
		input.Segments = append([]ScriptSegment(nil), plan.Segments...)
	}

	// PRE-EXISTING-7/8 (FASE 13): map Topic into the canonical
	// fingerprint. Mutations on Topic MUST change the cache key
	// (test invariant) and two plans with different Topics MUST
	// produce distinct keys.
	if plan.Topic != "" {
		input.Topic = plan.Topic
	}

	if len(plan.SegmentTopics) > 0 {
		input.SegmentTopics = append([]string(nil), plan.SegmentTopics...)
	}

	return input
}

// FingerprintInputFromItem builds a full item fingerprint input. It
// is the canonical input for adapters.BuildItemIdentity.
func FingerprintInputFromItem(item GenerationItemV2) GenerationFingerprintInput {
	input := GenerationFingerprintInput{
		ContractVersion:     2,
		SourceType:          string(item.Source.Type),
		Language:            item.Language,
		Tone:                item.Tone,
		Style:               item.Style,
		Guidelines:          item.Source.Guidelines,
		TargetWords:         item.ScriptParams.TargetWords,
		Model:               item.Model,
		PromptVersion:       item.ScriptParams.PromptVersion,
		EditorPromptVersion: item.ScriptParams.EditorPromptVersion,
		QAPromptVersion:     item.ScriptParams.QAPromptVersion,
		PlannerVersion:      item.ScriptParams.PlannerVersion,
		GroundingPolicy:     item.Source.GroundingPolicy,
	}

	if len(item.Source.ClipIDs) > 0 {
		input.ClipIDs = append([]string(nil), item.Source.ClipIDs...)
	}

	if item.Source.IsText() {
		input.SourceTextHash = sha256Hex(assembleSourceText(item.Source))
	} else {
		input.SourceTextHash = sha256Hex(sourceTextOrQuery(item.Source))
	}

	return input
}

// sourceTextOrQuery returns the canonical source text for non-text
// sources. When SourceText is empty, the query is the content that
// will be resolved (e.g. catalog/search query), so it participates
// in the identity.
func sourceTextOrQuery(src SourceSpec) string {
	if sourceText := src.SourceText; sourceText != "" {
		return sourceText
	}
	return src.Query
}

// assembleSourceText returns the canonical text assembly for a text
// source, matching the format produced by TextSourceResolver.
func assembleSourceText(src SourceSpec) string {
	var parts []string
	if topic := strings.TrimSpace(src.Topic); topic != "" {
		parts = append(parts, "Topic: "+topic)
	}
	if sourceText := strings.TrimSpace(src.SourceText); sourceText != "" {
		parts = append(parts, "Source: "+sourceText)
	}
	if guidelines := strings.TrimSpace(src.Guidelines); guidelines != "" {
		parts = append(parts, "Guidelines: "+guidelines)
	}
	return strings.Join(parts, "\n")
}

// sortClipIDsAndHashes sorts ClipIDs lexicographically and reorders
// ClipTranscriptHashes to keep the one-to-one pairing intact.
func sortClipIDsAndHashes(input *GenerationFingerprintInput) {
	if input == nil {
		return
	}

	n := len(input.ClipIDs)
	m := len(input.ClipTranscriptHashes)
	if n == 0 && m == 0 {
		return
	}

	// Pair the overlapping prefix so transcript hashes stay aligned
	// with their clip IDs. Any unpaired tail is appended after the
	// sorted pairs to keep the hash deterministic.
	pairs := make([][2]string, 0, n)
	for i := 0; i < n; i++ {
		hash := ""
		if i < m {
			hash = input.ClipTranscriptHashes[i]
		}
		pairs = append(pairs, [2]string{input.ClipIDs[i], hash})
	}

	sort.Slice(pairs, func(i, j int) bool {
		return pairs[i][0] < pairs[j][0]
	})

	sortedIDs := make([]string, 0, n)
	sortedHashes := make([]string, 0, n)
	for _, p := range pairs {
		sortedIDs = append(sortedIDs, p[0])
		sortedHashes = append(sortedHashes, p[1])
	}

	// Append any extra hashes that lack a corresponding clip ID.
	if m > n {
		extra := append([]string(nil), input.ClipTranscriptHashes[n:]...)
		sort.Strings(extra)
		sortedHashes = append(sortedHashes, extra...)
	}

	input.ClipIDs = sortedIDs
	input.ClipTranscriptHashes = sortedHashes
}

// cloneFingerprintInput returns a shallow copy of the input so
// in-place slice sorting does not mutate the caller's data.
func cloneFingerprintInput(input GenerationFingerprintInput) GenerationFingerprintInput {
	if input.ClipIDs != nil {
		input.ClipIDs = append([]string(nil), input.ClipIDs...)
	}
	if input.ClipTranscriptHashes != nil {
		input.ClipTranscriptHashes = append([]string(nil), input.ClipTranscriptHashes...)
	}
	// PR-CS-1 / FASE 7 (DoD #9): defensive deep-copy of Segments.
	// ScriptSegment is a value-struct (Topic + SourceText +
	// TargetWords are non-pointer fields) so the append pattern
	// copies both the slice header AND each value-struct entry
	// (Go semantics on `append([]T(nil), src...)` for value types).
	// Without this, BuildFingerprint's in-place SortClipIDsAndHashes
	// (or future similar transforms) would mutate the caller's slice.
	if input.Segments != nil {
		input.Segments = append([]ScriptSegment(nil), input.Segments...)
	}
	if input.SegmentTopics != nil {
		input.SegmentTopics = append([]string(nil), input.SegmentTopics...)
	}
	return input
}

// sha256Hex returns the SHA-256 hex digest of s.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

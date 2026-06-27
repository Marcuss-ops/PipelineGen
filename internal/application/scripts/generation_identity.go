// Package scripts — generation_identity.go provides a single canonical
// identity/fingerprint function for generation items. It replaces the
// duplicated ComputeFingerprint logic in clip_source_builder.go and
// the sourceFingerprint usage in pipeline_handlers.go.
//
// The identity is deterministic and includes every field that affects
// the generated script content. Output flags (postprocessors) are
// excluded — they control what artifacts to produce, not what text
// the engine writes.
package scripts

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildItemIdentity computes a deterministic identity string for a
// generation item. Two items with the same identity produce the same
// script text (modulo LLM non-determinism), so the identity serves as
// a memory-gate cache key.
//
// Included in the identity:
//   - Title, Language, Tone, Model
//   - Source content (topic, source_text, clip IDs, query)
//   - Script sizing (target words, duration)
//   - Prompt versions
//   - Style and guidelines
//
// Excluded from the identity:
//   - Source type (text/clips/catalog/search — how clips were found
//     does NOT affect the script; catalog and search produce the same
//     output for the same query)
//   - Output flags (postprocessors don't affect script text)
//   - Drive folder IDs (transport concern)
//   - ForceRefresh (cache-bypass control, not identity)
func BuildItemIdentity(item scriptpkg.GenerationItemV2) string {
	var parts []string

	add := func(key, val string) {
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}
		parts = append(parts, key+"="+val)
	}

	add("id", item.ID)
	add("title", item.Title)
	add("lang", item.Language)
	add("tone", item.Tone)
	add("model", item.Model)

	// Source identity. Source type is excluded — catalog and search
	// should produce the same deterministic fingerprint for the same
	// query, since the ResolvedSource shape is identical.
	src := item.Source
	add("topic", src.Topic)
	add("src_text", src.SourceText)
	add("guidelines", src.Guidelines)

	// Clip IDs (sorted for determinism).
	if len(src.ClipIDs) > 0 {
		sorted := make([]string, len(src.ClipIDs))
		copy(sorted, src.ClipIDs)
		sort.Strings(sorted)
		add("clips", strings.Join(sorted, ","))
	}
	add("query", src.Query)
	add("transcript", src.TranscriptPolicy)
	add("order", src.OrderingStrategy)

	// Script sizing.
	sp := item.ScriptParams
	if sp.TargetWords > 0 || sp.Duration > 0 || sp.MinWords > 0 || sp.SegmentWords > 0 || len(sp.SegmentTopics) > 0 {
		parts = append(parts, "size="+scriptSizeKey(sp))
	}
	add("prompt_v", sp.PromptVersion)
	add("editor_v", item.ScriptParams.EditorPromptVersion)
	add("qa_v", item.ScriptParams.QAPromptVersion)
	add("style", item.Style)
	add("script_guidelines", item.ScriptParams.Guidelines)

	raw := strings.Join(parts, "|")
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])[:16] // first 16 hex chars (64 bits)
}

// BuildEnvelopeIdentity computes a single identity for the entire
// envelope, used for top-level idempotency. For single-item envelopes
// this is the item identity; for multi-item envelopes it's a hash of
// all item identities concatenated.
func BuildEnvelopeIdentity(env *scriptpkg.GenerationEnvelopeV2) string {
	if env == nil || len(env.Items) == 0 {
		return ""
	}
	if len(env.Items) == 1 {
		return BuildItemIdentity(env.Items[0])
	}
	var ids []string
	for _, item := range env.Items {
		ids = append(ids, BuildItemIdentity(item))
	}
	raw := strings.Join(ids, "||")
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])[:16]
}

// scriptSizeKey produces a compact size representation for identity.
func scriptSizeKey(sp scriptpkg.ScriptSpec) string {
	parts := make([]string, 0, 3)
	if sp.TargetWords > 0 {
		parts = append(parts, "tw="+itoa(sp.TargetWords))
	}
	if sp.Duration > 0 {
		parts = append(parts, "dur="+itoa(sp.Duration))
	}
	if sp.MinWords > 0 {
		parts = append(parts, "min="+itoa(sp.MinWords))
	}
	if sp.SegmentWords > 0 {
		parts = append(parts, "segment_words="+itoa(sp.SegmentWords))
	}
	if len(sp.SegmentTopics) > 0 {
		topics := make([]string, len(sp.SegmentTopics))
		copy(topics, sp.SegmentTopics)
		for i := range topics {
			topics[i] = strings.TrimSpace(topics[i])
		}
		parts = append(parts, "segment_topics="+strings.Join(topics, ","))
	}
	return strings.Join(parts, ",")
}

// itoa is a tiny int-to-string helper that avoids importing strconv
// for the few identity fields that carry int values.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	digits := make([]byte, 0, 10)
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// Package scripts — source_resolver_clips.go resolves SourceClips
// sources into a ResolvedSource. It uses ClipSourceBuilder to fetch
// clips by ID and build context, then converts the result into
// typed ClipEvidence.
package usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"sort"
	"strings"
	"time"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/ptrutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"

	"go.uber.org/zap"
)

// ClipsSourceResolver resolves SourceClips (explicit clip IDs)
// into a ResolvedSource with ClipEvidence.
type ClipsSourceResolver struct {
	clipBuilder *ClipSourceBuilder
	log         *zap.Logger
}

// NewClipsSourceResolver creates a ClipsSourceResolver backed by
// the canonical ClipSourceBuilder. clipBuilder must be non-nil.
func NewClipsSourceResolver(clipBuilder *ClipSourceBuilder, log *zap.Logger) *ClipsSourceResolver {
	return &ClipsSourceResolver{
		clipBuilder: clipBuilder,
		log:         log,
	}
}

// Resolve fetches clips by explicit ID and builds a ResolvedSource
// with ClipEvidence.
//
// PR 4 (June 2026): resolutionContext is now threaded into
// ClipGenerationOptions.Language/Tone/Model/Style/TargetWords. The
// resolver reads these explicitly from the resolution context (the
// canonical source of operator-side traits) instead of hijacking
// SourceSpec.Guidelines.
func (r *ClipsSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	if r == nil || r.clipBuilder == nil {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "clips source resolver: ClipSourceBuilder not configured",
		}
	}

	if len(src.ClipIDs) == 0 {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "clips source requires at least one clip_id",
		}
	}

	start := time.Now()

	opts := &ClipGenerationOptions{
		// PR 4: language/tone/model/style/target words come from
		// resolutionContext — the canonical operator-side context.
		// src.Guidelines is intentionally NOT consumed here
		// (curate resolver historically did this; the bug class
		// is now centralised: any resolver reading Guidelines as
		// a technical field is wrong).
		Language:           resCtx.Language,
		Title:              resCtx.Title,
		Tone:               resCtx.Tone,
		Model:              resCtx.Model,
		Style:              resCtx.Style,
		TargetWords:        resCtx.TargetWords,
		NumClips:           resCtx.NumClips,
		SegmentWords:       resCtx.SegmentWords,
		SegmentTopics:      append([]string(nil), resCtx.SegmentTopics...),
		TranscriptPolicy:   src.TranscriptPolicy,
		OrderingStrategy:   src.OrderingStrategy,
		MinQualityScore:    ptrutil.DerefOr(src.MinQualityScore, 0.0),
		MinTranscriptWords: ptrutil.DerefOr(src.MinTranscriptWords, 0),
		// P0 #3 (June 2026): DriveLink required only when caller
		// wants document or scene images.
		RequireDriveLink: resCtx.RequireDriveLink,
	}

	return buildResolvedClipSource(ctx, r.clipBuilder, src, resolvedClipParams{
		sourceType:    scriptpkg.SourceClips,
		query:         strings.Join(src.ClipIDs, ","),
		clipIDs:       src.ClipIDs,
		opts:          opts,
		titleFallback: textutil.FirstNonEmpty(resCtx.Title, "Clip Script"),
		startTime:     start,
	}, r.log)
}

// BuildClipFingerprint computes a deterministic fingerprint from the
// fields of a SourceSpec and resolved ClipEvidence that ACTUALLY
// affect the generated script output.
//
// v2 (June 2026, Step 4 — fingerprint unification): the key is a
// strict whitelist of the inputs the assembled clip-evidence /
// script-generation pipeline actually reads. The previous (v1)
// scheme ad-hoc-included MinQualityScore, MinTranscriptWords,
// Topic, and SourceText — none of which the clip path applies,
// so changing the client-side value mutated the fingerprint but
// NOT the output. Cache-key poison resolved.
//
// WHITELIST (apply-only invariant):
//
//   - Version tag: fp=v2-apply-only
//   - Clip IDs (sorted) — request-side clip set
//   - TranscriptPolicy — forward-looking, A7 will apply it
//   - OrderingStrategy — applied by BuildClipContext
//   - Evidence AcceptedClipIDs (sorted) — resolved clip set
//   - Evidence AssembledText — the actual text fed to the LLM
//
// Text path (ev == nil):
//   - Topic + SourceText — these DO affect text-source output
//
// STRUCTURAL EXCLUSIONS:
//   - MinQualityScore — not applied (A7g pending)
//   - MinTranscriptWords — not applied (A7h pending)
func BuildClipFingerprint(src scriptpkg.SourceSpec, ev *scriptpkg.ClipEvidence) string {
	h := sha256.New()
	add := func(key, val string) {
		val = strings.TrimSpace(val)
		if val == "" {
			return
		}
		io.WriteString(h, key)
		io.WriteString(h, "=")
		io.WriteString(h, val)
		io.WriteString(h, "|")
	}

	// Version tag — bump to invalidate all prior caches.
	add("fp", "v2-apply-only")

	// Clip IDs from the source spec (sorted for determinism).
	if len(src.ClipIDs) > 0 {
		sorted := make([]string, len(src.ClipIDs))
		copy(sorted, src.ClipIDs)
		sort.Strings(sorted)
		add("clips", strings.Join(sorted, ","))
	}
	add("tp", src.TranscriptPolicy)
	add("order", src.OrderingStrategy)

	// Evidence fields (when clips were resolved).
	// Issue #2 (June 2026): ClipIDs renamed → AcceptedClipIDs.
	if ev != nil {
		if len(ev.AcceptedClipIDs) > 0 {
			sorted := make([]string, len(ev.AcceptedClipIDs))
			copy(sorted, ev.AcceptedClipIDs)
			sort.Strings(sorted)
			add("evclips", strings.Join(sorted, ","))
		}
		add("atext", ev.AssembledText)
	} else {
		// Text path: Topic and SourceText DO affect output.
		add("topic", src.Topic)
		add("stext", src.SourceText)
	}

	sum := h.Sum(nil)
	// First 16 hex chars (64 bits), matching the convention in
	// adapters/generation_identity.go and domain/script/cache_key.go.
	return hex.EncodeToString(sum[:])[:16]
}

// BuildItemIdentity computes a deterministic identity string for a
// generation item. It hashes all item-level fields that affect the
// generated script output (Title, Language, Tone, Model, source
// content, sizing, style, guidelines) plus the source fingerprint.
// Two items with the same identity produce the same script text.
//
// Canonical implementation matching the field set in
// adapters/generation_identity.go. The two functions produce
// identical hashes for the same input — this copy exists here to
// avoid a usecase→adapters import.
//
// Included: Title, Language, Tone, Model, Source fields (topic,
// source_text, guidelines, sorted clip IDs, query, transcript_policy,
// ordering_strategy), script sizing (target_words, duration, min_words,
// segment_words, segment_topics), prompt versions, style, guidelines.
//
// Excluded: Source type (text/clips — doesn't change output), output
// flags (postprocessors don't affect script text), drive folder IDs
// (transport concern), ForceRefresh (cache-bypass, not identity).
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

	src := item.Source
	add("topic", src.Topic)
	add("src_text", src.SourceText)
	add("guidelines", src.Guidelines)

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
	add("editor_v", sp.EditorPromptVersion)
	add("qa_v", sp.QAPromptVersion)
	add("style", item.Style)
	add("script_guidelines", sp.Guidelines)

	raw := strings.Join(parts, "|")
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

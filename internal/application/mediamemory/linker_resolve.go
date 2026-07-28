// Package mediamemory — linker_resolve.go owns the canonical "resolve"
// phase of the linker pipeline (architecture doc section 7 + Fase 3.2).
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// (media-source → candidate-text) extraction seam. The three helpers
// here (extractTranscript, extractVisualDescriptions, normalizedEntities)
// together form the "resolve" phase of the linker pipeline that
// EnrichCandidate (linker_worker.go) composes:
//
//   1. extractTranscript — canonical transcript text extraction
//      (skip-on-nil extractor; Fase 3.2 fallback path).
//   2. extractVisualDescriptions — canonical visual-desc text
//      extraction (per-keyframe caption join + best-effort
//      partial-success envelope).
//   3. normalizedEntities — canonical case-insensitive entity
//      dedupe (deterministic seed list, godlike/06 SSOT).
//
// godlike/07 NO-FAKE-AVAILABILITY: a nil extractor is the canonical
// Fase 3.2 fallback path (no transcriber / visual-describer wiring
// yet) — the linker degrades to "no transcript / no visual-desc input"
// without spurious failures. The functions here NEVER swallow
// failures silently: every per-step failure surfaces as either a
// wrapped typed sentinel (ErrLinkerExtractFailed) or an accumulated
// []string entry in the caller's res.Failures[] slice.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// extractTranscript walks the TranscriptExtractor and joins
// segments into a canonical text envelope. godlike/06 SSOT
// (skip-on-nil): a nil extractor is the canonical fallback for
// Fase 3.2 (no transcriber wiring yet) — the linker degrades
// to "no transcript input" without spurious failures.
func (w *defaultLinker) extractTranscript(ctx context.Context, req LinkerRequest) (string, error) {
	if w.deps.Transcript == nil {
		return "", nil
	}
	segments, err := w.deps.Transcript.Extract(ctx, req.Candidate.SourceURL, req.Candidate.MediaType)
	if err != nil {
		return "", fmt.Errorf("mediamemory: linker transcript for %q: %w",
			req.Candidate.ID, errors.Join(ErrLinkerExtractFailed, err))
	}
	var sb strings.Builder
	for _, seg := range segments {
		sb.WriteString(seg.Text)
		sb.WriteString(" ")
	}
	return strings.TrimSpace(sb.String()), nil
}

// extractVisualDescriptions walks (KeyframeExtractor,
// VisualDescriptionGenerator) and joins per-keyframe strings
// into a canonical visual-desc envelope. Returns (text, keyframes, errs)
// so a partial-extraction success can still proceed (e.g. some
// keyframes fail to caption — the linker uses the survivors).
// godlike/06 SSOT: a nil extractor pair short-circuits to
// ("", nil, nil) so the linker degrades gracefully.
//
// Fase 4.1 (visual-channel completion): when w.deps.FrameIndexer
// is wired AND w.deps.KeyframeEmbeddingText is non-empty, the
// linker ALSO generates one 768d SigLIP vector per keyframe
// (via the canonical EmbeddingChannelRegistry.ChannelVisual
// path) and writes it to pipelinegen_media_frames. Transient
// errors during frame indexing append to res.Failures[] but do
// NOT short-circuit concept/binding persistence — the visual
// channel is best-effort envelope over the canonical resolver
// hot path.
func (w *defaultLinker) extractVisualDescriptions(ctx context.Context, req LinkerRequest) (string, []Keyframe, []string) {
	if w.deps.Keyframe == nil || w.deps.VisualGen == nil {
		return "", nil, nil
	}
	keyframes, err := w.deps.Keyframe.Extract(ctx, req.Candidate.SourceURL, req.Candidate.MediaType)
	if err != nil {
		return "", nil, []string{fmt.Sprintf(
			"candidate=%q keyframe extract failed: %s", req.Candidate.ID, err.Error(),
		)}
	}
	var (
		sb          strings.Builder
		extractErrs []string
	)
	for _, kf := range keyframes {
		d, derr := w.deps.VisualGen.Generate(ctx, kf)
		if derr != nil {
			extractErrs = append(extractErrs, fmt.Sprintf(
				"candidate=%q keyframe_ms=%d visual-describe failed: %s",
				req.Candidate.ID, kf.Ms, derr.Error(),
			))
			continue
		}
		sb.WriteString(d)
		sb.WriteString(" ")
	}
	return strings.TrimSpace(sb.String()), keyframes, extractErrs
}

// normalizedEntities trims and dedupes an entity list. godlike/06
// SSOT (deterministic seed list): the canonical pre-upsert
// pipeline MUST be deterministic — a re-run with the same input
// produces the same (dedupe-d) entity sequence. De-dupe is
// case-insensitive to ensure that "Maya" / "maya" / "MAYA"
// collapse to a single seed phrase.
func normalizedEntities(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		k := strings.ToLower(strings.TrimSpace(e))
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, strings.TrimSpace(e))
	}
	return out
}

// Package mediamemory — linker_emit.go owns the canonical "emit"
// phase of the linker pipeline (architecture doc section 7 + Fase 3.2).
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// (MediaConcept → multicanale vector → Qdrant) and (Keyframe →
// SigLIP vector → pipelinegen_media_frames) emission seam. The two
// helpers here (encodeAndIndexConcepts, indexKeyframes) together
// form the "emit" phase of the linker pipeline that
// EnrichCandidate (linker_worker.go) composes:
//
//   1. encodeAndIndexConcepts — canonical multicanale encoder +
//      Qdrant concept indexer (per concept; idempotent via the
//      canonical Qdrant point at concept_id key).
//   2. indexKeyframes — canonical Fase 4.1 per-keyframe SigLIP
//      encoder + pipelinegen_media_frames indexer (best-effort
//      envelope over the canonical resolver hot path; per-keyframe
//      failures land in res.Failures[] without flipping the
//      candidate's DiscoveryStatus).
//
// godlike/07 NO-FAKE-AVAILABILITY: encodeAndIndexConcepts returns
// a single `error` (NOT a []string failures slice) so ANY encoder-
// call / zero-vector / concept-reupsert / Qdrant-index failure
// bubbles up to EnrichCandidate as a wrapped
// ErrLinkerEmbeddingFailed. The candidate's DiscoveryStatus stays
// at DiscoverySearched for Resume retry. indexKeyframes uses the
// []string failures-slice pattern instead — the visual channel is
// best-effort envelope, and per-keyframe failures do NOT short-
// circuit the rest of the linker pipeline.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// encodeAndIndexConcepts runs the multichannel encoder + Qdrant
// indexer per concept. godlike/06 SSOT: a nil encoding pipeline
// is the canonical "Phase 3.5 forward-pin" state — the linker
// degrades to concepts-without-vectors (still valid because
// the (phrase_fingerprint → concept_id) pointer is the SSOT,
// and Fase 2's indexer can backfill vectors per Fase 2.1).
//
// godlike/06 SSOT (EmbeddingVersion SSOT): the canonical
// concepts.Upsert in normalizeAndUpsertConcepts wrote the
// concept row BEFORE the encoder chose the version, so the
// media_concepts.embedding_version row was stamped with empty.
// To keep media_concepts.embedding_version in sync with the
// Qdrant point's embedding_version field, the linker re-upserts
// the concept with EmbeddingVersion set after Encode succeeds.
// ConceptRepository.Upsert is ON CONFLICT DO UPDATE so the
// re-upsert is canonical idempotent (no row churn for repeat
// ConceptID + same version).
// godlike/07 NO-FAKE-AVAILABILITY (transient propagation):
// this helper returns a single `error` (NOT a []string failures
// slice) so ANY encoder-call / zero-vector / concept-reupsert /
// Qdrant-index failure bubbles up to EnrichCandidate as a wrapped
// ErrLinkerEmbeddingFailed. The candidate's DiscoveryStatus stays
// at DiscoverySearched (Resume re-attempt contract). The
// orchestrator's failedCount logic counts ONLY
// ErrLinkerUnmappableConcept and ErrLinkerInvariantBroken as
// hard-fail signals; ErrLinkerEmbeddingFailed preserves the
// Reconciling state so a subsequent EnrichLinker call retries
// naturally.
func (w *defaultLinker) encodeAndIndexConcepts(ctx context.Context, req LinkerRequest, concepts []MediaConcept, transcriptText, visualDesc string) error {
	if w.deps.Encoder == nil || w.deps.Indexer == nil {
		return nil
	}
	for _, c := range concepts {
		channels := EncodingChannels{
			Text:       c.CanonicalText,
			Transcript: transcriptText,
			VisualDesc: visualDesc,
		}
		embedding, eerr := w.deps.Encoder.Encode(ctx, channels)
		if eerr != nil {
			return fmt.Errorf("candidate=%q concept=%q encode failed: %w",
				req.Candidate.ID, c.ID, errors.Join(ErrLinkerEmbeddingFailed, eerr))
		}
		if len(embedding.Vector) == 0 {
			return fmt.Errorf("candidate=%q concept=%q encoder returned zero-vector: %w",
				req.Candidate.ID, c.ID, ErrLinkerEmbeddingFailed)
		}
		c.EmbeddingVersion = embedding.Model
		// Re-upsert so media_concepts.embedding_version stays in
		// sync with the Qdrant payload's embedding_version
		// (canonical godlike/06 SSOT sync).
		if _, uerr := w.deps.Concepts.Upsert(ctx, c); uerr != nil {
			return fmt.Errorf("candidate=%q concept=%q re-upsert with EmbeddingVersion failed: %w",
				req.Candidate.ID, c.ID, errors.Join(ErrLinkerEmbeddingFailed, uerr))
		}
		if ierr := w.deps.Indexer.IndexConcept(ctx, c); ierr != nil {
			return fmt.Errorf("candidate=%q concept=%q qdrant index failed: %w",
				req.Candidate.ID, c.ID, errors.Join(ErrLinkerEmbeddingFailed, ierr))
		}
	}
	return nil
}

// indexKeyframes runs the Fase 4.1 visual-channel frame-index
// path. It is invoked AFTER concept persistence (step 5+6) and
// AFTER the multichannel concept embedding (step 7) so a
// transient frame-index failure cannot poison the canonical
// resolver hot path (godlike/06 SSOT best-effort envelope).
//
// godlike/07 NO-FAKE-AVAILABILITY (typed envelopes): per-keyframe
// failures are accumulated in the returned []string so the
// caller merges them into res.Failures[]. Transient failures
// (encoder / indexer) do NOT short-circuit subsequent keyframes.
//
// The text passed to the SigLIP encoder is the canonical
// KeyframeEmbeddingText (typically the candidate's title /
// description / visual-desc envelope). When the port is nil
// or text is empty the function short-circuits to nil.
//
// Each keyframe becomes a Qdrant point at
// pipelinegen_media_frames with the canonical `frame-{videoID}-{tsMs}`
// ID so re-extract is idempotent. Errors wrap
// ErrLinkerEmbeddingFailed; transient failures are appended to
// the returned slice, and the orchestrator proceeds.
func (w *defaultLinker) indexKeyframes(ctx context.Context, req LinkerRequest, keyframes []Keyframe) []string {
	var failures []string
	if w.deps.FrameIndexer == nil || w.deps.Encoder == nil {
		return failures
	}
	embedText := strings.TrimSpace(w.deps.KeyframeEmbeddingText)
	if embedText == "" {
		return failures
	}
	if len(keyframes) == 0 {
		return failures
	}
	videoID := strings.TrimSpace(req.Candidate.AssetID)
	if videoID == "" {
		videoID = strings.TrimSpace(req.Candidate.ProviderAssetID)
	}
	if videoID == "" {
		// Without a canonical asset/assetID the deterministic
		// point ID derivation falls back to candidate ID —
		// still unique per linker call, just not stable across
		// recovery passes.
		videoID = strings.TrimSpace(req.Candidate.ID)
	}
	language := strings.TrimSpace(req.Language)
	for _, kf := range keyframes {
		if kf.Ms < 0 {
			continue
		}
		channels := EncodingChannels{
			Text:       embedText,
			Transcript: "",
			VisualDesc: "",
		}
		embedding, eerr := w.deps.Encoder.Encode(ctx, channels)
		if eerr != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q frame ts=%d encode failed: %s",
				req.Candidate.ID, kf.Ms, eerr.Error()))
			continue
		}
		if len(embedding.Vector) == 0 {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q frame ts=%d encoder returned empty vector",
				req.Candidate.ID, kf.Ms))
			continue
		}
		if ierr := w.deps.FrameIndexer.IndexKeyframe(
			ctx,
			videoID,
			kf.Ms,
			req.Candidate.AssetID,
			language,
			embedding.Vector,
			embedding.Model,
		); ierr != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q frame ts=%d index failed: %s",
				req.Candidate.ID, kf.Ms, ierr.Error()))
		}
	}
	return failures
}

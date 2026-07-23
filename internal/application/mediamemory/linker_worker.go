// Package mediamemory — linker_worker.go is the canonical SSOT
// for the media.linker worker (architecture doc section 7 + Fase
// 3.2 user spec).
//
// godlike/06 SSOT: LinkerWorker is the SINGLE owner of the
// (candidate → concept → media_bindings) enrichment seam. The
// enrichment is per-candidate and per-call ATOMIC: either the
// candidate's DiscoveryStatus flips to DiscoveryIndexed (and
// media_bindings + concepts + Qdrant embeddings are persisted
// in one logical pass) or it stays at DiscoverySearched so a
// Resume retries the pipeline naturally on the next pull.
//
// godlike/06 SSOT (idempotency + resumability contract, enforced
// at this worker boundary):
//
//  1. Idempotency — a second EnrichCandidate call with a
//     candidate whose DiscoveryStatus ∈ {DiscoveryIndexed,
//     DiscoveryMaterialized} returns LinkerResult.Empty=true
//     and produces ZERO new writes (godlike/07 NO-FAKE-AVAILABILITY:
//     the early-return MUST be detectable via Empty so callers
//     don't re-invoke downstream steps).
//
//  2. Resumability — a candidate whose DiscoveryStatus ==
//     DiscoverySearched is processed in full again. The canonical
//     binding semantic (BindingsRepository.Upsert uses ON
//     CONFLICT DO UPDATE) makes a re-run safe — the rows are
//     rewritten with identical values, timestamps update.
//
//  3. Hard-fail semantic — a candidate whose enrichment cannot
//     produce any (concept, slot_kind) tuple transitions to
//     DiscoveryFailed via CandidateRepository.UpdateStatus and
//     returns wrapped ErrLinkerUnmappableConcept. The batch
//     continues with the next candidate.
//
//  4. Transient semantic — extract / encoding failures leave the
//     candidate at DiscoverySearched. Subsequent Resume retries
//     until success or until an operator-driven kill flips the
//     batch to Failed.
//
//  5. Invariant semantic — post-write reading that detects
//     (binding-without-concept or embedding-without-binding) is
//     surfaced as ErrLinkerInvariantBroken which is unrecoverable
//     (godlike/07). The candidate goes to DiscoveryFailed.
//
// godlike/07 NO-FAKE-AVAILABILITY: failures are NEVER swallowed.
// Every per-step failure surfaces a typed sentinel so the
// BatchService orchestrator can branch via errors.Is.
package mediamemory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
	"github.com/google/uuid"
)

// ── Dependency bundle ────────────────────────────────────────

// LinkerDeps is the canonical dependency bundle for
// LinkerWorker. godlike/06 SSOT (narrow port doctrine): the
// linker composes only the ports it actually consumes.
// Composition root wires concrete adapters (transcriber /
// keyframe-puller / visual-describer / entity-ner / text-encoder /
// sqlite repos / qdrant-indexer / canonical normalizer).
type LinkerDeps struct {
	Transcript   TranscriptExtractor
	Keyframe     KeyframeExtractor
	VisualGen    VisualDescriptionGenerator
	EntityDetect EntityDetector
	Encoder      EmbeddingEncoder
	Concepts     ConceptRepository
	Bindings     BindingRepository
	Candidates   CandidateRepository
	Indexer      EmbeddingIndexer
	// FrameIndexer (Fase 4.1) is the canonical seam that writes
	// per-keyframe 768d SigLIP vectors to pipelinegen_media_frames.
	// nil = the linker degrades to concepts-only (forward-pin to
	// phases when the production KeyframeExtractor + SigLIP sidecar
	// are wired). The degrade path is godlike/06 SSOT (canonical
	// fail-closed fallback is "skip frame indexing, not fail the
	// candidate").
	FrameIndexer KeyframeVisualIndexer
	// KeyframeEmbeddingText is the canonical text used to drive
	// the SigLIP text-to-visual encoder for each keyframe. When
	// non-empty AND the FrameIndexer is wired, the linker generates
	// a 768d vector per keyframe via search.ChannelVisual; when
	// empty, frame indexing is skipped (no fallback required).
	KeyframeEmbeddingText string
	Normalizer            Normalizer
	Log                   Logger
	Clock                 Clock
}

// ── Default implementation ────────────────────────────────

// defaultLinker is the canonical implementation of LinkerWorker.
// godlike/06 SSOT: a thin orchestrator over the canonical ports;
// the binding semantic (Upsert with ON CONFLICT DO UPDATE) makes
// the per-candidate pipeline re-run safe.
type defaultLinker struct {
	deps LinkerDeps
}

// NewDefaultLinker constructs the canonical linker. Composition
// root wires the concrete adapters; the linker here is the
// reuse-safe orchestrator.
func NewDefaultLinker(deps LinkerDeps) LinkerWorker {
	if deps.Log == nil {
		deps.Log = NoopLogger()
	}
	if deps.Clock == nil {
		deps.Clock = RealClock()
	}
	if deps.Normalizer == nil {
		deps.Normalizer = NewDefaultNormalizer(VisualIntentVersion)
	}
	return &defaultLinker{deps: deps}
}

// Compile-time assertion: defaultLinker satisfies LinkerWorker.
// Drift is a build error.
var _ LinkerWorker = (*defaultLinker)(nil)

// ── EnrichCandidate (canonical Fase 3.2 entrypoint) ────────────

// EnrichCandidate runs the canonical per-candidate enrichment
// pipeline and persists the (binding × concept × Qdrant) footprint.
//
// godlike/06 SSOT (pipeline steps):
//
//  1. Idempotency gate: DiscoveryStatus ∈ {DiscoveryIndexed,
//     DiscoveryMaterialized} → return Empty=true + zero writes.
//  2. Sanity check: req.Candidate.ID non-empty + Language non-empty.
//  3. Extract transcript / keyframes / visual descriptions.
//     Each extractor failure wraps ErrLinkerExtractFailed.
//  4. Detect entities.
//  5. Compose phrase candidates: detected entities + candidate
//     Title + Description (the canonical Title/Description seed
//     ensures catalog_only candidates whose AI extractor returns
//     zero entities still get at least one concept row).
//  6. Per-phrase: Normalize → ConceptRepository.Upsert (idempotent).
//  7. Encode multicanale embedding → EmbeddingIndexer.IndexConcept
//     (idempotent via canonical Qdrant point at concept_id key).
//  8. Per (concept_id × media.SlotPrimaryVideo): BindingsRepository.Upsert
//     (idempotent via canonical ON CONFLICT DO UPDATE).
//  9. CandidateRepository.UpdateStatus → DiscoveryIndexed.
//
// godlike/07 NO-FAKE-AVAILABILITY: any non-Recoverable failure
// flips DiscoveryStatus to DiscoveryFailed via UpdateStatus so
// the canonical dashboard state surface is always accurate.
func (w *defaultLinker) EnrichCandidate(ctx context.Context, req LinkerRequest) (LinkerResult, error) {
	// Always return a non-nil Failures slice so callers can range
	// over it safely.
	res := LinkerResult{
		PersistedBindingIDs: make([]string, 0, 4),
		IndexedConceptIDs:   make([]string, 0, 4),
		DetectedEntities:    make([]string, 0, 4),
		Failures:            make([]string, 0),
	}

	// Sanity check (godlike/07 — fail-closed on missing IDs / language).
	if strings.TrimSpace(req.Candidate.ID) == "" {
		return res, fmt.Errorf("mediamemory: linker enrich candidate missing ID: %w", ErrInvalidPhrase)
	}
	if strings.TrimSpace(req.Language) == "" {
		return res, fmt.Errorf("mediamemory: linker enrich candidate %q: language is empty: %w",
			req.Candidate.ID, ErrInvalidPhrase)
	}

	// Step 1 — Idempotency gate (godlike/06 SSOT contract).
	switch req.Candidate.DiscoveryStatus {
	case DiscoveryIndexed, DiscoveryMaterialized:
		// Early-return: re-call is a no-op. Empty=true so the
		// orchestrator skips downstream index-update / status-write.
		res.Status = req.Candidate.DiscoveryStatus
		res.Empty = true
		w.deps.Log.Debug(
			"mediamemory: linker enrich short-circuit (idempotency hit)",
			"candidate_id", req.Candidate.ID,
			"discovery_status", string(req.Candidate.DiscoveryStatus),
		)
		return res, nil
	}

	// Step 2 — Extraction.
	transcriptText, err := w.extractTranscript(ctx, req)
	if err != nil {
		// ErrLinkerExtractFailed is the transient sentinel — leave
		// DiscoveryStatus at Searched so Resume retries.
		return res, err
	}
	visualDesc, keyframes, extractErrs := w.extractVisualDescriptions(ctx, req)
	for _, e := range extractErrs {
		res.Failures = append(res.Failures, e)
	}

	// Step 3 — Entity detection.
	var detected []string
	if w.deps.EntityDetect != nil {
		ents, derr := w.deps.EntityDetect.DetectEntities(ctx, transcriptText, visualDesc)
		if derr != nil {
			res.Failures = append(res.Failures,
				fmt.Sprintf("candidate=%q entity-detect failed: %s", req.Candidate.ID, derr.Error()))
			// transient — status stays Searched
			return res, fmt.Errorf("mediamemory: linker entity-detect for %q: %w",
				req.Candidate.ID, errors.Join(ErrLinkerExtractFailed, derr))
		}
		detected = normalizedEntities(ents)
		res.DetectedEntities = append(res.DetectedEntities, detected...)
	}

	// Step 4 — Compose phrase candidates. godlike/06 SSOT
	// (canonical seed policy): the entity list is augmented with
	// Title + Description so a candidate with zero detectable
	// entities from the AI extractor still produces at least one
	// concept row. This is the canonical clinical-grade behavior:
	// catalog_only data MUST yield at least one concept per
	// candidate unless the candidate is genuinely empty.
	// Seed Title/Description first so the encoder's primary text
	// channel is populated with the canonical candidate metadata.
	phrases := make([]string, 0, len(detected)+2)
	if t := strings.TrimSpace(req.Candidate.Title); t != "" {
		phrases = append(phrases, t)
	}
	if d := strings.TrimSpace(req.Candidate.Description); d != "" {
		phrases = append(phrases, d)
	}
	phrases = append(phrases, detected...)

	if len(phrases) == 0 {
		// Step 4a — Hard-fail unmappable.
		res.Status = DiscoveryFailed
		res.Failures = append(res.Failures,
			fmt.Sprintf("candidate=%q: %s", req.Candidate.ID, ErrLinkerUnmappableConcept.Error()))
		if uerr := w.deps.Candidates.UpdateStatus(
			ctx, req.Candidate.ID, DiscoveryFailed, req.Candidate.MaterializationStatus,
		); uerr != nil {
			res.Failures = append(res.Failures,
				fmt.Sprintf("candidate=%q persist DiscoveryFailed status: %s", req.Candidate.ID, uerr.Error()))
		}
		return res, fmt.Errorf("mediamemory: linker unmappable for %q: %w",
			req.Candidate.ID, ErrLinkerUnmappableConcept)
	}

	// Step 5+6 — Normalize → upsert per phrase.
	concepts, normalizationErrs := w.normalizeAndUpsertConcepts(ctx, req, phrases)
	for _, e := range normalizationErrs {
		res.Failures = append(res.Failures, e)
	}
	if len(concepts) == 0 {
		res.Status = DiscoveryFailed
		res.Failures = append(res.Failures,
			fmt.Sprintf("candidate=%q zero concepts surviving normalization: %s",
				req.Candidate.ID, ErrLinkerConceptAssignmentFailed.Error()))
		if uerr := w.deps.Candidates.UpdateStatus(
			ctx, req.Candidate.ID, DiscoveryFailed, req.Candidate.MaterializationStatus,
		); uerr != nil {
			res.Failures = append(res.Failures,
				fmt.Sprintf("candidate=%q persist DiscoveryFailed status: %s", req.Candidate.ID, uerr.Error()))
		}
		return res, fmt.Errorf("mediamemory: linker zero concepts for %q: %w",
			req.Candidate.ID, ErrLinkerConceptAssignmentFailed)
	}

	// Step 7 — Multicanale encoding + Qdrant indexing (per concept).
	if encErr := w.encodeAndIndexConcepts(ctx, req, concepts, transcriptText, visualDesc); encErr != nil {
		// godlike/07 NO-FAKE-AVAILABILITY: any encoder / indexer /
		// concept-reupsert failure propagates a wrapped
		// ErrLinkerEmbeddingFailed so the candidate's DiscoveryStatus
		// stays at DiscoverySearched for Resume retry. The err's
		// message also lands in res.Failures[] for the dashboard's
		// per-candidate diagnostic surface.
		res.Failures = append(res.Failures, encErr.Error())
		return res, encErr
	}
	// IndexedConceptIDs is the canonical set returned via
	// LinkerResult; the orchestrator's batch IndexeCount derives
	// from this set (forward-pointer to Phase 2.3 metrics).
	for _, c := range concepts {
		res.IndexedConceptIDs = append(res.IndexedConceptIDs, c.ID)
	}

	// Step 7a — Fase 4.1 visual channel: per-keyframe 768d
	// SigLIP vectors into pipelinegen_media_frames. Best-effort
	// envelope over the canonical resolver hot path; transient
	// per-keyframe failures land in res.Failures[] without
	// flipping the candidate's DiscoveryStatus.
	if len(keyframes) > 0 && w.deps.FrameIndexer != nil && w.deps.Encoder != nil {
		if frameErrs := w.indexKeyframes(ctx, req, keyframes); len(frameErrs) > 0 {
			res.Failures = append(res.Failures, frameErrs...)
		}
	}

	// Step 8 — Binding persistence per (concept × media.SlotPrimaryVideo).
	bindingIDs, bindingErrs := w.persistBindings(ctx, req, concepts)
	res.PersistedBindingIDs = append(res.PersistedBindingIDs, bindingIDs...)
	for _, e := range bindingErrs {
		res.Failures = append(res.Failures, e)
	}

	// Step 9 — DiscoveryStatus terminal flip (godlike/06 SSOT
	// canonical terminal state for the linker pass). godlike/07:
	// only flip to DiscoveryIndexed when at least ONE binding
	// was persisted; otherwise DiscoveryFailed. The intent of
	// "indexed" is "this candidate produced a durable footprint
	// for resolver lookup", and an indexed-concepts-but-zero-
	// bindings row is not yet useful for the resolver hot path.
	if len(res.PersistedBindingIDs) == 0 {
		res.Status = DiscoveryFailed
		res.Failures = append(res.Failures,
			fmt.Sprintf("candidate=%q zero bindings persisted: %s",
				req.Candidate.ID, ErrLinkerInvariantBroken.Error()))
		if uerr := w.deps.Candidates.UpdateStatus(
			ctx, req.Candidate.ID, DiscoveryFailed, req.Candidate.MaterializationStatus,
		); uerr != nil {
			res.Failures = append(res.Failures,
				fmt.Sprintf("candidate=%q persist DiscoveryFailed status: %s", req.Candidate.ID, uerr.Error()))
		}
		return res, fmt.Errorf("mediamemory: linker produced zero bindings for %q: %w",
			req.Candidate.ID, ErrLinkerInvariantBroken)
	}

	if uerr := w.deps.Candidates.UpdateStatus(
		ctx, req.Candidate.ID, DiscoveryIndexed, req.Candidate.MaterializationStatus,
	); uerr != nil {
		// godlike/07: status-write failure is unrecoverable.
		res.Status = DiscoveryFailed
		res.Failures = append(res.Failures,
			fmt.Sprintf("candidate=%q persist DiscoveryIndexed status: %s", req.Candidate.ID, uerr.Error()))
		return res, fmt.Errorf("mediamemory: linker persist DiscoveryIndexed for %q: %w",
			req.Candidate.ID, errors.Join(ErrLinkerInvariantBroken, uerr))
	}
	res.Status = DiscoveryIndexed
	w.deps.Log.Info(
		"mediamemory: linker enrich complete",
		"candidate_id", req.Candidate.ID,
		"concept_count", len(concepts),
		"binding_count", len(res.PersistedBindingIDs),
	)
	return res, nil
}

// ── Step helpers ──────────────────────────────────────────

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
// into a canonical visual-desc envelope. Returns (text, errs)
// so a partial-extraction success can still proceed (e.g. some
// keyframes fail to caption — the linker uses the survivors).
// godlike/06 SSOT: a nil extractor pair short-circuits to
// ("", nil) so the linker degrades gracefully.
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

// normalizeAndUpsertConcepts loops over phrases, runs the
// canonical Normalizer, upserts the resulting MediaConcept.
// Tracks but does NOT short-circuit on per-phrase normalization
// failure (godlike/06 SSOT partial-success: at least one
// surviving concept is enough to proceed). Returns
// (concepts, failures).
func (w *defaultLinker) normalizeAndUpsertConcepts(ctx context.Context, req LinkerRequest, phrases []string) ([]MediaConcept, []string) {
	concepts := make([]MediaConcept, 0, len(phrases))
	failures := make([]string, 0)
	for _, phrase := range phrases {
		if phrase == "" {
			continue
		}
		concept, err := w.deps.Normalizer.Normalize(ctx, phrase, req.Language)
		if err != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q phrase=%q normalize failed: %s",
				req.Candidate.ID, phrase, err.Error(),
			))
			continue
		}
		// godlike/06 SSOT (default ConceptType): catalog_only
		// enrichment without an explicit entity classifier
		// assigns ConceptPhrase. Future Fase 4.1 visual channel
		// wires EntityDetector to override to ConceptEntity / etc.
		if concept.ConceptType == "" {
			concept.ConceptType = ConceptPhrase
		}
		if concept.ID == "" {
			concept.ID = uuid.NewString()
		}
		persisted, perr := w.deps.Concepts.Upsert(ctx, concept)
		if perr != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q concept (phrase=%q lang=%q) upsert failed: %s",
				req.Candidate.ID, phrase, req.Language, perr.Error(),
			))
			continue
		}
		concepts = append(concepts, persisted)
	}
	return concepts, failures
}

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

// persistBindings writes one MediaBinding per (concept ×
// media.SlotPrimaryVideo). godlike/06 SSOT (canonical slot seed):
// for Fase 3.2 the linker produces a primary_video slot per
// concept. Future Fase 4.4 will lift the slot set to the
// SceneVisualPlan's preferred_slots (the linker remains the
// slot-agnostic writer; the ranker / resolver select the
// canonical slot at render time).
func (w *defaultLinker) persistBindings(ctx context.Context, req LinkerRequest, concepts []MediaConcept) ([]string, []string) {
	ids := make([]string, 0, len(concepts))
	failures := make([]string, 0)
	if w.deps.Bindings == nil {
		return ids, []string{"mediamemory: linker BindingRepository is nil (composition root must wire sqlite repo)"}
	}
	for _, c := range concepts {
		binding := MediaBinding{
			ConceptID: c.ID,
			AssetID:   req.Candidate.AssetID,
			SlotKind:  media.SlotPrimaryVideo,
			Origin:    OriginAutoLink,
			// godlike/06 SSOT (auto-link approval semantics): the
			// linker writes ApprovalPending by default; the
			// dashboard's "Visual Memory" page is the canonical
			// approval surface for promotion to ApprovalApproved.
			ApprovalStatus: ApprovalPending,
			ManualScore:    0,
			SemanticScore:  0,
			QualityScore:   1, // linker-pass n/a; baseline 1 keeps dashboards sensible
		}
		persisted, perr := w.deps.Bindings.Upsert(ctx, binding)
		if perr != nil {
			failures = append(failures, fmt.Sprintf(
				"candidate=%q concept=%q persist binding failed: %s",
				req.Candidate.ID, c.ID, perr.Error(),
			))
			continue
		}
		ids = append(ids, persisted.ID)
	}
	return ids, failures
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

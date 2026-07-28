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
// Phase decomposition (godlike/06 SSOT, Pattern 5 single-purpose
// slice): the per-candidate pipeline is split across 3 phase
// files in this package; this file owns ONLY the orchestrator
// (EnrichCandidate) and the shared dependency bundle + struct:
//
//   - linker_resolve.go — resolve phase (extractTranscript +
//     extractVisualDescriptions + normalizedEntities).
//   - linker_link.go    — link phase (normalizeAndUpsertConcepts
//   - persistBindings).
//   - linker_emit.go    — emit phase (encodeAndIndexConcepts +
//     indexKeyframes).
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
)

// ── Dependency bundle ────────────────────────────────────────

// LinkerDeps is the canonical dependency bundle for
// LinkerWorker. godlike/06 SSOT (narrow port doctrine): the
// linker composes only the ports it actually consumes.
// Composition root wires concrete adapters (transcriber /
// keyframe-puller / visual-describer / entity-ner / text-encoder /
// sqlite repos / qdrant-indexer / canonical normalizer).
type LinkerExtractionDeps struct {
	Transcript   TranscriptExtractor
	Keyframe     KeyframeExtractor
	VisualGen    VisualDescriptionGenerator
	EntityDetect EntityDetector
}

type LinkerStorageDeps struct {
	Encoder    EmbeddingEncoder
	Concepts   ConceptRepository
	Bindings   BindingRepository
	Candidates CandidateRepository
	Indexer    EmbeddingIndexer
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
}

type LinkerDeps struct {
	LinkerExtractionDeps
	LinkerStorageDeps
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
//
// Phase dispatch: the per-step helpers live in 3 phase files
// (linker_resolve.go for resolve, linker_link.go for link,
// linker_emit.go for emit); this method is the canonical
// orchestrator that composes them in canonical order.
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

	// Step 2 — Extraction (linker_resolve.go).
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

	// Step 5+6 — Normalize → upsert per phrase (linker_link.go).
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

	// Step 7 — Multicanale encoding + Qdrant indexing (per concept)
	// (linker_emit.go).
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
	// flipping the candidate's DiscoveryStatus (linker_emit.go).
	if len(keyframes) > 0 && w.deps.FrameIndexer != nil && w.deps.Encoder != nil {
		if frameErrs := w.indexKeyframes(ctx, req, keyframes); len(frameErrs) > 0 {
			res.Failures = append(res.Failures, frameErrs...)
		}
	}

	// Step 8 — Binding persistence per (concept × media.SlotPrimaryVideo)
	// (linker_link.go).
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

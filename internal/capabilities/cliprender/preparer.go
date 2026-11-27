package cliprender

// preparer.go implements the parallel preparation phase (feature spec §3-§5,
// §8, §12): materialize source/watermark/background, resolve or generate the
// canonical transcript, and resolve the output contract — concurrently, with
// no artificial serial barriers.
//
// The fan-out is structured as two PARALLEL waves separated only by real data
// dependencies:
//
//	Wave 1 (parallel): resolve source asset, watermark asset, background
//	                   asset, transcript DB lookup, output contract.
//	Wave 2 (parallel): materialize source, watermark, background, and
//	                   generate the transcript when the reuse lookup missed.
//
// The transcript generation in wave 2 waits only on the source materialization
// (the one real dependency: Whisper needs the local source bytes) via a
// channel handoff — it never waits on watermark/background. Independent
// downloads always run concurrently; the anti-pattern the spec forbids
// ("download source → WAIT → download watermark → WAIT") is structurally
// impossible here.
//
// Each phase is implemented by one instrumented helper (instrumentedResolve /
// instrumentedMaterialize / instrumentedResolveContract /
// instrumentedTranscriptLookup / instrumentedTranscriptGenerate) that owns the
// phase's timing sample, its structured debug lines and its typed error.
// Prepare itself owns ONLY the fan-out: which phases run, and what each one
// waits on.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// ── Typed errors ─────────────────────────────────────────────────────

// noSpeechError is the concrete ErrNoSpeech value. It is a struct rather than
// fmt.Errorf so the message stays stable and Unwrap can expose the broader
// generation-unavailable sentinel.
type noSpeechError struct{}

func (noSpeechError) Error() string { return "clip.render: no speech detected in source audio" }

func (noSpeechError) Unwrap() error { return ErrTranscriptGenerationUnavailable }

var (
	// ErrTranscriptUnavailable is returned when the transcript policy is
	// "reuse" but no READY canonical track exists. Fail-closed: the pipeline
	// never invents a transcript or silently proceeds without one.
	ErrTranscriptUnavailable = errors.New("clip.render: transcript reuse requested but no READY track exists")
	// ErrTranscriptGenerationUnavailable is returned when generation is
	// required (reuse missed) but no generation source is available.
	ErrTranscriptGenerationUnavailable = errors.New("clip.render: transcript generation required but no generation source available")
	// ErrNoSpeech classifies a generation attempt that RAN and found no
	// speech (silent or music-only clip). It is intentionally distinct from an
	// infrastructure/Whisper failure: the ASR worked, there is simply nothing
	// to transcribe. It unwraps to ErrTranscriptGenerationUnavailable so
	// callers that already treat "no transcript" as fail-closed keep working
	// while operators (and the render failure projection) can tell the two
	// cases apart.
	ErrNoSpeech = noSpeechError{}
	// ErrMaterializerReturnedNoArtifact is the typed contract violation for an
	// AssetMaterializer that returns (nil, nil). The port promises a verified
	// artifact on success; a nil result is a defect, never a valid empty asset.
	ErrMaterializerReturnedNoArtifact = errors.New("clip.render: asset materializer returned no artifact")
	// ErrInputRevisionMismatch is the fail-closed gate for a request that pins
	// an input revision the resolved bytes do not satisfy. It is a correctness
	// error, not a transient one: retrying the same payload over the same
	// bytes cannot succeed, and rendering anyway would publish stale pixels.
	ErrInputRevisionMismatch = errors.New("clip.render: input revision mismatch")
)

// Preparer is the canonical parallel-preparation orchestrator. It is
// constructed with the narrow ports; the composition root wires the concrete
// adapters.
type Preparer struct {
	assets     AssetResolver
	material   AssetMaterializer
	transcript TranscriptResolver
	contract   ContractResolver
	log        *zap.Logger
}

// NewPreparer constructs the Preparer. Fail-closed: every mandatory port is
// required — a nil port is a wiring bug, never a silently-degraded path.
func NewPreparer(
	assets AssetResolver,
	material AssetMaterializer,
	transcript TranscriptResolver,
	contract ContractResolver,
	log *zap.Logger,
) (*Preparer, error) {
	if assets == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: AssetResolver is required")
	}
	if material == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: AssetMaterializer is required")
	}
	if transcript == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: TranscriptResolver is required")
	}
	if contract == nil {
		return nil, fmt.Errorf("cliprender.NewPreparer: ContractResolver is required")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &Preparer{assets: assets, material: material, transcript: transcript, contract: contract, log: log}, nil
}

// MaterializeAsset resolves a certified asset to local bytes without rendering
// anything. It is the sequence phase's source port: an assembly reads artifacts
// already produced, so it must never re-run a render.
func (p *Preparer) MaterializeAsset(ctx context.Context, assetID string) (*MaterializedAsset, error) {
	if p == nil || p.assets == nil || p.material == nil {
		return nil, errors.New("clip.render: asset resolver is not wired")
	}
	ref, err := p.assets.ResolveAsset(ctx, assetID)
	if err != nil {
		return nil, err
	}
	if ref == nil {
		return nil, fmt.Errorf("clip.render: asset %s resolved to nothing", assetID)
	}
	mat, err := p.material.Materialize(ctx, *ref)
	if err != nil {
		return nil, err
	}
	if mat == nil {
		return nil, fmt.Errorf("%w: asset %s", ErrMaterializerReturnedNoArtifact, assetID)
	}
	return mat, nil
}

// prepareRefs is the wave-1 result: resolved identities, contract and the
// optional canonical transcript lookup.
type prepareRefs struct {
	source        *AssetRef
	watermark     *AssetRef
	background    *AssetRef
	contract      *ResolvedContract
	existing      *TranscriptResult
	existingFound bool
}

// prepareMaterialized is the wave-2 result: local bytes and the generated
// transcript (when generation was required).
type prepareMaterialized struct {
	source     *MaterializedAsset
	watermark  *MaterializedAsset
	background *MaterializedAsset
	transcript *TranscriptResult
}

// Prepare runs the parallel preparation phase and returns the typed handoff
// to the render step. req is normalized + validated here (idempotent — the
// worker may already have normalized it). runID scopes the attempt.
//
// Prepare orchestrates the two waves; the observability and error contract of
// every individual phase lives in its instrumented helper, so the fan-out stays
// legible.
func (p *Preparer) Prepare(ctx context.Context, req *RenderRequest, runID string) (*Prepared, error) {
	start := time.Now()
	req.Normalize()
	if err := req.Validate(); err != nil {
		return nil, err
	}

	tracker := newTimingTracker()
	refs, err := p.resolvePrepareInputs(ctx, runID, req, tracker)
	if err != nil {
		return nil, err
	}
	// A revision pin that the RESOLVED identity already disproves fails before
	// a byte is downloaded. A ref with no content hash cannot disprove anything;
	// the materialized digest is checked after wave 2 instead.
	if err := validateResolvedRefRevisions(req, refs); err != nil {
		return nil, err
	}

	// Fail-fast: a strict `reuse` request whose READY track is absent is
	// already decided after wave 1. Starting wave 2 would download source,
	// watermark and background only to fail with the same typed error (CR-06).
	if !refs.existingFound && req.Transcript.Mode == TranscriptModeReuse {
		return nil, fmt.Errorf("%w: asset %q language %q", ErrTranscriptUnavailable, req.SourceAssetID, req.Transcript.Language)
	}

	// Resolve the background media family ONCE, before any byte is materialized:
	// it is a sealed-plan input (image vs video selects a different render
	// layer), so it is decided here and never inferred by the render worker from
	// a filename. resolveBackgroundKind owns the rule; a plate we cannot
	// classify fails closed.
	backgroundKind, err := resolvePreparedBackgroundKind(req, refs.background)
	if err != nil {
		return nil, err
	}

	mats, err := p.materializePrepareInputs(ctx, runID, req, refs, tracker)
	if err != nil {
		return nil, err
	}

	// ── Post-wave resolution ────────────────────────────────────────────
	if !refs.existingFound && req.Transcript.Mode == TranscriptModeReuse {
		return nil, fmt.Errorf("%w: asset %q language %q", ErrTranscriptUnavailable, req.SourceAssetID, req.Transcript.Language)
	}
	transcript := mats.transcript
	if transcript == nil && refs.existingFound {
		transcript = refs.existing
	}
	if transcript == nil || !transcript.HasText() {
		return nil, fmt.Errorf("%w: asset %q language %q", ErrTranscriptGenerationUnavailable, req.SourceAssetID, req.Transcript.Language)
	}
	if err := validateInputRevisions(req, mats, transcript); err != nil {
		return nil, err
	}

	timings := tracker.finish(time.Since(start))
	return buildPrepared(req, runID, refs, mats, backgroundKind, transcript, timings, p.log), nil
}

// resolvePrepareInputs runs wave 1: identities, contract and transcript lookup
// in parallel. It returns only after every wave-1 phase has settled.
func (p *Preparer) resolvePrepareInputs(ctx context.Context, runID string, req *RenderRequest, tracker *timingTracker) (*prepareRefs, error) {
	refs := &prepareRefs{}
	watermarkEnabled := req.Watermark != nil && req.Watermark.Enabled
	backgroundAsset := req.Background != nil && req.Background.Mode == BackgroundModeAsset
	// `auto` and `reuse` look the canonical track up. `generate` is an explicit
	// manual repair that deliberately skips the lookup and re-runs ASR.
	lookupTranscript := req.Transcript.Mode == TranscriptModeReuse ||
		req.Transcript.Mode == TranscriptModeAuto

	var wave1 errgroup.Group
	wave1.Go(func() error {
		ref, err := p.instrumentedResolve(ctx, runID, "source", req.SourceAssetID, tracker, true)
		if err != nil {
			return err
		}
		refs.source = ref
		return nil
	})
	// Text watermarks are rendered directly by the compositor and do not
	// require an asset lookup/materialization. Only asset-backed watermarks
	// participate in the resolver waves.
	watermarkAsset := watermarkEnabled && req.Watermark != nil && strings.TrimSpace(req.Watermark.Text) == ""
	if watermarkAsset {
		wave1.Go(func() error {
			ref, err := p.instrumentedResolve(ctx, runID, "watermark", req.Watermark.AssetID, tracker, false)
			if err != nil {
				return err
			}
			refs.watermark = ref
			return nil
		})
	}
	if backgroundAsset {
		wave1.Go(func() error {
			ref, err := p.instrumentedResolve(ctx, runID, "background", req.Background.AssetID, tracker, false)
			if err != nil {
				return err
			}
			refs.background = ref
			return nil
		})
	}
	if lookupTranscript {
		wave1.Go(func() error {
			res, found, err := p.instrumentedTranscriptLookup(ctx, runID, req, tracker)
			if err != nil {
				return err
			}
			refs.existing, refs.existingFound = res, found
			return nil
		})
	}
	wave1.Go(func() error {
		c, err := p.instrumentedResolveContract(ctx, runID, req, tracker)
		if err != nil {
			return err
		}
		refs.contract = c
		return nil
	})
	if err := wave1.Wait(); err != nil {
		return nil, err
	}
	return refs, nil
}

// materializePrepareInputs runs wave 2: materialize the resolved bytes and
// generate the transcript when the lookup missed. The source gate releases the
// transcript generator as soon as the source bytes exist.
func (p *Preparer) materializePrepareInputs(ctx context.Context, runID string, req *RenderRequest, refs *prepareRefs, tracker *timingTracker) (*prepareMaterialized, error) {
	mats := &prepareMaterialized{}

	// sourceReady is the one real data dependency: transcript generation
	// needs the materialized source bytes. Watermark/background never gate it.
	sourceReady := make(chan struct{})
	var sourceErr error

	// `generate` forces ASR; `auto` generates only when the READY lookup missed
	// (or returned an empty track); `reuse` never generates. The fallback is
	// explicit in the policy, not a silent upgrade of a strict reuse request.
	generateTranscript := req.Transcript.Mode == TranscriptModeGenerate ||
		(req.Transcript.Mode == TranscriptModeAuto && (!refs.existingFound || refs.existing == nil || !refs.existing.HasText()))

	var wave2 errgroup.Group
	wave2.Go(func() error {
		// The publish callback fires immediately after the materializer port
		// returns and BEFORE the phase is instrumented: the transcript
		// generator is released as soon as the source bytes exist, never
		// gated on the watermark/background downloads or on the phase logging.
		_, err := p.instrumentedMaterialize(ctx, runID, "source", refs.source, tracker, func(mat *MaterializedAsset, err error) {
			mats.source, sourceErr = mat, err
			close(sourceReady)
		})
		return err
	})
	if refs.watermark != nil {
		wave2.Go(func() error {
			mat, err := p.instrumentedMaterialize(ctx, runID, "watermark", refs.watermark, tracker, nil)
			if err != nil {
				return err
			}
			mats.watermark = mat
			return nil
		})
	}
	if refs.background != nil {
		wave2.Go(func() error {
			mat, err := p.instrumentedMaterialize(ctx, runID, "background", refs.background, tracker, nil)
			if err != nil {
				return err
			}
			mats.background = mat
			return nil
		})
	}
	if generateTranscript {
		wave2.Go(func() error {
			// Wait only on the real dependency (materialized source bytes).
			// The wait is a dependency, not work: the phase timing below
			// measures only the Generate call itself.
			<-sourceReady
			if sourceErr != nil {
				return fmt.Errorf("clip.render: transcript generation aborted: source materialization failed: %w", sourceErr)
			}
			res, err := p.instrumentedTranscriptGenerate(ctx, runID, req, mats.source, tracker)
			if err != nil {
				return err
			}
			mats.transcript = res
			return nil
		})
	}
	if err := wave2.Wait(); err != nil {
		return nil, err
	}
	return mats, nil
}

// validateResolvedRefRevisions is the wave-1 half of the pin gate: the asset
// registry hash and the existing transcript hash are already known, so a
// mismatch can fail before any download or ASR. An absent actual hash is not a
// mismatch here — the verified bytes are checked after materialization.
func validateResolvedRefRevisions(req *RenderRequest, refs *prepareRefs) error {
	if req == nil || refs == nil {
		return nil
	}
	checks := []struct {
		label  string
		pinned string
		actual string
	}{
		{"source", req.SourceRevision, refRevision(refs.source)},
		{"background", revisionOrEmpty(req.Background), refRevision(refs.background)},
		{"watermark", revisionOrEmptyWatermark(req.Watermark), refRevision(refs.watermark)},
		{"transcript", revisionOrEmptyTranscript(req.Transcript), transcriptRevision(refs.existing)},
	}
	for _, check := range checks {
		pinned := normalizeSHA256(check.pinned)
		actual := normalizeSHA256(check.actual)
		if pinned == "" || actual == "" {
			continue
		}
		if actual != pinned {
			return fmt.Errorf("%w: %s pinned %s but resolved %s", ErrInputRevisionMismatch, check.label, pinned, actual)
		}
	}
	return nil
}

func refRevision(ref *AssetRef) string {
	if ref == nil {
		return ""
	}
	return ref.ContentSHA256
}

// validateInputRevisions enforces the CRA-03 identity rule: a pin is a claim
// about the BYTES, so it is checked against the materialized/verified digest,
// not merely against the asset ID. A pin that cannot be verified (empty actual
// digest) fails closed rather than being treated as satisfied.
func validateInputRevisions(req *RenderRequest, mats *prepareMaterialized, transcript *TranscriptResult) error {
	if req == nil || mats == nil {
		return nil
	}
	checks := []struct {
		label  string
		pinned string
		actual string
	}{
		{"source", req.SourceRevision, materializedRevision(mats.source)},
		{"background", revisionOrEmpty(req.Background), materializedRevision(mats.background)},
		{"watermark", revisionOrEmptyWatermark(req.Watermark), materializedRevision(mats.watermark)},
		{"transcript", revisionOrEmptyTranscript(req.Transcript), transcriptRevision(transcript)},
	}
	for _, check := range checks {
		pinned := normalizeSHA256(check.pinned)
		if pinned == "" {
			continue
		}
		actual := normalizeSHA256(check.actual)
		if actual == "" || actual != pinned {
			return fmt.Errorf("%w: %s pinned %s but resolved %q", ErrInputRevisionMismatch, check.label, pinned, check.actual)
		}
	}
	return nil
}

func materializedRevision(asset *MaterializedAsset) string {
	if asset == nil {
		return ""
	}
	return asset.SHA256
}

func transcriptRevision(transcript *TranscriptResult) string {
	if transcript == nil {
		return ""
	}
	return transcript.TextSHA256
}

func resolvePreparedBackgroundKind(req *RenderRequest, ref *AssetRef) (string, error) {
	if ref == nil {
		return "", nil
	}
	return resolveBackgroundKind(req.Background.Kind, ref.AssetID, ref.MediaType)
}

// buildPrepared projects the two waves into the typed handoff and emits the
// per-phase aggregate line. Field names follow the phase names recorded by the
// tracker (e.g. resolve_source_ms, materialize_source_ms).
func buildPrepared(
	req *RenderRequest,
	runID string,
	refs *prepareRefs,
	mats *prepareMaterialized,
	backgroundKind string,
	transcript *TranscriptResult,
	timings PreparationTimings,
	log *zap.Logger,
) *Prepared {
	phaseFields := make([]zap.Field, 0, len(timings.Phases)+4)
	for _, p := range timings.Phases {
		phaseFields = append(phaseFields, zap.Int64(p.Phase+"_ms", p.WallMS))
	}
	phaseFields = append(phaseFields,
		zap.String("run_id", runID),
		zap.String("source_asset_id", req.SourceAssetID),
		zap.Int64("total_wall_ms", timings.TotalWallMS),
		zap.Int64("total_work_ms", timings.TotalWorkMS),
		zap.Bool("parallel", timings.Parallel),
		zap.Bool("transcript_reused", transcript.Reused),
	)
	// prepare.done carries the aggregate durations only; it is Debug like the
	// per-wave detail. Info stays reserved for the job-level milestones
	// (started → submitted → remote render completed → publish completed →
	// failed) so the operator log is not flooded by internal wave chatter.
	log.Debug("clip.render.prepare.done", phaseFields...)

	return &Prepared{
		RunID:          runID,
		Source:         mats.source,
		Watermark:      mats.watermark,
		Background:     mats.background,
		BackgroundKind: backgroundKind,
		Transcript:     transcript,
		Contract:       refs.contract,
		Timings:        timings,
	}
}

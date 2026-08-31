// Package voiceover — promo.go (P0-#3 atomic cutover, July 2026).
//
// Service.GeneratePromo delegates to the per-item use case via
// promoVoiceoverAdapter. The previous voiceoverGenBridge routed through
// the legacy Service.GenerateWithDestination; the cutover moves the
// promo path onto the canonical per-item pipeline
// (ProcessVoiceoverItemUseCase → ProcessSegmentUseCase) so:
//
//   - The promo path uses the SAME per-item pipeline runner the
//     voiceover.generate + voiceover.generate_item job paths use
//     (godlike/06 SSOT: one canonical per-item pipeline).
//   - Failures propagate as a typed Go error
//     (wraps ErrPromoVoiceoverGeneration) instead of the pre-fix
//     anti-pattern (Result{OK:false, Warnings:[...]}, nil) which
//     silently swallowed failures at the workflow/promo layer.
//   - The adapter is port-driven (depends on voiceover.VoiceoverItemExecutor,
//     NOT on *Service), so the promo path is testable via a stub
//     executor without standing up the full voiceover Service surface.
//
// Companion callers use VoiceoverItemExecutor directly; no positional
// Service generation API remains in the runtime.
package voiceover

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"

	domainvo "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/workflow/promo"
)

// ErrPromoVoiceoverGeneration is the typed sentinel wrapped by every
// real voiceover failure surfaced through the promo path. Callers
// (workflow/promo.Generator) can errors.Is against this sentinel to
// branch on real voiceover failures (vs translation failures, which
// are already typed via ErrTranslationFailed).
//
// Pre-P0-#3 the bridge returned (*domainvo.Result{OK:false}, nil)
// which HID failures from the upstream workflow — the per-language
// breakdown in the response was the only signal, and only when the
// caller explicitly checked `result.OK` instead of the Go error. The
// post-P0-#3 adapter returns (nil, err) wrapping
// ErrPromoVoiceoverGeneration so the workflow's `if voErr != nil`
// check at generate.go:151 correctly flips the response to OK=false
// and increments Failed — same path as translation failures, so
// dashboards light up uniformly.
var ErrPromoVoiceoverGeneration = errors.New("voiceover promo: per-item generation failed")

// GeneratePromo is the canonical entry point for the promo workflow
// (translate → generate per language). P0-#3: the per-language
// generation is delegated to the per-item use case via
// promoVoiceoverAdapter (port-driven; the adapter is constructed
// inline because it carries no state beyond the executor + log).
//
// Composition root contract: callers must pass the canonical
// per-item use case into voiceover.NewService via
// VoiceoverIntegrationDeps.ProcessItem (see build_bundles_voiceover.go).
// A nil executor at construction time surfaces here as a fail-closed
// error so the missing wire-up is fixed before deploy (godlike/07
// NO-FAKE-AVAILABILITY: a misconfigured composition root must NOT
// silently fall back to a no-op promo generation).
func (s *Service) GeneratePromo(ctx context.Context, req *promo.Request) (*promo.Response, error) {
	if s.translator == nil {
		return nil, fmt.Errorf("translator not configured")
	}
	if s.processItem == nil {
		return nil, fmt.Errorf("voiceover.Service.GeneratePromo: processItem use case not wired (P0-#3 cutover requires VoiceoverItemExecutor — composition root should pass processItemUseCase into voiceover.NewService)")
	}

	// The adapter is constructed inline because it carries no state
	// beyond the executor + log; the canonical per-item use case
	// (ProcessVoiceoverItemUseCase) is goroutine-safe so the adapter
	// is too. A future refactor can hoist the adapter to a struct
	// field if memoisation becomes a concern.
	gen := promo.NewGenerator(s.translator, &promoVoiceoverAdapter{
		executor: s.processItem,
		log:      s.log,
	}, s.log)

	return gen.Generate(ctx, req)
}

// promoVoiceoverAdapter adapts the canonical per-item use case
// (voiceover.VoiceoverItemExecutor) to the promo workflow's narrow
// VoiceoverGenerator port.
//
// P0-#3 (July 2026): the adapter uses the command-driven per-item
// pipeline and surfaces failures as typed Go errors. It:
//
//  1. Pre-computes TextHash via voiceover.ComputeTextHash (single
//     source of truth — same shape as the canonical fanout).
//  2. Generates a stable RequestID via buildRequestID (the canonical
//     per-batch correlation identifier; same shape as every other
//     voiceover path).
//  3. Maps the domain command (Text + Locale + Destination{FolderID})
//     to the canonical per-item command (Text + Language + Filename
//     + TextHash + RequestID + Destination{Kind:"explicit"} +
//     Strategy="replace" + RemoveSilence=false).
//  4. Calls the per-item use case's Execute method, which routes
//     through the SAME ProcessSegmentUseCase (TTS → AudioPost →
//     Publish → Finalize) the batch path uses — single canonical
//     per-item pipeline (godlike/06 SSOT).
//  5. On real failure: returns (nil, err) wrapping
//     ErrPromoVoiceoverGeneration. The workflow generator's
//     `if voErr != nil` check (generate.go:151) flips the response
//     to OK=false and increments Failed — same path as translation
//     failures.
//  6. On success: maps the typed *VoiceoverItemResult (DriveLink,
//     DriveFileID, Voice, LocalPath) to the workflow *domainvo.Result.
//
// ParentJobID contract: the promo path is synchronous within
// GeneratePromo (it does not enqueue per-language child jobs), so
// the dispatcher-assigned parent job ID is not available. We use
// the per-batch RequestID as a stable correlation identifier — same
// shape the dispatcher would emit. The aggregator + per-item
// parent-child wiring is reserved for the parent
// voiceover.generate job type (jobs/jobs/fanout.go).
type promoVoiceoverAdapter struct {
	executor VoiceoverItemExecutor
	log      *zap.Logger
}

// Generate implements promo.VoiceoverGenerator. The signature matches
// the workflow port exactly: (ctx, domainvo.GenerateVoiceoverCommand)
// → (*domainvo.Result, error). Errors are typed via
// ErrPromoVoiceoverGeneration (callers can errors.Is).
func (a *promoVoiceoverAdapter) Generate(ctx context.Context, cmd domainvo.GenerateVoiceoverCommand) (*domainvo.Result, error) {
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("%w: executor not wired (composition root must inject VoiceoverItemExecutor)", ErrPromoVoiceoverGeneration)
	}

	normalized := cmd.Normalize()
	if err := normalized.Validate(); err != nil {
		return nil, fmt.Errorf("%w: validate command: %v", ErrPromoVoiceoverGeneration, err)
	}

	requestID := buildRequestID()
	textHash := ComputeTextHash(normalized.Text)
	filename := normalized.Filename()

	// Map the domain command's Destination{FolderID} to the canonical
	// per-item DestinationRequest. Kind="explicit" signals the resolver
	// to use the caller-supplied FolderID verbatim (no GroupsResolver
	// call). The promo path never carries Group / SubfolderName /
	// StyleGroup.
	var dest *DestinationRequest
	if normalized.Destination.FolderID != "" {
		dest = &DestinationRequest{
			Kind:     "explicit",
			FolderID: normalized.Destination.FolderID,
		}
	}

	itemCmd := &GenerateVoiceoverItemCommand{
		// ParentJobID is the dispatcher-assigned ID at production;
		// the promo path is synchronous so we use the per-batch
		// RequestID as a stable correlation identifier (Validate
		// requires non-empty ParentJobID; the dispatcher would
		// normally populate it but the promo path bypasses the
		// dispatcher by design).
		ParentJobID: requestID,
		RequestID:   requestID,
		Text:        normalized.Text,
		Language:    Language(normalized.Locale),
		Voice:       normalized.Voice,
		Filename:    filename,
		TextHash:    textHash,
		Destination: dest,
		Strategy:    "replace", // canonical default for promo (matches pre-P0-#3 Service.GenerateWithDestination)
		// Keep promo voiceovers on the same permanent post-TTS cleanup
		// path as script scenes (silence threshold: 800 ms).
		RemoveSilence: true,
	}

	out, err := a.executor.Execute(ctx, itemCmd)
	if err != nil {
		// Typed-error contract: real failures surface as a Go error
		// (wrapping ErrPromoVoiceoverGeneration) instead of a
		// Result{OK:false} + nil error. The workflow's
		// `if voErr != nil` check at generate.go:151 correctly
		// flips the response to OK=false.
		return nil, fmt.Errorf("%w: %v", ErrPromoVoiceoverGeneration, err)
	}
	if out == nil {
		// Defensive: the per-item use case should return a non-nil
		// result even on failure (so the typed error from
		// ProcessSegmentUseCase stays informative). A nil result
		// without error is an invariant violation — surface as a
		// typed failure so the operator sees the data shape drift.
		return nil, fmt.Errorf("%w: per-item use case returned nil result without error (invariant violation)", ErrPromoVoiceoverGeneration)
	}
	if a.log != nil {
		a.log.Info("promoVoiceoverAdapter: per-item generation succeeded",
			zap.String("request_id", requestID),
			zap.String("language", normalized.Locale),
			zap.String("drive_link", out.DriveLink))
	}
	return &domainvo.Result{
		OK: true,
		Synthesis: domainvo.VoiceoverSynthesisResult{
			Locale:   normalized.Locale,
			Text:     normalized.Text,
			Voice:    out.Voice,
			Filename: filename,
		},
		DriveLink:   out.DriveLink,
		DriveFileID: out.DriveFileID,
		Status:      string(StatusCompleted),
		Warnings:    []string{},
	}, nil
}

// Compile-time assertion (AGENTS.md Pattern 0): promoVoiceoverAdapter
// must structurally satisfy the narrow promo.VoiceoverGenerator port.
// Drift between the adapter signature and the port contract triggers
// a compile error at this line — preventing silent drift on the
// wire contract.
var _ promo.VoiceoverGenerator = (*promoVoiceoverAdapter)(nil)

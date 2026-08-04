// Package voiceover — stages.go (PR-VO-STAGES-SPLIT, P0 #2 in
// VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-01).
//
// SLIM ORCHESTRATOR. The pre-split stages.go carried 3 stages in
// 628 LoC. Post-split:
//
//   - stage_synthesize.go   — Stage 1: TTS synthesis
//   - stage_postprocess.go  — Stage 2: audio post-processing (forward-pointer)
//   - stage_destination.go  — Stage 3: Drive upload via lifecycle.UploadOnly
//   - stage_persist.go      — Stage 4: in-tx atomic writes (forward-pointer)
//   - stage_finalize.go     — Stage 5: commit + post-commit verification
//
// This file retains ONLY the per-batch orchestrator (GenerateBatch)
// + the canonical restoreIdent constant (referenced by all stage
// files via the shared package scope). No behavior change in
// EXPAND — the batch pipeline (process.go::processLanguage) still
// calls synthesizeStage → destinationStage → finalizeStage in the
// same order as before the split.
//
// The 2 new stage files (stage_postprocess.go + stage_persist.go)
// are forward-pointers per the thinker's godlike/07 minimal-blast-
// radius recommendation: they exist as canonical seams for a future
// BACKFILL wave that will wire postprocessStage between
// synthesizeStage and destinationStage, and split finalizeStage's
// in-tx work into persistStage + a slim commit/verify wrapper.
//
// Identifier convention: the magic string "PR-VOICEOVER-RESTORE"
// surfaces in WARN/INFO log messages so an operator can grep for
// the restoration scope and verify which stage actually executed.
package voiceover

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// restoreIdent is the canonical one-shot identifier embedded in the
// per-stage log messages. Operators can `rg restoreIdent internal/`
// to enumerate all restored surfaces. Referenced by all 5 stage
// files via the shared package scope (no import needed).
const restoreIdent = "PR-VOICEOVER-RESTORE"

// GenerateBatch is the per-batch orchestrator called by the
// single-language wrappers (Generate, GenerateWithDestination) and
// the worker job handler (handleBatchJob).
//
// RESTORED body (June 2026):
//
//  1. Path-traversal rejection on req.Destination (preserved from
//     PR-VO-A4 so the contract test TestGenerateBatch_RejectsPathTraversalPayload
//     continues to fire fast-closed — Validate() runs BEFORE any
//     service-field access, only s.log is touched).
//  2. normalizeBatchRequest — fills defaults (template, strategy, lang).
//  3. Batch-level identifiers: requestID + textHash (computed once
//     for the batch — both are part of the row identity so they must
//     be stable across the per-language fan-out).
//  4. resolveDestination once (per req.Destination) — same folder +
//     StyleGroup for every language.
//  5. Per-language fan-out: processLanguage is the per-language
//     orchestrator (lives in process.go); it builds the BatchItem
//     and calls the 3 stages (synthesize → destination → finalize)
//     under stageLog telemetry wrappers.
//  6. Aggregate: response.OK = all items succeeded (otherwise false
//     so the caller can distinguish partial failure from full success).
//
// Why this is slim: each stage owns its own scope (synthesize /
// destination / finalize). GenerateBatch glues the per-language
// wiring only; the heavy lifting is in stage_*.go files.
func (s *Service) GenerateBatch(ctx context.Context, req *BatchRequest) (*BatchResponse, error) {
	// PR-VO-A4 (path-traversal rejection): validate the inbound
	// destination BEFORE any field access on req. The pre-PR8
	// process.go called this gate inside processLanguage per
	// language, but the canonical pre-PR8 GenerateBatch also called
	// it at the entrypoint (so the entire batch fails-closed on
	// the first traversal payload rather than per-language). The
	// test TestGenerateBatch_RejectsPathTraversalPayload pins this
	// contract.
	if req != nil && req.Destination != nil {
		if vErr := req.Destination.Validate(); vErr != nil {
			if s.log != nil {
				s.log.Warn("PR-VO-A4: GenerateBatch rejected path-traversal payload",
					zap.String("restored", restoreIdent),
					zap.String("subfolder_name", req.Destination.SubfolderName),
					zap.Error(vErr))
			}
			return nil, vErr
		}
	}

	// normalize into a local copy; SHALLOW-CLONE isolation pin — see pkg/immutability/copy.go godlike/06 SSOT.
	if req != nil {
		normalized := normalizeBatchRequest(*req)
		req = &normalized
	}

	if s.log != nil {
		s.log.Info("GenerateBatch entry", zap.String("project", req.Project), zap.Any("destination", req.Destination))
	}

	if req.Destination == nil && s.cfg != nil && s.cfg.Drive.VoiceoverFolder() != "" {
		req.Destination = &DestinationRequest{
			FolderID: s.cfg.Drive.VoiceoverFolder(),
		}
	}

	// P0.6 request_id threading: use the caller-supplied request_id
	// when available (threaded from API → CorrelationID → fanout →
	// child cmd.RequestID → BatchRequest.RequestID). Only generate a
	// new buildRequestID() when no caller ID is present (legacy
	// batch/promo paths that don't thread the ID yet).
	requestID := req.RequestID
	if requestID == "" {
		requestID = buildRequestID()
	}
	textHash := ComputeFullTextHash(req.Text)

	// PR-VO-AUDIT-P02 (June 2026): the legacy gate
	// `if req.Destination != nil` is REMOVED. The canonical destination
	// resolver (destination_resolver.go::ResolveVoiceoverDestination)
	// handles nil dest via its nil-dest branch which falls back to the
	// configured cfg.Drive.VoiceoverFolder() (or surfaces
	// ErrMissingFolder when no default is configured). Keeping the
	// gate here would silently restore the pre-PR8 bug where a
	// nil-Destination request fell through the worker-side path with
	// `dest = nil` and failed at Stage 2 with `missing_folder_id` even
	// though the cfg had a valid voiceover folder configured.
	var dest *ResolvedDestination
	d, err := s.resolveDestination(ctx, req.Destination)
	if err != nil {
		if s.log != nil {
			s.log.Warn("GenerateBatch: resolveDestination failed",
				zap.String("restored", restoreIdent),
				zap.Error(err))
		}
		code := ""
		if errors.Is(err, ErrVoiceoverDestinationUnavailable) {
			code = VoiceoverDestinationUnavailableCode
		}
		return &BatchResponse{
			OK:        false,
			Error:     fmt.Sprintf("GenerateBatch: resolve destination: %v", err),
			ErrorCode: code,
		}, fmt.Errorf("GenerateBatch: resolve destination: %w", err)
	}
	dest = d

	items := make([]BatchItem, 0, len(req.Languages))
	ok := true
	for _, lang := range req.Languages {
		// PR-VO-TYPED-PRIMITIVES (July 2026): req.Languages is now
		// []Language so `lang` is already typed (Language). The
		// processLanguage signature takes the typed value verbatim.
		item := s.processLanguage(ctx, requestID, textHash, lang, req, dest)
		if item.Status == StatusFailed {
			ok = false
		}
		items = append(items, item)
	}

	return &BatchResponse{
		OK:        ok,
		RequestID: requestID,
		Items:     items,
	}, nil
}

// Package voiceover — destination_helpers.go (PR-VO-DRY-PAIR,
// P0 #5 in VO-DECOMPOSITION-2026-07-04 wave, deadline 2026-08-15).
//
// Shared destination-resolution helpers consumed by BOTH the batch
// use case (usecase.go::GenerateVoiceoversUseCase) and the per-item
// use case (process_voiceover_item.go::ProcessVoiceoverItemUseCase).
//
// Pre-DRY both Execute methods carried ~30-line duplicate switch /
// if-else blocks for resolving a destination with a
// DefaultFolderResolver fallback. Post-DRY both callers route
// through the single free function below.
//
// godlike/06 SSOT: this file is the SINGLE canonical owner of the
// destination-with-default-fallback pattern. Future changes to the
// fallback precedence or the synthesized DestinationRequest shape
// live here.
//
// godlike/07 minimal-blast-radius: the function is a pure free
// function — no struct, no constructor. Callers pass the deps
// they already have (DestinationResolver, DefaultFolderResolver,
// Logger) without changing their UseCaseDeps shapes.
package voiceover

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
)

// ResolveDestinationWithFallback resolves the voiceover destination
// with a DefaultFolderResolver fallback. It is the canonical path
// consumed by both GenerateVoiceoversUseCase.Execute (batch) and
// ProcessVoiceoverItemUseCase.Execute (per-item).
//
// Resolution order:
//  1. destReq != nil → call destResolver.Resolve(ctx, destReq).
//     The caller's explicit destination always wins.
//  2. destReq == nil AND defaultResolver != nil → call
//     defaultResolver.Resolve(ctx). If ok && folderID != "",
//     synthesise a minimal DestinationRequest{FolderID: folderID}
//     and call destResolver.Resolve. Logs the fallback at Info
//     level so operators see the config-driven routing.
//  3. Otherwise → return (nil, nil). No destination is available;
//     the caller decides whether to fail (per-item path) or defer
//     the failure to per-item processing (batch path).
//
// Returns:
//   - (dest, nil)   — destination resolved successfully
//   - (nil, error)  — resolution failed (caller should surface the error)
//   - (nil, nil)    — no destination available (caller decides)
func ResolveDestinationWithFallback(
	ctx context.Context,
	destReq *DestinationRequest,
	destResolver DestinationResolver,
	defaultResolver VoiceoverDefaultFolderResolver,
	logger *zap.Logger,
) (*ResolvedDestination, error) {
	// Rule 1: any caller-supplied destination is authoritative. This
	// includes KindExplicit: an unavailable explicit request must fail,
	// never fall through to defaults or historical roots.
	if destReq != nil {
		if destResolver == nil {
			return nil, fmt.Errorf("%w: destination resolver is not configured", ErrVoiceoverDestinationUnavailable)
		}
		resolved, err := destResolver.Resolve(ctx, destReq)
		if err != nil {
			if errors.Is(err, ErrVoiceoverDestinationUnavailable) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: explicit destination resolver: %v", ErrVoiceoverDestinationUnavailable, err)
		}
		if resolved == nil || resolved.FolderID == "" {
			return nil, fmt.Errorf("%w: destination resolver returned no folder", ErrVoiceoverDestinationUnavailable)
		}
		return resolved, nil
	}

	// Rule 2: default folder fallback.
	if defaultResolver != nil {
		if destResolver == nil {
			return nil, fmt.Errorf("%w: destination resolver is not configured for default destination", ErrVoiceoverDestinationUnavailable)
		}
		folderID, localOutputDir, ok := defaultResolver.Resolve(ctx)
		if ok && folderID != "" {
			if logger != nil {
				logger.Info("destination fallback to voiceover default folder",
					zap.String("folder_id", folderID),
					zap.String("output_dir", localOutputDir))
			}
			// Synthesise a minimal DestinationRequest from the
			// resolved default folder so the downstream resolver
			// sees a uniform shape. FolderPath is threaded
			// verbatim — the batch path uses it as the TTS
			// output directory; the per-item path's
			// ResolveVoiceoverDestination routes it through
			// the direct() helper which preserves it.
			resolved, err := destResolver.Resolve(ctx, &DestinationRequest{
				FolderID:   folderID,
				FolderPath: localOutputDir,
			})
			if err != nil {
				if errors.Is(err, ErrVoiceoverDestinationUnavailable) {
					return nil, err
				}
				return nil, fmt.Errorf("%w: configured default destination resolver: %v", ErrVoiceoverDestinationUnavailable, err)
			}
			if resolved == nil || resolved.FolderID == "" {
				return nil, fmt.Errorf("%w: configured default resolved to no folder", ErrVoiceoverDestinationUnavailable)
			}
			return resolved, nil
		}
	}

	// Rule 3: no destination available. This is a hard, typed failure;
	// callers must not interpret it as permission to use another root.
	return nil, fmt.Errorf("%w: no destination supplied or configured", ErrVoiceoverDestinationUnavailable)
}

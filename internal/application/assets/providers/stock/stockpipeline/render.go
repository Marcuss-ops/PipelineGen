// Package stockpipeline — render.go refactored (PR6, June 2026).
//
// Pre-PR6: renderChunk lived inside the application package and directly
// constructed the FFmpeg filter_complex chain, the 14-string transitions
// table, and the per-codec encoding arguments. It also called process.Run
// to dispatch the resulting command line. All FFmpeg + execution knowledge
// leaked into the application layer (violates AGENTS.md Pattern 0 + 8).
//
// Post-PR6: this file is a PURE DECISION MODULE. It inspects the
// application-side rendering policy (clips, every-Nth transitions, every-
// Nth overlays, encoding params) and produces a neutral
// `stock.RenderRequest` that the canonical `StockRenderer` port consumes.
// The FFmpeg-specific code lives in
// `internal/infrastructure/media/render/ffmpeg.go` (see package docs).
//
// Import-boundary invariant:
//
//	go vet ./internal/application/assets/providers/stock/...
//
// must NOT import `internal/infrastructure/media/ffmpeg` OR
// `internal/infrastructure/process`. This file respects the invariant.
package stockpipeline

import (
	"context"
	"fmt"

	"go.uber.org/zap"
)

// renderChunk is the app-layer decision entry point for chunk rendering.
// It translates the application flags (noTransitions, noEffects, noAudio)
// into a generic stock.RenderRequest and delegates execution to s.renderer.
//
// Behavioural equivalence with pre-PR6:
//   - noTransitions && noEffects → renderer picks concat-demuxer fast path
//   - noTransitions only OR noEffects only → filter_complex with one branch
//   - full policy → filter_complex with Nth-clip transitions + Nth-clip overlays
//
// Titles are currently unused by the FFmpeg impl (kept in the signature
// for downstream API compatibility — the future subtitle overlay path will
// consume them).
func (s *Service) renderChunk(ctx context.Context, clips []string, titles []string, outputPath string, noTransitions, noEffects, noAudio bool, chunkIdx int) error {
	if len(clips) == 0 {
		return fmt.Errorf("renderChunk: no clips to render")
	}
	if s.renderer == nil {
		return fmt.Errorf("renderChunk: StockRenderer port is nil — was composition root build correct?")
	}

	req := RenderRequest{
		OutputPath:       outputPath,
		InputPaths:       clips,
		Width:            s.pcfg.Width,
		Height:           s.pcfg.Height,
		FPS:              s.pcfg.FPS,
		Codec:            s.pcfg.Codec,
		Preset:           s.pcfg.Preset,
		CRF:              s.pcfg.CRF,
		KeyframeInterval: s.pcfg.KeyframeInterval,
		KeepAudio:        !noAudio,

		NoTransitions:   noTransitions,
		TransitionEvery: s.pcfg.TransitionInterval,
		ClipDurationSec: s.pcfg.ClipDuration,

		NoEffects:       noEffects,
		EffectsDir:      s.pcfg.EffectsDir,
		EffectEvery:     s.pcfg.EffectInterval,
		EffectIndexHint: chunkIdx % 1024, // deterministic hint (mod 1024 avoids overflow on big indexes)
		OverlayOpacity:  s.pcfg.OverlayOpacity,

		Logger:     s.log,
		ChunkIndex: chunkIdx,
	}

	res, err := s.renderer.Render(ctx, req)
	if err != nil {
		return fmt.Errorf("renderChunk port failed: %w", err)
	}

	// Telemetry projection: the result carries operational metrics the
	// caller may want for logging downstream (which transitions actually
	// fired, how long it took). This data is informational only; the
	// rendered file at req.OutputPath is the primary artifact.
	s.log.Info("stock render: completed via port",
		zap.Int("chunk", chunkIdx),
		zap.Bool("fast_path", res.UsedFastPath),
		zap.Int64("duration_ms", res.DurationMS),
		zap.Strings("transitions", res.AppliedTransitions),
		zap.Strings("overlays", res.AppliedOverlayFiles),
	)
	return nil
}

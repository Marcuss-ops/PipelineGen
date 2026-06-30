// Package voiceover — promo workflow moved to workflow/promo (PR 6, June 2026).
// GeneratePromo delegates to the workflow package.
//
// BLOC5.3 commit-1-consumer-cutover (June 2026): voiceoverGenBridge was
// rewritten so the canonical promo generator no longer reaches Service.Generate
// (the legacy path that threads through Service.GenerateBatch → processLanguage).
// The bridge now delegates synchronously to ProcessVoiceoverItemUseCase —
// the SAME canonical per-item pipeline that the async child worker
// (jobs/GenerateItemJobHandler) consumes. master-plan rule: "promo deve
// accodare voiceover.generate non un secondo TTS" — the canonical child
// pipeline (TTS → AudioPost → Lifecycle/Upload → SwapTx + Outbox) is
// reused; promo does NOT own a second TTS orchestrator.
package voiceover

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	domainvo "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
	"go.uber.org/zap"
)

func (s *Service) GeneratePromo(ctx context.Context, req *PromoRequest) (*PromoResponse, error) {
	if s.translator == nil {
		return nil, fmt.Errorf("translator not configured")
	}

	// BLOC5.3 commit-1: bridge delegates to the canonical per-item
	// pipeline (ProcessVoiceoverItemUseCase), NOT to Service.Generate.
	// Fail-fast: if the composition root did not wire the use case
	// (older bundles pre-BLOC5.3), surface the wiring gap as a typed
	// error instead of silently falling back to the legacy path.
	voGen := &voiceoverGenBridge{
		processItemUseCase: s.processItemUseCase,
		log:                s.log,
	}
	gen := promo.NewGenerator(s.translator, voGen, s.log)

	result, err := gen.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// voiceoverGenBridge adapts ProcessVoiceoverItemUseCase to the
// promo.VoiceoverGenerator narrow port. BLOC5.3 commit-1 replace the
// legacy *Service.Generate adapter (which routed through Service.GenerateBatch)
// with the canonical per-item use case so the promo generator's
// per-language iteration reaches the SAME pipeline as the async
// child worker (jobs/GenerateItemJobHandler) — single canonical pipeline.
type voiceoverGenBridge struct {
	processItemUseCase VoiceoverItemExecutor
	log                *zap.Logger
}

func (b *voiceoverGenBridge) Generate(ctx context.Context, cmd domainvo.GenerateVoiceoverCommand) (*domainvo.Result, error) {
	if b == nil || b.processItemUseCase == nil {
		return nil, fmt.Errorf("voiceoverGenBridge: ProcessVoiceoverItemUseCase not wired (composition root must supply via VoiceoverGenerationDeps.ProcessItemUseCase)")
	}
	normalized := cmd.Normalize()
	if err := normalized.Validate(); err != nil {
		return nil, fmt.Errorf("voiceoverGenBridge: validate command: %w", err)
	}

	// Map domain command to the canonical per-item Command shape.
	// TextHash is sha256(text) hex — stable across the per-language
	// fan-out so every sibling writes the same text_hash into its row.
	textHash := sha256Hex(normalized.Text)
	itemCmd := &GenerateVoiceoverItemCommand{
		ParentJobID: "", // promo is sync-via-bridge; no parent aggregator; child row stands alone
		RequestID:   buildRequestID(), // package-private from types.go (was re-exported from process_one.go before its deletion in BLOC5.3 commit-2)
		Text:        normalized.Text,
		Language:    normalized.Locale,
		Voice:       normalized.Voice,
		Filename:    normalized.Filename(),
		TextHash:    textHash,
		Strategy:    asset.StrategyVerify,
		Metadata:    nil,
	}
	if normalized.Destination.FolderID != "" {
		itemCmd.Destination = &DestinationRequest{
			Kind:     "explicit",
			FolderID: normalized.Destination.FolderID,
		}
	}

	res, err := b.processItemUseCase.Execute(ctx, itemCmd)
	if err != nil {
		return nil, fmt.Errorf("voiceoverGenBridge: canonical pipeline execute: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("voiceoverGenBridge: canonical pipeline returned nil result (item_language=%q)", itemCmd.Language)
	}
	if res.Status == StatusFailed {
		// Surface the typed failure code as a Result envelope (OK=false +
		// Status="failed" + Warnings carrying the failure message). The
		// promo generator (workflow/promo) reads Result.DriveLink, so
		// leaving it empty here propagates to the per-language breakdown
		// in the response body. The audit pin Confirming-promo funnel
		// audit is that the typed error string from processItemUseCase
		// (e.g. "tts_failed: ...", "outbox_enqueue_failed: ...") is
		// preserved verbatim in Warnings[] so operators can grep the
		// typed failure code from response logs.
		return &domainvo.Result{
			OK:       false,
			ID:       res.ID,
			Locale:   res.Language,
			Text:     normalized.Text,
			Voice:    res.Voice,
			Filename: res.Filename,
			Status:   string(StatusFailed),
			Warnings: []string{fmt.Sprintf("voiceover canonical pipeline failed: %s", res.Error)},
		}, nil
	}
	if b.log != nil {
		b.log.Info("voiceoverGenBridge: canonical pipeline succeeded",
			zap.String("language", res.Language),
			zap.String("filename", res.Filename),
			zap.String("voiceover_id", res.ID))
	}
	return &domainvo.Result{
		OK:          true,
		ID:          res.ID,
		Locale:      res.Language,
		Voice:       res.Voice,
		Text:        normalized.Text,
		Filename:    res.Filename,
		LocalPath:   res.LocalPath,
		FileHash:    res.FileHash,
		DriveLink:   res.DriveLink,
		DriveFileID: res.DriveFileID,
		Status:      string(res.Status),
		Warnings:    []string{},
	}, nil
}

// sha256Hex returns the lowercase-hex SHA-256 of the input string.
// text_hash is the canonical per-batch fingerprint; identical texts
// across languages MUST collapse to identical hashes so sibling rows
// in the parent-child fan-out share the same content fingerprint.
func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

// Ensure bridge satisfies the interface at compile time.
var _ promo.VoiceoverGenerator = (*voiceoverGenBridge)(nil)

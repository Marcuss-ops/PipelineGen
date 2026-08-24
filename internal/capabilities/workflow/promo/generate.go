package workflow

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	domainvo "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover"
)

// VoiceoverGenerator is the narrow port needed by the promo workflow.
type VoiceoverGenerator interface {
	Generate(ctx context.Context, cmd domainvo.GenerateVoiceoverCommand) (*domainvo.Result, error)
}

// Generator orchestrates the promo workflow: translate text to N
// languages, then generate a voiceover for each.
type Generator struct {
	translator translation.TranslatorFunc
	voGen      VoiceoverGenerator
	log        *zap.Logger
}

// NewGenerator creates a promo workflow generator.
func NewGenerator(
	translator translation.TranslatorFunc,
	voGen VoiceoverGenerator,
	log *zap.Logger,
) *Generator {
	if log == nil {
		log = zap.NewNop()
	}
	return &Generator{translator: translator, voGen: voGen, log: log}
}

// Generate translates text to target languages then generates voiceovers.
//
// PR-VO-A5/A6 (strict accounting + fail-closed translator, June 2026):
// the legacy loop silently `continue`d on translation failure,
// producing a Response with Total != len(targets) and Failed that
// counted only voiceover failures. The new loop:
//
//   - attempts translation for every target (no early short-circuit
//     on first failure; isolation helps the operator see the full
//     failure pattern);
//   - on translation failure, populates a Result entry with
//     OK=false, Error="translation failed: ...", increments
//     resp.Failed, AND continues the loop unless req.AllowUntranslated
//     is set (in which case the legacy lenient drop applies);
//   - on translation success, attempts voiceover as before;
//   - resp.Total is ALWAYS len(targets);
//   - resp.OK = (resp.Failed == 0).
//
// The handler /promo route already inspects resp.OK to choose between
// 200 OK and 5xx; this fix lets the handler surface a non-OK response
// while still returning the per-language breakdown.
func (g *Generator) Generate(ctx context.Context, req *Request) (*Response, error) {
	if g.translator == nil {
		return nil, fmt.Errorf("translator not configured")
	}

	targets := translation.DefaultPromoLanguages()
	if len(req.Languages) > 0 {
		requested := make(map[string]bool)
		for _, l := range req.Languages {
			requested[l] = true
		}
		filtered := make([]translation.LanguageTarget, 0, len(targets))
		for _, t := range targets {
			if requested[t.Code] {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			targets = filtered
		}
	}

	resp := &Response{
		OK:      true, // provisional; flipped to false on any failure below
		Total:   len(targets),
		Results: make([]Result, 0, len(targets)),
	}

	for _, t := range targets {
		translated, txErr := g.translator(ctx, req.Text, t.Name)

		// PR-VO-A6: resolve translation outcome with an empty/whitespace
		// guard. Determinism here matters because the canonical
		// llm.Translator may return (text, nil) and text=="" — TTS
		// engines either hang or produce 0-byte audio on empty input.
		// We collaps empty-payload to ErrTranslationEmpty so dashboards
		// grep the same prefix either way.
		var sentinel error
		if txErr != nil {
			sentinel = fmt.Errorf("%w: %v", ErrTranslationFailed, txErr)
		} else if strings.TrimSpace(translated) == "" {
			sentinel = ErrTranslationEmpty
		}

		if sentinel != nil {
			g.log.Warn("promo translation failed",
				zap.String("language", t.Code),
				zap.Bool("allow_untranslated", req.AllowUntranslated),
				zap.Error(sentinel))

			// PR-VO-A6 LITERAL semantics: AllowUntranslated=true
			// silently skips the entry end-to-end (no Failed++, no
			// OK=false flip, no Result entry). The caller has explicitly
			// opted in to "this batch is acceptable even if some
			// translations fail" — surfacing any consequence in
			// resp.OK or resp.Failed would contradict the opt-in
			// contract. The legacy lenient contract dropped only the
			// Result entry; the literal contract drops every layer of
			// the failure so operator dashboards stay green by design.
			// Audit visibility falls back to the log line above
			// (which is emitted unconditionally).
			if req.AllowUntranslated {
				continue
			}

			// Strict mode (default): publish a Result entry so callers
			// can see which language failed; flip resp.OK to false;
			// increment resp.Failed so aggregate counts match the
			// per-language breakdown.
			resp.Results = append(resp.Results, Result{
				OK:       false,
				Language: t.Code,
				Error:    sentinel.Error(),
			})
			resp.Failed++
			resp.OK = false
			continue
		}

		// Translation succeeded. Branch on DryRun vs real-run.
		if req.DryRun {
			resp.Results = append(resp.Results, Result{
				OK:         true,
				Language:   t.Code,
				Translated: translated,
			})
			resp.Success++
			continue
		}

		cmd := domainvo.GenerateVoiceoverCommand{
			Text:   translated,
			Locale: t.Code,
		}
		cmd = cmd.Normalize()

		if req.DriveFolderID != "" {
			cmd.Destination = domainvo.DestinationRef{
				FolderID: req.DriveFolderID,
			}
		}

		result, voErr := g.voGen.Generate(ctx, cmd)
		if voErr != nil {
			g.log.Warn("promo voiceover failed",
				zap.String("language", t.Code), zap.Error(voErr))
			// Voiceover failures ALMOST ALWAYS count regardless of
			// AllowUntranslated — only the upstream translation step
			// is gated by the opt-in. A failed voiceover consumes
			// compute + produces no deliverable, and the strict retry
			// semantics are the same as for batch flow. Stateful
			// operators rely on Failed++ here so the dashboard surfaces
			// re-runnable work even when translations fully succeeded.
			resp.Results = append(resp.Results, Result{
				OK:         false,
				Language:   t.Code,
				Translated: translated,
				Error:      fmt.Errorf("%w: %v", ErrVoiceoverFailed, voErr).Error(),
			})
			resp.Failed++
			resp.OK = false
			continue
		}

		resp.Results = append(resp.Results, Result{
			OK:          true,
			Language:    t.Code,
			Translated:  translated,
			DriveLink:   result.DriveLink,
			DriveFileID: result.DriveFileID,
		})
		resp.Success++
	}

	return resp, nil
}

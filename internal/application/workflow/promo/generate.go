package promo

import (
	"context"
	"fmt"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	domainvo "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
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

	type translationResult struct {
		target     translation.LanguageTarget
		translated string
	}
	translations := make([]translationResult, 0, len(targets))
	for _, t := range targets {
		translated, err := g.translator(ctx, req.Text, t.Name)
		if err != nil {
			g.log.Warn("promo translation failed",
				zap.String("language", t.Code), zap.Error(err))
			continue
		}
		translations = append(translations, translationResult{target: t, translated: translated})
	}

	if len(translations) == 0 {
		return nil, fmt.Errorf("all translations failed")
	}

	resp := &Response{
		OK:      true,
		Total:   len(translations),
		Results: make([]Result, 0, len(translations)),
	}

	if req.DryRun {
		for _, tr := range translations {
			resp.Results = append(resp.Results, Result{
				OK:         true,
				Language:   tr.target.Code,
				Translated: tr.translated,
			})
			resp.Success++
		}
		return resp, nil
	}

	for _, tr := range translations {
		cmd := domainvo.GenerateVoiceoverCommand{
			Text:   tr.translated,
			Locale: tr.target.Code,
		}
		cmd = cmd.Normalize()

		if req.DriveFolderID != "" {
			cmd.Destination = domainvo.DestinationRef{
				FolderID: req.DriveFolderID,
			}
		}

		result, err := g.voGen.Generate(ctx, cmd)
		if err != nil {
			g.log.Warn("promo voiceover failed",
				zap.String("language", tr.target.Code), zap.Error(err))
			resp.Results = append(resp.Results, Result{
				OK:       false,
				Language: tr.target.Code,
				Error:    err.Error(),
			})
			resp.Failed++
			continue
		}

		resp.Results = append(resp.Results, Result{
			OK:          true,
			Language:    tr.target.Code,
			Translated:  tr.translated,
			DriveLink:   result.DriveLink,
			DriveFileID: result.DriveFileID,
		})
		resp.Success++
	}

	return resp, nil
}

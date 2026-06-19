package voiceover

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"
)

func (s *Service) GeneratePromo(ctx context.Context, req *PromoRequest) (*PromoResponse, error) {
	if s.translator == nil {
		return nil, fmt.Errorf("translator not configured")
	}

	targets := DefaultPromoLanguages()
	if len(req.Languages) > 0 {
		// Filter to requested languages
		requested := make(map[string]bool)
		for _, l := range req.Languages {
			requested[l] = true
		}
		filtered := make([]LanguageTarget, 0, len(targets))
		for _, t := range targets {
			if requested[t.Code] {
				filtered = append(filtered, t)
			}
		}
		if len(filtered) > 0 {
			targets = filtered
		}
	}

	// Step 1: Translate text to all target languages
	type translation struct {
		target     LanguageTarget
		translated string
	}
	translations := make([]translation, 0, len(targets))
	for _, t := range targets {
		translated, err := s.translator(ctx, req.Text, t.Name)
		if err != nil {
			s.log.Warn("promo translation failed",
				zap.String("language", t.Code), zap.Error(err))
			continue
		}
		translations = append(translations, translation{target: t, translated: translated})
	}

	if len(translations) == 0 {
		return nil, fmt.Errorf("all translations failed")
	}

	resp := &PromoResponse{
		OK:      true,
		Total:   len(translations),
		Results: make([]PromoResult, 0, len(translations)),
	}

	if req.DryRun {
		// Return translations only
		for _, tr := range translations {
			resp.Results = append(resp.Results, PromoResult{
				OK:         true,
				Language:   tr.target.Code,
				Translated: tr.translated,
			})
			resp.Success++
		}
		return resp, nil
	}

	// Step 2: Generate voiceover for each translation
	for _, tr := range translations {
		filename := fmt.Sprintf("promo_%s.mp3", strings.ReplaceAll(tr.target.Code, "-", "_"))

		var dest *DestinationRequest
		if req.DriveFolderID != "" {
			dest = &DestinationRequest{FolderID: req.DriveFolderID}
		}

		var result *VoiceoverResult
		var err error
		if dest != nil {
			result, err = s.GenerateWithDestination(ctx, tr.translated, tr.target.Code, filename, dest)
		} else {
			result, err = s.Generate(ctx, tr.translated, tr.target.Code, filename)
		}

		if err != nil {
			s.log.Warn("promo voiceover failed",
				zap.String("language", tr.target.Code), zap.Error(err))
			resp.Results = append(resp.Results, PromoResult{
				OK:       false,
				Language: tr.target.Code,
				Error:    err.Error(),
			})
			resp.Failed++
			continue
		}

		resp.Results = append(resp.Results, PromoResult{
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

// Cfg returns the config for external access (e.g. Drive folder).

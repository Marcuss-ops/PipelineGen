// Package usecase — document builder helpers.
//
// document_builder.go owns the Drive-folder recommendation pipeline:
// ResolveRecommendedDriveFolder. Extracted from flow_helpers.go
// (July 2026, LONG-FILES-SPLIT-2026-07-06).
package usecase

import (
	"context"
	"strings"

	"go.uber.org/zap"

	sliceutil "github.com/Marcuss-ops/PipelineGen/pkg/sliceutil"
)

// ── ResolveRecommendedDriveFolder ────────────────────────────────────────────

// ResolveRecommendedDriveFolder recommends a Drive folder for the script
// based on topic, keywords, and entities.
func ResolveRecommendedDriveFolder(ctx context.Context, svc ClipServices, title, script string, insights ScriptInsights) *ScriptDriveFolderSuggestion {
	if svc.AssocSvc == nil {
		return nil
	}

	keywords := make([]string, 0, 12)
	keywords = append(keywords, insights.ImportantWords...)
	keywords = append(keywords, insights.ImportantPhrases...)
	keywords = append(keywords, insights.ArtlistPhrases...)
	keywords = sliceutil.UniqueLimitedStrings(keywords, 12)

	entities := sliceutil.UniqueLimitedStrings(insights.SpecialNames, 12)
	req := AssociationCandidatesRequest{
		Topic:     strings.TrimSpace(title),
		Subject:   strings.TrimSpace(title),
		Narrative: strings.TrimSpace(script),
		Keywords:  keywords,
		Entities:  entities,
		TopK:      5,
	}
	resp, err := svc.AssocSvc.BuildCandidates(ctx, req)
	if err != nil || resp == nil || len(resp.Candidates) == 0 {
		return nil
	}

	for _, candidate := range resp.Candidates {
		folderID := strings.TrimSpace(candidate.FolderID)
		if folderID != "" && svc.DriveSvc != nil {
			active, checkErr := svc.DriveSvc.FileIsNotTrashed(ctx, folderID)
			if checkErr != nil {
				if svc.Logger != nil {
					svc.Logger.Warn("failed to check if drive folder is trashed, skipping",
						zap.String("folder_id", folderID),
						zap.String("folder_name", candidate.Name),
						zap.Error(checkErr),
					)
				}
				continue
			}
			if !active {
				if svc.Logger != nil {
					svc.Logger.Info("skipping trashed drive folder",
						zap.String("folder_id", folderID),
						zap.String("folder_name", candidate.Name),
					)
				}
				continue
			}
		}
		link := strings.TrimSpace(candidate.Link)
		if link == "" && folderID != "" {
			link = "https://drive.google.com/drive/folders/" + folderID
		}
		return &ScriptDriveFolderSuggestion{
			Database: strings.TrimSpace(candidate.Database),
			Source:   strings.TrimSpace(candidate.Source),
			Name:     strings.TrimSpace(candidate.Name),
			Path:     strings.TrimSpace(candidate.Path),
			Link:     link,
			FolderID: folderID,
			Score:    int(candidate.Score),
			Reason:   strings.TrimSpace(candidate.Reason),
		}
	}

	return nil
}

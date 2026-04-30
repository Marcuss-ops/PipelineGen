package script

import (
	"context"
	"strings"

	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
	"velox/go-master/pkg/models"
)

func modelClipsToScoredMatches(clips []models.Clip, reason, source, link string) []scoredMatch {
	matches := make([]scoredMatch, 0, len(clips))
	for _, clip := range clips {
		path := clip.LocalPath
		if path == "" {
			path = clip.FolderPath
		}
		matches = append(matches, scoredMatch{
			Title:   clip.Name,
			Path:    path,
			Source:  source,
			Link:    link,
			Details: reason,
		})
	}
	return matches
}

func buildClipDriveMatchingSection(ctx context.Context, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, dataDir string, repo *clips.Repository) ScriptSection {
	catalog, err := loadClipDriveCatalog(ctx, dataDir, repo)
	if err != nil || len(catalog) == 0 {
		return ScriptSection{Title: "Drive Matching", Body: "None"}
	}

	terms := uniqueStrings(append(collectTopicTerms(req.Topic), collectClipDrivePhrases(narrative, analysis)...))
	matches := matchClipDriveCatalog(catalog, terms, 4)
	if len(matches) == 0 {
		return ScriptSection{Title: "Drive Matching", Body: "None"}
	}

	return ScriptSection{
		Title: "Drive Matching",
		Body:  renderMatches(matches),
	}
}

func matchClipDriveCatalog(catalog []clipDriveRecord, terms []string, limit int) []scoredMatch {
	if len(catalog) == 0 {
		return nil
	}
	matches := make([]scoredMatch, 0, len(catalog))
	for _, rec := range catalog {
		candidate := buildClipCandidateString(rec)
		score := scoreText(candidate, terms)
		if score == 0 {
			continue
		}
		title := resolveClipDriveTitle(rec)
		matches = append(matches, scoredMatch{
			Title:   title,
			Path:    rec.FolderPath,
			Score:   score,
			Source:  "drive catalog",
			Link:    pickClipDriveRecordLink(rec),
			Details: strings.Join(rec.Tags, ", "),
		})
	}

	matches = sortTopMatches(matches, limit)
	return matches
}

func pickClipDriveRecordLink(rec clipDriveRecord) string {
	if strings.TrimSpace(rec.DriveLink) != "" {
		return rec.DriveLink
	}
	if strings.TrimSpace(rec.DownloadLink) != "" {
		return rec.DownloadLink
	}
	if strings.TrimSpace(rec.FolderID) != "" {
		return "https://drive.google.com/drive/folders/" + rec.FolderID
	}
	return ""
}

func resolveClipDriveTitle(rec clipDriveRecord) string {
	if title := strings.TrimSpace(rec.Name); title != "" {
		return title
	}
	if title := strings.TrimSpace(rec.Filename); title != "" {
		return title
	}
	return strings.TrimSpace(rec.FolderPath)
}

func buildClipCandidateString(rec clipDriveRecord) string {
	return strings.ToLower(strings.Join([]string{
		rec.Name,
		rec.Filename,
		rec.FolderPath,
		rec.Group,
		rec.MediaType,
		rec.DriveLink,
		strings.Join(rec.Tags, " "),
	}, " "))
}

func timelineMatchesFromCatalog(catalog []clipDriveRecord, seg TimelineSegment, limit int) []scoredMatch {
	terms := uniqueStrings(append(collectTopicTerms(seg.OpeningSentence+" "+seg.ClosingSentence), seg.Keywords...))
	if len(terms) == 0 {
		return nil
	}
	matches := make([]scoredMatch, 0, len(catalog))
	for _, rec := range catalog {
		candidate := buildClipCandidateString(rec)
		score := scoreText(candidate, terms)
		if score == 0 {
			continue
		}
		title := resolveClipDriveTitle(rec)
		matches = append(matches, scoredMatch{
			Title:   title,
			Path:    rec.FolderPath,
			Score:   score,
			Source:  "drive catalog",
			Link:    pickClipDriveRecordLink(rec),
			Details: strings.Join(rec.Tags, ", "),
		})
	}
	return limitTimelineMatches(sortTopMatches(matches, limit), limit)
}

func bestTimelineMatchFromCatalog(catalog []clipDriveRecord, seg TimelineSegment) (scoredMatch, bool) {
	if len(catalog) == 0 {
		return scoredMatch{}, false
	}
	terms := uniqueStrings(append(collectTopicTerms(seg.OpeningSentence+" "+seg.ClosingSentence), seg.Keywords...))
	var best scoredMatch
	bestScore := -1
	for _, rec := range catalog {
		candidate := buildClipCandidateString(rec)
		score := scoreText(candidate, terms)
		title := resolveClipDriveTitle(rec)
		if score > bestScore {
			bestScore = score
			best = scoredMatch{
				Title:   title,
				Path:    rec.FolderPath,
				Score:   score,
				Source:  "drive catalog",
				Link:    pickClipDriveRecordLink(rec),
				Details: strings.Join(rec.Tags, ", "),
			}
		}
	}
	if bestScore < 0 {
		return scoredMatch{}, false
	}
	return best, true
}

func limitTimelineMatches(matches []scoredMatch, limit int) []scoredMatch {
	if limit <= 0 || len(matches) <= limit {
		return matches
	}
	return matches[:limit]
}

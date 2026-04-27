package script

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"velox/go-master/internal/ml/ollama/types"
	"velox/go-master/internal/repository/clips"
)

type driveCheckpointIndex struct {
	Version int                    `json:"version"`
	Updated string                 `json:"updated_at"`
	Jobs    []driveCheckpointEntry `json:"jobs"`
}

type driveCheckpointEntry struct {
	Keyword  string `json:"keyword"`
	Status   string `json:"status"`
	DriveID  string `json:"drive_id"`
	DriveURL string `json:"drive_url"`
	Filename string `json:"filename"`
}

func buildDriveMatchingSection(ctx context.Context, dataDir string, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, repo *clips.Repository) ScriptSection {
	terms := collectTopicTerms(req.Topic)

	// Try DB first
	if repo != nil {
		dbClips, err := repo.ListClips(ctx, "")
		if err == nil && len(dbClips) > 0 {
			type folderMatch struct {
				FolderID   string
				FolderPath string
				MaxScore   int
				BestTitle  string
				Source     string
			}
			folderMatches := make(map[string]*folderMatch)

			for _, clip := range dbClips {
				// Normalize media type for comparison
				mt := strings.ToLower(strings.TrimSpace(clip.MediaType))
				if mt != "drive" && mt != "stock_drive" && mt != "clips" {
					continue
				}

				score := scoreText(strings.ToLower(clip.Name+" "+clip.Filename+" "+clip.Metadata), terms)
				if score == 0 {
					continue
				}

				// Group strictly by Path
				groupKey := clip.FolderPath
				if groupKey == "" {
					groupKey = "root_" + clip.Source
				}

				if existing, ok := folderMatches[groupKey]; !ok || score > existing.MaxScore {
					folderMatches[groupKey] = &folderMatch{
						FolderID:   clip.FolderID,
						FolderPath: clip.FolderPath,
						MaxScore:   score,
						BestTitle:  clip.Name,
						Source:     clip.Source,
					}
				}
			}

			matches := make([]scoredMatch, 0, len(folderMatches))
			for _, fm := range folderMatches {
				link := ""
				if fm.FolderID != "" {
					link = "https://drive.google.com/drive/folders/" + fm.FolderID
				}

				title := fm.FolderPath
				if idx := strings.LastIndex(title, "/"); idx != -1 {
					title = title[idx+1:]
				}
				if title == "" {
					title = fm.BestTitle
				}

				matches = append(matches, scoredMatch{
					Title:  title,
					Path:   fm.FolderPath,
					Score:  fm.MaxScore,
					Source: "drive sql db",
					Link:   link,
				})
			}

			matches = sortTopMatches(matches, 4)
			if len(matches) > 0 {
				return ScriptSection{
					Title: "🎞️ Drive Matching",
					Body:  renderMatches(matches),
				}
			}
		}
	}

	// Fallback to legacy JSON
	path := filepath.Join(dataDir, "clipsearch_checkpoints.json")

	var index driveCheckpointIndex
	if err := readJSON(path, &index); err != nil {
		return ScriptSection{
			Title: "Drive Matching",
			Body:  "Drive matching unavailable: no local checkpoint index found.",
		}
	}

	matches := make([]scoredMatch, 0, len(index.Jobs))
	for _, job := range index.Jobs {
		if strings.TrimSpace(job.Filename) == "" && strings.TrimSpace(job.DriveURL) == "" {
			continue
		}
		score := scoreText(strings.ToLower(job.Keyword+" "+job.Filename+" "+job.Status), terms)
		if score == 0 {
			continue
		}
		matches = append(matches, scoredMatch{
			Title:   job.Filename,
			Score:   score,
			Source:  "local checkpoint index",
			Link:    job.DriveURL,
			Details: "keyword: " + job.Keyword,
		})
	}

	matches = sortTopMatches(matches, 4)
	if len(matches) == 0 {
		return ScriptSection{
			Title: "Drive Matching",
			Body:  "None",
		}
	}

	return ScriptSection{
		Title: "Drive Matching",
		Body:  renderMatches(matches),
	}
}

func firstNonEmptyString(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func buildArtlistMatchingSection(ctx context.Context, dataDir string, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, plan *TimelinePlan, repo *clips.Repository) ScriptSection {
	if plan != nil {
		matches := collectArtlistPhraseMatchesFromPlan(plan)
		if len(matches) > 0 {
			return ScriptSection{
				Title: "🎞️ Artlist Matching",
				Body:  renderArtlistPhraseMatches(matches),
			}
		}
	}

	terms := collectTopicTerms(req.Topic)
	if repo != nil {
		dbClips, err := repo.ListClips(ctx, "")
		if err == nil && len(dbClips) > 0 {
			type folderMatch struct {
				FolderID   string
				FolderPath string
				MaxScore   int
				BestTitle  string
				Source     string
			}
			folderMatches := make(map[string]*folderMatch)

			for _, clip := range dbClips {
				mt := strings.ToLower(strings.TrimSpace(clip.MediaType))
				if mt != "stock" && mt != "artlist" {
					continue
				}

				title := clip.Name
				if title == "" {
					title = clip.Filename
				}
				if title == "" {
					continue
				}

				score := scoreText(strings.ToLower(strings.Join([]string{
					title,
					clip.Filename,
					clip.FolderPath,
					clip.Group,
					strings.Join(clip.Tags, " "),
					clip.Source,
					clip.Category,
				}, " ")), terms)
				if score == 0 {
					continue
				}

				fID := clip.FolderID
				if fID == "" {
					fID = "path_" + clip.FolderPath
				}
				if existing, ok := folderMatches[fID]; !ok || score > existing.MaxScore {
					folderMatches[fID] = &folderMatch{
						FolderID:   clip.FolderID,
						FolderPath: clip.FolderPath,
						MaxScore:   score,
						BestTitle:  title,
						Source:     clip.Source,
					}
				}
			}

			matches := make([]scoredMatch, 0, len(folderMatches))
			for _, fm := range folderMatches {
				link := ""
				if fm.FolderID != "" {
					link = "https://drive.google.com/drive/folders/" + fm.FolderID
				}
				matches = append(matches, scoredMatch{
					Title:  fm.BestTitle,
					Path:   fm.FolderPath,
					Score:  fm.MaxScore,
					Source: "artlist sql db",
					Link:   link,
				})
			}

			matches = sortTopMatches(matches, 4)
			if len(matches) > 0 {
				return ScriptSection{
					Title: "🎞️ Artlist Matching",
					Body:  renderArtlistPhraseMatches(matches),
				}
			}
		}
	}

	return ScriptSection{
		Title: "🎞️ Artlist Matching",
		Body:  "None",
	}
}

func collectArtlistPhraseMatchesFromPlan(plan *TimelinePlan) []scoredMatch {
	if plan == nil {
		return nil
	}

	matches := make([]scoredMatch, 0, 16)
	seen := make(map[string]struct{})
	for _, seg := range plan.Segments {
		segmentMatches := collectArtlistPhraseMatchesFromSegment(seg.ArtlistMatches, 5)
		for _, match := range segmentMatches {
			phrase := strings.TrimSpace(match.Title)
			if phrase == "" {
				continue
			}
			key := strings.ToLower(phrase)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, match)
		}
	}

	return sortTopMatches(matches, 20)
}

func collectArtlistPhraseMatchesFromSegment(matches []scoredMatch, limit int) []scoredMatch {
	if len(matches) == 0 {
		return nil
	}

	segmentMatches := make([]scoredMatch, 0, len(matches))
	seen := make(map[string]struct{})
	for _, match := range matches {
		phrase := strings.TrimSpace(match.Title)
		if phrase == "" {
			continue
		}
		key := strings.ToLower(phrase)
		if _, ok := seen[key]; ok {
			continue
		}
		link := strings.TrimSpace(match.Link)
		if link == "" {
			continue
		}
		seen[key] = struct{}{}
		segmentMatches = append(segmentMatches, scoredMatch{
			Title:   phrase,
			Score:   match.Score,
			Source:  match.Source,
			Link:    link,
			Details: match.Details,
		})
	}

	return sortTopMatches(segmentMatches, limit)
}

func renderArtlistPhraseMatches(matches []scoredMatch) string {
	if len(matches) == 0 {
		return "None"
	}

	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteString("\n")
		}

		phrase := strings.TrimSpace(match.Title)
		if phrase == "" {
			phrase = "Artlist phrase"
		}

		b.WriteString("✨ \"")
		b.WriteString(phrase)
		b.WriteString("\"\n")
		b.WriteString("  Source: ")
		b.WriteString(match.Source)
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("  Score: %d\n", match.Score))
		if strings.TrimSpace(match.Link) != "" {
			b.WriteString("  Link: ")
			b.WriteString(match.Link)
			b.WriteString("\n")
		}
		if strings.TrimSpace(match.Details) != "" {
			b.WriteString("  Details: ")
			b.WriteString(match.Details)
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func buildArtlistPhraseBlock(analysis *types.FullEntityAnalysis) string {
	if analysis == nil {
		return ""
	}

	var b strings.Builder
	seen := make(map[string]struct{})
	for _, segment := range analysis.SegmentEntities {
		for phrase := range segment.ArtlistPhrases {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			key := strings.ToLower(phrase)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			b.WriteString("Frase Artlist: ")
			b.WriteString(phrase)
			b.WriteString("\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func pickFirstMatchWithLink(matches []scoredMatch) *scoredMatch {
	if len(matches) == 0 {
		return nil
	}

	for i := range matches {
		if strings.TrimSpace(matches[i].Link) != "" {
			return &matches[i]
		}
	}
	return nil
}

func collectArtlistPhraseMatches(analysis *types.FullEntityAnalysis) []scoredMatch {
	if analysis == nil {
		return nil
	}

	matches := make([]scoredMatch, 0, 8)
	seen := make(map[string]struct{})
	for _, segment := range analysis.SegmentEntities {
		for phrase, links := range segment.ArtlistMatches {
			phrase = strings.TrimSpace(phrase)
			if phrase == "" {
				continue
			}
			key := strings.ToLower(phrase)
			if _, ok := seen[key]; ok {
				continue
			}
			link := firstNonEmptyString(links)
			if link == "" {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, scoredMatch{
				Title:   phrase,
				Score:   100,
				Source:  "artlist phrase match",
				Link:    link,
				Details: strings.Join(uniqueStrings(links), ", "),
			})
		}
	}

	return sortTopMatches(matches, 4)
}

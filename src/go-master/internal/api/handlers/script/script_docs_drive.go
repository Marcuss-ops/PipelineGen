package script

import (
	"context"
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

func buildDriveMatchingSection(dataDir string, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, repo *clips.Repository) ScriptSection {
	terms := collectTopicTerms(req.Topic)

	// Try DB first
	if repo != nil {
		dbClips, err := repo.ListClips(context.Background(), "")
		if err == nil && len(dbClips) > 0 {
			matches := make([]scoredMatch, 0, len(dbClips))
			for _, clip := range dbClips {
				if clip.MediaType != "drive" {
					continue
				}
				
				score := scoreText(strings.ToLower(clip.Name+" "+clip.Filename+" "+clip.Metadata), terms)
				if score == 0 {
					continue
				}
				matches = append(matches, scoredMatch{
					Title:   clip.Filename,
					Score:   score,
					Source:  "drive sql db",
					Link:    clip.DriveLink,
					Details: "keyword: " + clip.Name,
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

type artlistIndex struct {
	FolderID string            `json:"folder_id"`
	Clips    []artlistClipItem `json:"clips"`
}

type artlistClipItem struct {
	ClipID     string   `json:"clip_id"`
	FolderID   string   `json:"folder_id"`
	Filename   string   `json:"filename"`
	Title      string   `json:"title"`
	Name       string   `json:"name"`
	URL        string   `json:"url"`
	DriveURL   string   `json:"drive_url"`
	Folder     string   `json:"folder"`
	Category   string   `json:"category"`
	Source     string   `json:"source"`
	Tags       []string `json:"tags"`
	Duration   int      `json:"duration"`
	Downloaded bool     `json:"downloaded"`
}

// DisplayName returns a human readable name for Artlist entries.
func (a artlistClipItem) DisplayName() string {
	if strings.TrimSpace(a.Title) != "" {
		return a.Title
	}
	if strings.TrimSpace(a.Filename) != "" {
		return a.Filename
	}
	if strings.TrimSpace(a.Name) != "" {
		return a.Name
	}
	if strings.TrimSpace(a.URL) != "" {
		return a.URL
	}
	return a.ClipID
}

// PickLink returns the best available link for Artlist entries.
func (a artlistClipItem) PickLink() string {
	if strings.TrimSpace(a.URL) != "" {
		return a.URL
	}
	if strings.TrimSpace(a.DriveURL) != "" {
		return a.DriveURL
	}
	if strings.TrimSpace(a.FolderID) != "" {
		return "https://drive.google.com/drive/folders/" + a.FolderID
	}
	return ""
}

func buildArtlistMatchingSection(dataDir string, req ScriptDocsRequest, narrative string, analysis *types.FullEntityAnalysis, repo *clips.Repository) ScriptSection {
	terms := collectTopicTerms(req.Topic)

	// Try DB first
	if repo != nil {
		dbClips, err := repo.ListClips(context.Background(), "") // Empty group to list all
		if err == nil && len(dbClips) > 0 {
			matches := make([]scoredMatch, 0, len(dbClips))
			for _, clip := range dbClips {
				// We only want Artlist/Stock clips here
				if clip.MediaType != "stock" {
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

				link := clip.ExternalURL
				if link == "" {
					link = clip.DriveLink
				}

				matches = append(matches, scoredMatch{
					Title:   title,
					Score:   score,
					Source:  "artlist sql db",
					Link:    link,
					Details: strings.Join(clip.Tags, ", "),
				})
			}

			matches = sortTopMatches(matches, 4)
			if len(matches) > 0 {
				return ScriptSection{
					Title: "🎞️ Artlist Matching",
					Body:  renderMatches(matches),
				}
			}
		}
	}

	// Fallback to legacy JSON
	path := filepath.Join(dataDir, "artlist_stock_index.json")

	var index artlistIndex
	if err := readJSON(path, &index); err != nil {
		return ScriptSection{
			Title: "Artlist Matching",
			Body:  "Artlist index unavailable.",
		}
	}

	matches := make([]scoredMatch, 0, len(index.Clips))
	for _, clip := range index.Clips {
		title := strings.TrimSpace(clip.DisplayName())
		if title == "" {
			continue
		}
		score := scoreText(strings.ToLower(strings.Join([]string{
			title,
			clip.Filename,
			clip.Title,
			clip.Name,
			clip.Folder,
			clip.Category,
			strings.Join(clip.Tags, " "),
			clip.Source,
		}, " ")), terms)
		if score == 0 {
			continue
		}
		matches = append(matches, scoredMatch{
			Title:   title,
			Score:   score,
			Source:  "artlist local index",
			Link:    clip.PickLink(),
			Details: strings.Join(clip.Tags, ", "),
		})
	}

	matches = sortTopMatches(matches, 4)
	if len(matches) == 0 {
		return ScriptSection{
			Title: "Artlist Matching",
			Body:  "None",
		}
	}

	return ScriptSection{
		Title: "Artlist Matching",
		Body:  renderMatches(matches),
	}
}

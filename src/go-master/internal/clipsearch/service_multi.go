package clipsearch

import (
	"context"
	"fmt"
	"strings"
)

func (s *Service) processKeywordsMulti(ctx context.Context, keywords []string, opts SearchOptions) ([]SearchResult, int) {
	maxPerKeyword := opts.MaxClipsPerKeyword
	if maxPerKeyword <= 1 {
		maxPerKeyword = 1
	}
	results := make([]SearchResult, 0, len(keywords)*maxPerKeyword)
	newUploads := 0

	for _, kw := range keywords {
		seenDrive := make(map[string]bool)
		kwResults := make([]SearchResult, 0, maxPerKeyword)
		attemptLimit := maxPerKeyword * 4
		if attemptLimit < 3 {
			attemptLimit = 3
		}
		for attempt := 1; attempt <= attemptLimit && len(kwResults) < maxPerKeyword; attempt++ {
			jobID := fmt.Sprintf("%s_m%d", s.ensureKeywordJobCheckpoint(kw), attempt)
			res, uploaded, found := s.processKeyword(ctx, kw, SearchOptions{ForceFresh: true}, jobID)
			if !found {
				break
			}
			driveID := strings.TrimSpace(res.DriveID)
			if driveID == "" || seenDrive[driveID] {
				continue
			}
			seenDrive[driveID] = true
			kwResults = append(kwResults, res)
			results = append(results, res)
			if uploaded {
				newUploads++
			}
		}
		if len(kwResults) > 0 {
			s.uploadKeywordSummary(ctx, kw, kwResults)
		}
	}
	return results, newUploads
}

func enrichYouTubeSearchResult(res *SearchResult, keyword string, meta *YouTubeClipMetadata) {
	if res == nil || meta == nil {
		return
	}
	if strings.TrimSpace(meta.Description) != "" {
		res.Description = strings.TrimSpace(meta.Description)
	}
	if meta.SelectedMoment != nil {
		res.StartSec = meta.SelectedMoment.StartSec
		res.EndSec = meta.SelectedMoment.EndSec
		res.Score = meta.SelectedMoment.Score
	}
	if strings.TrimSpace(meta.Transcript) != "" {
		r := []rune(strings.TrimSpace(meta.Transcript))
		if len(r) > 220 {
			res.TranscriptSnippet = string(r[:220]) + "..."
		} else {
			res.TranscriptSnippet = string(r)
		}
	}
	if strings.TrimSpace(meta.VideoID) != "" {
		res.ThumbnailURL = "https://img.youtube.com/vi/" + strings.TrimSpace(meta.VideoID) + "/hqdefault.jpg"
	}
	tagSet := make(map[string]bool)
	for _, t := range keywordSearchTokens(keyword) {
		tt := strings.TrimSpace(strings.ToLower(t))
		if tt != "" && !tagSet[tt] {
			tagSet[tt] = true
			res.Tags = append(res.Tags, tt)
		}
	}
	for _, t := range strings.Fields(strings.ToLower(meta.Channel + " " + meta.Title)) {
		tt := strings.TrimSpace(t)
		if len(tt) < 4 {
			continue
		}
		if !tagSet[tt] {
			tagSet[tt] = true
			res.Tags = append(res.Tags, tt)
		}
		if len(res.Tags) >= 8 {
			break
		}
	}
}

func (s *Service) uploadKeywordSummary(ctx context.Context, keyword string, clips []SearchResult) {
	if s.uploader == nil || len(clips) == 0 {
		return
	}
	folderID := strings.TrimSpace(clips[0].FolderID)
	if folderID == "" {
		return
	}
	text := buildKeywordSummaryText(keyword, clips)
	base := fmt.Sprintf("riepilogo_%s.txt", sanitizeFilename(keyword))
	_, _ = s.uploader.UploadTextSidecar(ctx, folderID, base, keyword, text)
}

func buildKeywordSummaryText(keyword string, clips []SearchResult) string {
	var b strings.Builder
	b.WriteString("RIEPILOGO CLIP - " + strings.TrimSpace(keyword) + "\n")
	b.WriteString(strings.Repeat("=", 80) + "\n\n")
	b.WriteString(fmt.Sprintf("Totale clip: %d\n\n", len(clips)))
	for i, c := range clips {
		title := c.Keyword
		if strings.TrimSpace(c.Filename) != "" {
			title = c.Filename
		}
		b.WriteString(fmt.Sprintf("%d. [%s]\n", i+1, title))
		b.WriteString("Link: " + strings.TrimSpace(c.DriveURL) + "\n")
		b.WriteString("File: " + strings.TrimSpace(c.Filename) + "\n")
		b.WriteString("Stato: Upload completato\n")
		if strings.TrimSpace(c.Description) != "" {
			b.WriteString("Descrizione: " + strings.TrimSpace(c.Description) + "\n")
		}
		if len(c.Tags) > 0 {
			b.WriteString("Tag: " + strings.Join(c.Tags, ", ") + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

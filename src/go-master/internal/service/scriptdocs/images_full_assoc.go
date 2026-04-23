package scriptdocs

import (
	"context"
	"strings"
)

func (s *ScriptDocService) buildImagesFullAssociations(ctx context.Context, topic string, chapters []ScriptChapter, entityImages map[string]string) []ImageAssociation {
	if len(chapters) == 0 {
		return nil
	}

	seenURLs := make(map[string]bool)
	out := make([]ImageAssociation, 0, len(chapters)*4)
	for _, chapter := range chapters {
		out = append(out, s.buildImageAssociationsForChapter(ctx, topic, chapter, entityImages, seenURLs)...)
	}

	if len(out) == 0 {
		for _, candidate := range s.imageCandidatesForTopic(topic, entityImages) {
			rec, cached, trace, err := s.resolveImageForEntityWithTrace(ctx, topic, candidate.entity, candidate.query, ScriptChapter{}, 0)
			if err != nil || rec == nil || strings.TrimSpace(rec.ImageURL) == "" {
				continue
			}
			out = append(out, ImageAssociation{
				Phrase:       topic,
				Entity:       candidate.entity,
				Query:        candidate.query,
				ImageURL:     rec.ImageURL,
				Source:       rec.Source,
				Title:        rec.Title,
				PageURL:      rec.PageURL,
				Score:        candidate.score,
				Cached:       cached,
				LocalPath:    rec.LocalPath,
				MimeType:     rec.MimeType,
				FileSize:     rec.FileSizeBytes,
				AssetHash:    rec.AssetHash,
				DownloadedAt: formatTime(rec.DownloadedAt),
				Resolution:   trace,
			})
			break
		}
	}

	return out
}

func (s *ScriptDocService) buildImageAssociationsForChapter(ctx context.Context, topic string, chapter ScriptChapter, entityImages map[string]string, seenURLs map[string]bool) []ImageAssociation {
	candidates := s.imageCandidatesForChapter(topic, chapter, entityImages)
	target := imageAssociationsTargetCount(chapter)
	if target < 1 {
		target = 1
	}
	produced := 0
	out := make([]ImageAssociation, 0, target)
	for _, candidate := range candidates {
		if produced >= target {
			break
		}
		rec, cached, trace, err := s.resolveImageForEntityWithTrace(ctx, topic, candidate.entity, candidate.query, chapter, chapter.Index+1)
		if err != nil || rec == nil || strings.TrimSpace(rec.ImageURL) == "" {
			continue
		}
		urlKey := strings.ToLower(strings.TrimSpace(rec.ImageURL))
		if urlKey == "" || seenURLs[urlKey] {
			continue
		}
		seenURLs[urlKey] = true
		out = append(out, ImageAssociation{
			Phrase:       compactSnippet(chapter.SourceText, 140),
			Entity:       candidate.entity,
			Query:        candidate.query,
			ImageURL:     rec.ImageURL,
			Source:       rec.Source,
			Title:        rec.Title,
			PageURL:      rec.PageURL,
			StartTime:    chapter.StartTime,
			EndTime:      chapter.EndTime,
			ChapterIndex: chapter.Index + 1,
			Score:        candidate.score,
			Cached:       cached,
			LocalPath:    rec.LocalPath,
			MimeType:     rec.MimeType,
			FileSize:     rec.FileSizeBytes,
			AssetHash:    rec.AssetHash,
			DownloadedAt: formatTime(rec.DownloadedAt),
			Resolution:   trace,
		})
		produced++
	}
	return out
}

func imageAssociationsTargetCount(chapter ScriptChapter) int {
	duration := chapter.EndTime - chapter.StartTime
	if duration <= 0 {
		duration = int(chapter.Confidence * 10)
	}
	if duration <= 0 {
		duration = 12
	}
	// Aim for one visual every ~6 seconds, with a minimum of 1.
	target := (duration + 5) / 6
	if target < 1 {
		target = 1
	}
	if target > 8 {
		target = 8
	}
	return target
}

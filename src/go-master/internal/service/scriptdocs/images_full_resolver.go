package scriptdocs

import (
	"context"
	"fmt"
	"strings"

	"velox/go-master/internal/imagesdb"
)

func (s *ScriptDocService) resolveImageForEntity(ctx context.Context, topic, entity, query string, chapter ScriptChapter, chapterIndex int) (*imagesdb.ImageRecord, bool, error) {
	rec, cached, _, err := s.resolveImageForEntityWithTrace(ctx, topic, entity, query, chapter, chapterIndex)
	return rec, cached, err
}

func (s *ScriptDocService) resolveImageForEntityWithTrace(ctx context.Context, topic, entity, query string, chapter ScriptChapter, chapterIndex int) (*imagesdb.ImageRecord, bool, *AssetResolution, error) {
	entity = strings.TrimSpace(entity)
	if entity == "" {
		return nil, false, nil, nil
	}
	if strings.TrimSpace(query) == "" {
		query = entity
	}
	trace := newAssetResolution("images", "imagesdb-cache", "entityimages", "download").withOutcome("", "image entity resolution", false)
	trace.RequestKey = normalizeKeyword(entity) + "|" + normalizeKeyword(query)

	if s.imagesDB != nil {
		if rec, ok := s.imagesDB.Get(entity); ok && strings.TrimSpace(rec.ImageURL) != "" {
			rec.VideoID = topic
			rec.ChapterIndex = chapterIndex
			rec.Query = query
			rec.UsedCount++
			if rec.LocalPath == "" || !fileExists(rec.LocalPath) {
				if s.imageDownloader != nil {
					downloaded, err := s.imageDownloader.Download(ctx, *rec)
					if err == nil && downloaded != nil {
						rec.LocalPath = downloaded.LocalPath
						rec.MimeType = downloaded.MimeType
						rec.FileSizeBytes = downloaded.FileSize
						rec.AssetHash = downloaded.AssetHash
						rec.DownloadedAt = downloaded.DownloadedAt
					}
				}
			}
			_ = s.imagesDB.Touch(*rec)
			trace.withOutcome("imagesdb-cache", "cached image record reused", true)
			return rec, true, trace, nil
		}
	}

	finder := s.imageFinder
	if finder == nil {
		return nil, false, trace, fmt.Errorf("image finder not configured")
	}

	candidates := []string{query, entity}
	if strings.TrimSpace(topic) != "" && normalizeKeyword(topic) != normalizeKeyword(entity) {
		candidates = append(candidates, entity+" "+topic, topic+" "+entity)
	}

	for _, candidateQuery := range candidates {
		imageURL := strings.TrimSpace(finder.Find(candidateQuery))
		if imageURL == "" {
			continue
		}
		rec := &imagesdb.ImageRecord{
			Entity:         entity,
			Query:          candidateQuery,
			Source:         "entityimages",
			Title:          entity,
			ImageURL:       imageURL,
			VideoID:        topic,
			ChapterIndex:   chapterIndex,
			RelevanceScore: scoreImageCandidate(entity, topic, chapter, 0),
		}
		if s.imageDownloader != nil {
			if downloaded, err := s.imageDownloader.Download(ctx, *rec); err == nil && downloaded != nil {
				rec.LocalPath = downloaded.LocalPath
				rec.MimeType = downloaded.MimeType
				rec.FileSizeBytes = downloaded.FileSize
				rec.AssetHash = downloaded.AssetHash
				rec.DownloadedAt = downloaded.DownloadedAt
			}
		}
		if s.imagesDB != nil {
			if err := s.imagesDB.Upsert(*rec); err != nil {
				return nil, false, trace, err
			}
		}
		trace.withOutcome("entityimages", "finder resolved a new image", false)
		return rec, false, trace, nil
	}

	trace.addNote("no image match found for entity")
	return nil, false, trace, nil
}

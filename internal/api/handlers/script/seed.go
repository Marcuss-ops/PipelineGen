package script

import (
	"context"
	"strings"
	"time"

	artlistservice "velox/go-master/internal/service/artlist"
	"velox/go-master/internal/repository/clips"
)

func seedArtlistPhrasesFromLiveSearch(ctx context.Context, artlistSvc *artlistservice.Service, repo *clips.Repository, topic string) map[string][]string {
	if artlistSvc == nil || strings.TrimSpace(topic) == "" {
		return nil
	}

	searchCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()

	resp, err := artlistSvc.Search(searchCtx, &artlistservice.SearchRequest{
		Term:     topic,
		Limit:    10,
		SaveDB:   true,
		PreferDB: false,
	})
	if err != nil || resp == nil || len(resp.Clips) == 0 {
		return nil
	}

	phrases := make(map[string][]string, len(resp.Clips))
	for _, clip := range resp.Clips {
		if clip.ID != "" {
			_, _ = artlistSvc.ProcessClip(searchCtx, &artlistservice.ProcessClipRequest{
				ClipID:       clip.ID,
				AutoDownload: true,
				AutoUpload:   true,
			})
		}

		name := strings.TrimSpace(clip.Name)
		if name == "" {
			name = strings.TrimSpace(clip.Filename)
		}
		if name == "" {
			continue
		}

		keywords := uniqueStrings(append([]string{}, clip.Tags...))
		if len(keywords) == 0 {
			keywords = collectTopicTerms(name)
		}
		if len(keywords) == 0 {
			keywords = collectTopicTerms(topic)
		}
		if len(keywords) == 0 {
			continue
		}

		phrases[name] = keywords
		if repo != nil {
			clipCopy := clip
			_ = repo.UpsertClip(ctx, &clipCopy)
		}
	}

	return phrases
}

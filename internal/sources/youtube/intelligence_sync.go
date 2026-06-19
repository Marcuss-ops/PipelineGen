package youtube

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"

	"go.uber.org/zap"
)

func (s *Service) syncManifestClipIntelligence(ctx context.Context, clipFolder *media.ClipFolder, item *media.ClipManifestItem) {
	if s == nil || s.clipsRepo == nil || clipFolder == nil || item == nil || item.ID == "" {
		return
	}

	if item.SearchVisibility == "" {
		item.SearchVisibility = deriveSearchVisibility(item.QualityScore, nil, item.Tags)
	}

	if clipFolder.LocalFolderPath != "" {
		metaPath := filepath.Join(clipFolder.LocalFolderPath, "metadata_"+item.ID+".json")
		if metaBytes, err := os.ReadFile(metaPath); err == nil {
			var meta ClipMetadataFile
			if err := json.Unmarshal(metaBytes, &meta); err == nil {
				meta.DuplicateGroupID = item.DuplicateGroupID
				meta.DuplicateOf = item.DuplicateOf
				meta.IsDuplicate = item.IsDuplicate
				meta.IsBestVersion = item.IsBestVersion
				meta.DuplicateReason = item.DuplicateReason
				meta.DuplicateScore = item.DuplicateScore
				meta.TopicClusterID = item.TopicClusterID
				meta.TopicClusterLabel = item.TopicClusterLabel
				meta.TopicClusterSize = item.TopicClusterSize
				meta.TopicClusterRank = item.TopicClusterRank
				meta.QualityScore = item.QualityScore
				meta.SearchVisibility = item.SearchVisibility
				if data, err := json.MarshalIndent(meta, "", "  "); err == nil {
					_ = os.WriteFile(metaPath, data, 0644)
				}
			}
		}
	}

	clip, err := s.clipsRepo.Get(ctx, item.ID)
	if err != nil || clip == nil {
		return
	}

	if clip.Metadata == nil {
		clip.Metadata = make(map[string]any)
	}
	clip.SearchText = item.EmbeddingText
	clip.Tags = mergeTagLists(item.Tags, item.SourceTags, item.ClipTags, item.Topics, item.Speakers, item.MentionedPeople, item.People)
	clip.SetMetadataString("clean_title", item.CleanTitle)
	clip.SetMetadataString("short_title", item.ShortTitle)
	clip.SetMetadataString("clip_summary", item.ClipSummary)
	clip.SetMetadataString("hook", item.Hook)
	clip.Metadata["topics"] = append([]string(nil), item.Topics...)
	clip.Metadata["speakers"] = append([]string(nil), item.Speakers...)
	clip.Metadata["mentioned_people"] = append([]string(nil), item.MentionedPeople...)
	clip.Metadata["people"] = append([]string(nil), item.People...)
	clip.Metadata["source_tags"] = append([]string(nil), item.SourceTags...)
	clip.Metadata["clip_tags"] = append([]string(nil), item.ClipTags...)
	clip.Metadata["search_keywords"] = append([]string(nil), item.SearchKeywords...)
	clip.Metadata["clean_transcript"] = item.CleanTranscript
	clip.Metadata["embedding_text"] = item.EmbeddingText
	clip.Metadata["quality_score"] = item.QualityScore
	clip.Metadata["search_visibility"] = item.SearchVisibility
	clip.Metadata["duplicate_group_id"] = item.DuplicateGroupID
	clip.Metadata["duplicate_of"] = item.DuplicateOf
	clip.Metadata["is_duplicate"] = item.IsDuplicate
	clip.Metadata["is_best_version"] = item.IsBestVersion
	clip.Metadata["duplicate_reason"] = item.DuplicateReason
	clip.Metadata["duplicate_score"] = item.DuplicateScore
	clip.Metadata["topic_cluster_id"] = item.TopicClusterID
	clip.Metadata["topic_cluster_label"] = item.TopicClusterLabel
	clip.Metadata["topic_cluster_size"] = item.TopicClusterSize
	clip.Metadata["topic_cluster_rank"] = item.TopicClusterRank

	if err := s.clipsRepo.Upsert(ctx, clip); err != nil {
		s.log.Debug("failed to persist clip intelligence", zap.String("clip_id", item.ID), zap.Error(err))
		return
	}

	if err := s.clipsRepo.UpdateSearchTerms(ctx, clip.ID, string(clip.Source), chooseSearchTitle(item), clip.Tags, clip.SearchText); err != nil {
		s.log.Debug("failed to update search terms", zap.String("clip_id", item.ID), zap.Error(err))
	}
}

func chooseSearchTitle(item *media.ClipManifestItem) string {
	if item == nil {
		return ""
	}
	if item.CleanTitle != "" {
		return item.CleanTitle
	}
	if item.Name != "" {
		return item.Name
	}
	return item.ShortTitle
}

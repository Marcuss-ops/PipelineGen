package handlers

import (
	"context"
	"encoding/json"

	"go.uber.org/zap"

	"velox/go-master/pkg/concurrent"
)

// handlePostGeneration runs entity extraction, insight building, and metadata
// generation in parallel when the corresponding flags are set.
// Returns the raw entities JSON, structured insights, and per-language video metadata.
func (h *ScriptFlowHandler) handlePostGeneration(
	ctx context.Context,
	payload *jobPayloadUnified,
	pathResult *clipSourcePathResult,
) (entitiesJSON string, insights ScriptInsights, videoMetadata []VideoMetadata) {
	if !payload.ExtractEntities && !payload.GenerateMetadata {
		return "", ScriptInsights{}, nil
	}
	if pathResult == nil || pathResult.WriteResult == nil {
		return "", ScriptInsights{}, nil
	}

	group, groupCtx := concurrent.WithContext(ctx)

	if payload.ExtractEntities {
		group.Go("entities-and-insights", func() error {
			var client EntityScriptExtractor
			if h.generator != nil {
				client = h.generator.GetClient()
			}
			ents, err := ExtractScriptEntities(groupCtx, client, pathResult.WriteResult.Script, h.metadataModel)
			if err != nil {
				h.log.Warn("failed to extract entities", zap.Error(err))
			}
			entitiesJSON = ents
			if h.insightBuilder != nil {
				insights = h.insightBuilder.Build(groupCtx, payload.Title, pathResult.WriteResult.Script, ents)
			}
			return nil
		})
	}

	if payload.GenerateMetadata {
		group.Go("video-metadata", func() error {
			languages := BuildMetadataLanguages(payload.Language, payload.Languages)
			videoMetadata = GenerateVideoMetadata(groupCtx, h.generator, payload.Title, languages, h.metadataModel)
			return nil
		})
	}

	if waitErr := group.Wait(); waitErr != nil {
		h.log.Warn("post-generation phase returned an error (continuing)", zap.Error(waitErr))
	}

	return entitiesJSON, insights, videoMetadata
}

// handleCreateDoc builds the Google Doc content and creates the document.
func (h *ScriptFlowHandler) handleCreateDoc(
	ctx context.Context,
	payload *jobPayloadUnified,
	pathResult *clipSourcePathResult,
	entitiesJSON string,
	insights ScriptInsights,
	videoMetadata []VideoMetadata,
	scenes []ScriptSceneImage,
) (docLink, docID string) {
	if pathResult == nil || pathResult.WriteResult == nil {
		return "", ""
	}

	docContent := buildGeneratedTextDocContent(
		payload.Title,
		pathResult.WriteResult.Script,
		payload.TargetWords,
		videoMetadata,
		entitiesJSON,
		insights,
		scenes,
	)
	resolvedDriveFolderID, err := h.resolveDriveFolderID(ctx, payload.DriveFolderID, "1qfTiJNqnce18MmeDrV4ORh6n5jLW4dR5")
	if err != nil {
		h.log.Warn("failed to resolve custom drive folder name/path, using default root", zap.Error(err))
		resolvedDriveFolderID = "1qfTiJNqnce18MmeDrV4ORh6n5jLW4dR5"
	}

	if l, id := h.maybeCreateGoogleDoc(ctx, payload.Title, docContent, resolvedDriveFolderID, true); l != "" {
		docLink = l
		docID = id
		h.log.Info("Google Doc created", zap.String("doc_id", docID))
	} else {
		h.log.Warn("Google Doc creation failed (non-fatal)")
	}
	return docLink, docID
}

// buildFinalResult assembles the final result map sent back to the client.
func (h *ScriptFlowHandler) buildFinalResult(
	payload *jobPayloadUnified,
	pathResult *clipSourcePathResult,
	entitiesJSON string,
	insights ScriptInsights,
	videoMetadata []VideoMetadata,
	docLink, docID string,
	scenes []ScriptSceneImage,
	voiceovers []SceneVoiceover,
	totalDurMs int64,
) map[string]any {
	if pathResult == nil || pathResult.WriteResult == nil {
		return map[string]any{"ok": false}
	}

	result := map[string]any{
		"ok":           true,
		"script":       pathResult.WriteResult.Script,
		"word_count":   pathResult.WriteResult.WordCount,
		"title":        payload.Title,
		"language":     payload.Language,
		"cache_status": pathResult.WriteResult.CacheStatus,
	}

	if payload.ExtractEntities {
		result["entities_json"] = entitiesJSON
		result["important_words"] = insights.ImportantWords
		result["important_phrases"] = insights.ImportantPhrases
		result["special_names"] = insights.SpecialNames
		result["artlist_phrases"] = insights.ArtlistPhrases
		result["artlist_clip_suggestions"] = insights.ArtlistClipSuggestions
		result["recommended_drive_folder"] = insights.RecommendedDriveFolder
		result["phrase_clip_suggestions"] = insights.PhraseClipSuggestions
		result["intro_clips"] = insights.IntroClips
		result["entity_images"] = insights.EntityImages
	}
	if payload.GenerateSceneImages {
		result["scenes"] = scenes
		if scenesJSONBytes, err := json.Marshal(scenes); err == nil {
			result["scenes_json"] = string(scenesJSONBytes)
		}
	}
	if len(voiceovers) > 0 {
		result["voiceovers"] = voiceovers
		for _, vo := range voiceovers {
			if vo.Status == "completed" {
				if vo.Link != "" {
					result["voiceover_path"] = vo.Link
					result["audio_path"] = vo.Link
				} else if vo.LocalPath != "" {
					result["voiceover_path"] = vo.LocalPath
					result["audio_path"] = vo.LocalPath
				}
				break
			}
		}
	}
	if payload.GenerateMetadata {
		result["metadata"] = videoMetadata
	}
	if docLink != "" {
		result["doc_url"] = docLink
		result["doc_id"] = docID
	}

	if len(pathResult.ClipScenes) > 0 {
		result["clip_scenes"] = pathResult.ClipScenes
		result["clip_count"] = len(pathResult.ClipScenes)
	}

	// Auto-search specific enrichments
	if len(pathResult.SearchResults) > 0 {
		result["search_results"] = pathResult.SearchResults
	}
	if pathResult.NarrativePlan != nil {
		result["narrative_plan"] = pathResult.NarrativePlan
	}
	if pathResult.CurateTimings.TotalMs > 0 {
		result["curate_timings"] = map[string]any{
			"search_ms":        pathResult.CurateTimings.SearchMs,
			"build_context_ms": pathResult.CurateTimings.BuildCtxMs,
			"write_script_ms":  pathResult.CurateTimings.WriteScriptMs,
			"total_ms":         pathResult.CurateTimings.TotalMs,
		}
	}

	result["timings"] = map[string]any{
		"total_ms": totalDurMs,
	}

	return result
}

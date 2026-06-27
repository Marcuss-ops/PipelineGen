package scripts

import scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

func (pu *PipelineUseCase) buildFinalResult(
	payload *scriptpkg.GenerationSpec,
	pathResult *ClipSourcePathResult,
	entitiesJSON string,
	insights ScriptInsights,
	videoMetadata []VideoMetadata,
	docLink, docID string,
	scenes []SceneImage,
	voiceovers []SceneVoiceover,
	totalDurMs int64,
) map[string]any {
	if pathResult == nil || pathResult.WriteResult == nil {
		return map[string]any{"ok": false}
	}
	normalizedScenes, scenesJSON, hasScenes := marshalNormalizedScenes(pathResult.ClipScenes, scenes)
	out := map[string]any{
		"ok":           true,
		"word_count":   pathResult.WriteResult.WordCount,
		"title":        payload.Title,
		"language":     payload.Language,
		"cache_status": pathResult.WriteResult.CacheStatus,
	}
	if payload.MaxChars > 0 && hasScenes {
		out["script"] = normalizedScenes
		out["script_raw"] = pathResult.WriteResult.Script
	} else {
		out["script"] = pathResult.WriteResult.Script
	}
	if payload.GenerateSceneImages && hasScenes {
		out["scenes"] = normalizedScenes
		out["scenes_json"] = scenesJSON
	}
	if payload.ExtractEntities {
		out["entities_json"] = entitiesJSON
		out["important_words"] = insights.ImportantWords
		out["important_phrases"] = insights.ImportantPhrases
		out["special_names"] = insights.SpecialNames
		out["artlist_phrases"] = insights.ArtlistPhrases
		// PR2 2b/c (June 2026): the 5 media-side fields below were
		// removed from ScriptInsights. The helpers that populated
		// them (SearchArtlistClips, BuildPhraseClipSuggestions,
		// ResolveRecommendedDriveFolder, SearchIntroClips,
		// EnrichSpecialNamesWithImages) depended on realtime/association
		// packages no longer in the repo. Production wiring always
		// returned empty for each. We emit empty arrays for back-compat
		// with downstream consumers that check for these keys.
		out["artlist_clip_suggestions"] = []interface{}{}
		out["recommended_drive_folder"] = nil
		out["phrase_clip_suggestions"] = []interface{}{}
		out["intro_clips"] = []interface{}{}
		out["entity_images"] = []interface{}{}
	}
	if len(voiceovers) > 0 {
		out["voiceovers"] = voiceovers
		for _, vo := range voiceovers {
			if vo.Status == "completed" {
				if vo.Link != "" {
					out["voiceover_path"] = vo.Link
					out["audio_path"] = vo.Link
				} else if vo.LocalPath != "" {
					out["voiceover_path"] = vo.LocalPath
					out["audio_path"] = vo.LocalPath
				}
				break
			}
		}
	}
	if payload.GenerateMetadata {
		out["metadata"] = videoMetadata
	}
	if docLink != "" {
		out["doc_url"] = docLink
		out["doc_link"] = docLink
		out["doc_id"] = docID
	}
	if len(pathResult.ClipScenes) > 0 {
		out["clip_scenes"] = pathResult.ClipScenes
		out["clip_count"] = len(pathResult.ClipScenes)
	}
	if len(pathResult.SearchResults) > 0 {
		out["search_results"] = pathResult.SearchResults
	}
	if pathResult.NarrativePlan != nil {
		out["narrative_plan"] = pathResult.NarrativePlan
	}
	if pathResult.CurateTimings.TotalMs > 0 {
		out["curate_timings"] = map[string]any{
			"search_ms":        pathResult.CurateTimings.SearchMs,
			"build_context_ms": pathResult.CurateTimings.BuildCtxMs,
			"write_script_ms":  pathResult.CurateTimings.WriteScriptMs,
			"total_ms":         pathResult.CurateTimings.TotalMs,
		}
	}
	out["timings"] = map[string]any{"total_ms": totalDurMs}
	return out
}

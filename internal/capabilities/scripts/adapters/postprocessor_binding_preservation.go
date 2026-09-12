// Package adapters — postprocessor_binding_preservation.go: the scene-binding
// and VidRush-segment preservation half of the post-processor merge
// (extracted 2026-09-12 from postprocessor_composite_merge.go to keep both
// halves under max_lines_per_file_strict=600, godlike/08).
package adapters

import (
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func preserveSceneBindings(previous, replacement []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	if len(previous) == 0 || len(replacement) == 0 {
		return replacement
	}
	out := append([]scriptpkg.SpecScene(nil), replacement...)
	bySegment := make(map[string]int, len(previous))
	byID := make(map[string]int, len(previous))
	for i, scene := range previous {
		if key := strings.TrimSpace(scene.SegmentID); key != "" {
			bySegment[key] = i
		}
		if key := strings.TrimSpace(scene.ID); key != "" {
			byID[key] = i
		}
	}
	used := make(map[int]struct{}, len(previous))
	for i := range out {
		previousIndex := -1
		if key := strings.TrimSpace(out[i].SegmentID); key != "" {
			if index, ok := bySegment[key]; ok {
				previousIndex = index
			}
		} else if key := strings.TrimSpace(out[i].ID); key != "" {
			if index, ok := byID[key]; ok {
				previousIndex = index
			}
		} else if i < len(previous) {
			previousIndex = i
		}
		if previousIndex < 0 || previousIndex >= len(previous) {
			continue
		}
		if _, exists := used[previousIndex]; exists {
			continue
		}
		used[previousIndex] = struct{}{}
		if strings.TrimSpace(out[i].SegmentID) == "" {
			out[i].SegmentID = previous[previousIndex].SegmentID
		}
		if !out[i].Kind.Valid() || out[i].Kind == scriptpkg.SceneNarration {
			if previous[previousIndex].Kind.Valid() {
				out[i].Kind = previous[previousIndex].Kind
			}
		}
		out[i].Bindings = preserveBindings(previous[previousIndex].Bindings, out[i].Bindings)
		preserveResolvedEntityImages(&out[i], previous[previousIndex])
	}
	return out
}

// preserveResolvedEntityImages carries identity-image bindings across a
// later scene rewrite (for example materialization or document preparation).
// Those processors may return a fresh annotation slice without image fields;
// replacing it wholesale would silently turn a successfully materialized
// person image back into an unbound entity.
func preserveResolvedEntityImages(replacement *scriptpkg.SpecScene, previous scriptpkg.SpecScene) {
	if replacement == nil || previous.Annotations == nil || len(previous.Annotations.PrimaryEntities) == 0 {
		return
	}
	if replacement.Annotations == nil {
		return
	}
	previousImages := make(map[string]scriptpkg.EntityImageBinding)
	for _, entity := range previous.Annotations.PrimaryEntities {
		if entity.Image == nil || strings.TrimSpace(entity.Image.Status) != "resolved" {
			continue
		}
		key := normalizeEntityMatch(entity.CanonicalName)
		if key == "" {
			key = normalizeEntityMatch(entity.Text)
		}
		if key != "" {
			previousImages[key] = *entity.Image
		}
	}
	if len(previousImages) == 0 {
		return
	}
	for i := range replacement.Annotations.PrimaryEntities {
		entity := &replacement.Annotations.PrimaryEntities[i]
		if entity.Image != nil && strings.TrimSpace(entity.Image.Status) == "resolved" {
			continue
		}
		key := normalizeEntityMatch(entity.CanonicalName)
		if key == "" {
			key = normalizeEntityMatch(entity.Text)
		}
		if image, ok := previousImages[key]; ok {
			copy := image
			entity.Image = &copy
		}
	}
}

func preserveBindings(previous, replacement scriptpkg.SceneBindings) scriptpkg.SceneBindings {
	previous = cloneSceneBindings(previous)
	replacement = cloneSceneBindings(replacement)
	if replacement.Stock == nil {
		replacement.Stock = previous.Stock
	}
	if len(replacement.Media) == 0 {
		replacement.Media = append([]scriptpkg.ResolvedMediaBinding(nil), previous.Media...)
	}

	switch {
	case len(replacement.Clips) > 0:
		usedPrevious := make(map[int]struct{}, len(replacement.Clips))
		for i := range replacement.Clips {
			previousIndex := -1
			if replacement.Clips[i].ClipID != "" {
				for j := range previous.Clips {
					if previous.Clips[j].ClipID == replacement.Clips[i].ClipID {
						previousIndex = j
						break
					}
				}
			}
			if previousIndex < 0 && i < len(previous.Clips) {
				previousIndex = i
			}
			if previousIndex >= 0 {
				usedPrevious[previousIndex] = struct{}{}
				replacement.Clips[i] = mergeClipBinding(previous.Clips[previousIndex], replacement.Clips[i])
			}
		}
		// Translation and other scene replacements are often partial. Keep
		// previously materialized clips that were not mentioned by the
		// replacement so a multi-clip scene cannot collapse to one entry.
		for i := range previous.Clips {
			if _, exists := usedPrevious[i]; !exists {
				replacement.Clips = append(replacement.Clips, previous.Clips[i])
			}
		}
	case replacement.Clip != nil:
		// A legacy replacement containing only Clip must not collapse an
		// already-materialized multi-clip scene to one entry.
		if len(previous.Clips) > 0 {
			replacement.Clips = append([]scriptpkg.ClipBinding(nil), previous.Clips...)
			for i := range replacement.Clips {
				if replacement.Clips[i].ClipID == replacement.Clip.ClipID {
					replacement.Clips[i] = mergeClipBinding(previous.Clips[i], *replacement.Clip)
					break
				}
			}
		} else {
			replacement.Clips = []scriptpkg.ClipBinding{*replacement.Clip}
		}
	default:
		replacement.Clips = append([]scriptpkg.ClipBinding(nil), previous.Clips...)
	}
	if len(replacement.Clips) > 0 {
		replacement.Clip = &replacement.Clips[0]
	}

	if previous.Voiceover != nil {
		if replacement.Voiceover == nil {
			replacement.Voiceover = previous.Voiceover
		} else {
			if replacement.Voiceover.Status == "" {
				replacement.Voiceover.Status = previous.Voiceover.Status
			}
			if replacement.Voiceover.Link == "" {
				replacement.Voiceover.Link = previous.Voiceover.Link
			}
			if replacement.Voiceover.LocalPath == "" {
				replacement.Voiceover.LocalPath = previous.Voiceover.LocalPath
			}
			if replacement.Voiceover.DurationMs == 0 {
				replacement.Voiceover.DurationMs = previous.Voiceover.DurationMs
			}
			if len(previous.Voiceover.Links) > 0 {
				links := make(map[string]string, len(previous.Voiceover.Links)+len(replacement.Voiceover.Links))
				for language, link := range previous.Voiceover.Links {
					links[language] = link
				}
				for language, link := range replacement.Voiceover.Links {
					links[language] = link
				}
				replacement.Voiceover.Links = links
			}
			// Per-language timing bundles are additive state: previously
			// published timing links must survive a partial replacement
			// (translation / synthesis / reconciliation). The replacement
			// entry wins per language, but never erases a language that the
			// replacement did not touch.
			if len(previous.Voiceover.Timing) > 0 {
				timing := make(map[string]scriptpkg.VoiceoverTimingBinding, len(previous.Voiceover.Timing)+len(replacement.Voiceover.Timing))
				for language, entry := range previous.Voiceover.Timing {
					timing[language] = entry
				}
				for language, entry := range replacement.Voiceover.Timing {
					timing[language] = entry
				}
				replacement.Voiceover.Timing = timing
			}
		}
	}
	return replacement
}

func mergeClipBinding(previous, replacement scriptpkg.ClipBinding) scriptpkg.ClipBinding {
	if replacement.ClipID == "" {
		replacement.ClipID = previous.ClipID
	}
	if replacement.ClipTitle == "" {
		replacement.ClipTitle = previous.ClipTitle
	}
	if replacement.DriveLink == "" {
		replacement.DriveLink = previous.DriveLink
	}
	if replacement.SubtitleLink == "" {
		replacement.SubtitleLink = previous.SubtitleLink
	}
	if replacement.SubtitleFileID == "" {
		replacement.SubtitleFileID = previous.SubtitleFileID
	}
	if replacement.StartMs == 0 && replacement.EndMs == 0 {
		replacement.StartMs = previous.StartMs
		replacement.EndMs = previous.EndMs
	}
	if replacement.DurationMs == 0 {
		replacement.DurationMs = previous.DurationMs
	}
	return replacement
}

func mergeVidRushSegments(dst, src []scriptpkg.VidRushSegmentResult) []scriptpkg.VidRushSegmentResult {
	if len(src) == 0 {
		return dst
	}
	index := make(map[string]int, len(dst))
	out := make([]scriptpkg.VidRushSegmentResult, 0, len(dst)+len(src))
	for _, seg := range dst {
		if seg.SegmentID == "" {
			out = append(out, seg)
			continue
		}
		if i, exists := index[seg.SegmentID]; exists {
			out[i] = mergeVidRushSegmentResult(out[i], seg)
			continue
		}
		index[seg.SegmentID] = len(out)
		out = append(out, seg)
	}
	for _, seg := range src {
		if seg.SegmentID == "" {
			out = append(out, seg)
			continue
		}
		if i, ok := index[seg.SegmentID]; ok {
			out[i] = mergeVidRushSegmentResult(out[i], seg)
			continue
		}
		index[seg.SegmentID] = len(out)
		out = append(out, seg)
	}
	return out
}

// mergeVidRushSegmentResult preserves provider discoveries when a later
// processor returns only its own asset delta (for example, internet_images
// returning an image candidate after clip_search returned Artlist clips).
// Segment identity is shared, but provider assets are additive state and
// must not be replaced by the last processor to touch the segment.
func mergeVidRushSegmentResult(dst, src scriptpkg.VidRushSegmentResult) scriptpkg.VidRushSegmentResult {
	out := cloneVidRushSegmentResult(dst)
	if src.SceneID != "" {
		out.SceneID = src.SceneID
	}
	if src.Text != "" {
		out.Text = src.Text
	}
	if src.TextHash != "" {
		out.TextHash = src.TextHash
	}
	if len(src.Insights.Entities) > 0 {
		out.Insights.Entities = append([]scriptpkg.ExtractedEntity(nil), src.Insights.Entities...)
	}
	if src.Insights.VisualProfile != nil {
		visualProfile := *src.Insights.VisualProfile
		visualProfile.Terms = append([]string(nil), src.Insights.VisualProfile.Terms...)
		out.Insights.VisualProfile = &visualProfile
	}
	if len(src.Insights.ImportantPhrases) > 0 {
		out.Insights.ImportantPhrases = append([]string(nil), src.Insights.ImportantPhrases...)
	}
	if len(src.Insights.ImportantWords) > 0 {
		out.Insights.ImportantWords = append([]string(nil), src.Insights.ImportantWords...)
	}
	if len(src.Insights.ArtlistQueries) > 0 {
		out.Insights.ArtlistQueries = append([]string(nil), src.Insights.ArtlistQueries...)
	}
	if len(src.Insights.ImageQueries) > 0 {
		out.Insights.ImageQueries = append([]string(nil), src.Insights.ImageQueries...)
	}
	// Provider deltas are normalized against the owning segment before they
	// are merged. A stamped candidate from another segment is discarded
	// instead of being rebound by the last processor to touch this result.
	out.Assets.Candidates = appendProviderCandidatesUnique(out.Assets.Candidates, normalizeVidRushCandidateList(src.Assets.Candidates, out))
	out.Assets.SecondaryImages = appendProviderCandidatesUnique(out.Assets.SecondaryImages, normalizeVidRushCandidateList(src.Assets.SecondaryImages, out))
	out.Assets.GeneratedImages = appendProviderCandidatesUnique(out.Assets.GeneratedImages, normalizeVidRushCandidateList(src.Assets.GeneratedImages, out))
	if src.Assets.PrimaryVideo != nil {
		if primary, ok := normalizeVidRushCandidate(*src.Assets.PrimaryVideo, out); ok {
			out.Assets.PrimaryVideo = &primary
		}
	}
	if src.Assets.SelectionReason != "" {
		out.Assets.SelectionReason = src.Assets.SelectionReason
	}
	if src.Assets.CandidateSetHash != "" {
		out.Assets.CandidateSetHash = src.Assets.CandidateSetHash
	}
	if src.Cache.Extraction != "" {
		out.Cache.Extraction = src.Cache.Extraction
	}
	if src.Cache.Artlist != "" {
		out.Cache.Artlist = src.Cache.Artlist
	}
	if src.Cache.InternetImages != "" {
		out.Cache.InternetImages = src.Cache.InternetImages
	}
	if src.Cache.ImageGeneration != "" {
		out.Cache.ImageGeneration = src.Cache.ImageGeneration
	}
	if src.Cache.Binding != "" {
		out.Cache.Binding = src.Cache.Binding
	}
	normalizeVidRushSegmentAssets(&out)
	return out
}

// cloneSpecSceneSlice returns a shallow copy of the scene slice.
// Bindings is a value struct, so copying the slice element is enough
// to isolate in-place binding mutations from the original backing array.
func cloneSpecSceneSlice(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	if scenes == nil {
		return nil
	}
	out := make([]scriptpkg.SpecScene, len(scenes))
	copy(out, scenes)
	return out
}

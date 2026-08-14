package usecase

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func canonicalClipDriveLink(clip *asset.Asset) string {
	if clip == nil {
		return ""
	}
	if link := strings.TrimSpace(clip.DriveLink()); link != "" {
		if strings.HasPrefix(link, "http://") || strings.HasPrefix(link, "https://") {
			return link
		}
		// Some live catalog rows expose the Drive file ID in the legacy
		// drive_link field. Normalize that representation below.
		if fileID := strings.TrimSpace(clip.DriveFileID()); fileID != "" {
			return "https://drive.google.com/file/d/" + fileID + "/view"
		}
		return "https://drive.google.com/file/d/" + link + "/view"
	}
	// The internal asset ID is never a Drive locator. If the registry has no
	// verified Drive location, keep the link absent and let downstream policy
	// surface the unavailable location explicitly.
	return ""
}

// appendClipSourceText writes the legacy technical per-clip source
// text block used for provenance and compatibility consumers.
func (c *ClipSourceBuilder) appendClipSourceText(w *strings.Builder, id string, clip *asset.Asset, transcript, metadataText string) {
	w.WriteString(fmt.Sprintf("CLIP %s: %s\n", id, clipDisplayName(clip, id)))
	if searchText := strings.TrimSpace(clip.SearchText); searchText != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", searchText))
	} else if desc := strings.TrimSpace(clip.GetMetadataString("description")); desc != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", desc))
	} else if metadataText = strings.TrimSpace(metadataText); metadataText != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", metadataText))
	}
	if transcript != "" {
		excerpt := truncateExcerpt(transcript, excerptMaxRunes)
		w.WriteString(fmt.Sprintf("  Transcript: %s\n", excerpt))
	}
	if len(clip.Tags) > 0 {
		w.WriteString(fmt.Sprintf("  Tags: %s\n", strings.Join(clip.Tags, ", ")))
	}
	w.WriteString("\n")
}

// appendNarrativeClipText writes the model-facing per-clip
// evidence block. The projection is intentionally narration-only:
// no clip IDs, Drive links, tags or source URLs.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// transcript string is PRE-RESOLVED by the caller (the main
// per-clip loop calls resolveTranscript exactly once and
// threads the result through). This method does NOT call
// resolveTranscript itself.
func (c *ClipSourceBuilder) appendNarrativeClipText(w *strings.Builder, position int, clip *asset.Asset, transcript, metadataText string) {
	if w == nil || clip == nil {
		return
	}
	view := buildModelClipView(position, clip, transcript, metadataText)
	w.WriteString(fmt.Sprintf("NARRATIVE EVIDENCE %d\n", position+1))
	w.WriteString(fmt.Sprintf("Ref: %s\n", view.Ref))
	if title := strings.TrimSpace(view.VisualSummary); title != "" {
		w.WriteString(fmt.Sprintf("VisualSummary: %s\n", title))
	}
	if desc := strings.TrimSpace(view.Description); desc != "" {
		w.WriteString(fmt.Sprintf("Description: %s\n", desc))
	}
	if transcript := strings.TrimSpace(view.Transcript); transcript != "" {
		w.WriteString(fmt.Sprintf("Transcript: %s\n", transcript))
	}
	w.WriteString(fmt.Sprintf("DurationMs: %d\n", view.DurationMs))
	w.WriteString("\n")
}

func buildModelClipView(position int, clip *asset.Asset, transcript, metadataText string) scriptpkg.ModelClipView {
	view := scriptpkg.ModelClipView{
		Ref:        fmt.Sprintf("clip_%d", position+1),
		Transcript: truncateExcerpt(strings.TrimSpace(transcript), excerptMaxRunes),
		DurationMs: parseMetadataMs(clip.GetMetadataString("duration_ms")),
	}
	if view.DurationMs <= 0 {
		startMs := parseMetadataMs(clip.GetMetadataString("start_ms"))
		endMs := parseMetadataMs(clip.GetMetadataString("end_ms"))
		if endMs > startMs {
			view.DurationMs = endMs - startMs
		}
	}
	if searchText := strings.TrimSpace(clip.SearchText); searchText != "" {
		view.Description = searchText
	} else if desc := strings.TrimSpace(clip.GetMetadataString("description")); desc != "" {
		view.Description = desc
	} else {
		view.Description = strings.TrimSpace(metadataText)
	}
	if title := strings.TrimSpace(clipDisplayName(clip, "")); title != "" {
		view.VisualSummary = title
	} else {
		view.VisualSummary = view.Description
	}
	return view
}

// appendClipDetail populates the per-clip detail map with the
// primary evidence used for clip-native scene construction.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// transcript string is PRE-RESOLVED by the caller.
func (c *ClipSourceBuilder) appendClipDetail(details map[string]scriptpkg.ClipDetail, id string, clip *asset.Asset, transcript, metadataText string) {
	if details == nil || clip == nil || c == nil {
		return
	}
	desc := strings.TrimSpace(clip.SearchText)
	if desc == "" {
		desc = strings.TrimSpace(clip.GetMetadataString("description"))
	}
	if desc == "" {
		desc = strings.TrimSpace(metadataText)
	}
	startMs, endMs := clipTimeline(clip)
	if startMs < 0 {
		startMs = parseMetadataMs(clip.GetMetadataString("start_ms"))
	}
	if endMs < 0 {
		endMs = parseMetadataMs(clip.GetMetadataString("end_ms"))
	}
	details[id] = scriptpkg.ClipDetail{
		Name:        clipDisplayName(clip, id),
		Description: desc,
		Transcript:  transcript,
		Tags:        append([]string(nil), clip.Tags...),
		StartMs:     startMs,
		EndMs:       endMs,
		DriveLink:   canonicalClipDriveLink(clip),
		LocalPath:   strings.TrimSpace(clip.LocalPath()),
	}
}

// buildClipEvidence assembles the canonical *scriptpkg.ClipEvidence
// surface from the per-loop accumulators.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): takes the
// per-clip resolvedTracks map (keyed by canonical clip ID) and
// populates the 3 new fingerprint fields (LanguageCode,
// TextTrackVersion, TranscriptHash) from the FIRST non-nil
// track. godlike/07 minimum-blast-radius: the fingerprint is
// per-evidence, not per-clip; the "first" track is the
// canonical choice when multiple clips resolve (matches the
// existing per-evidence language convention).
func buildClipEvidence(
	canonicalIDs, clipNames []string,
	clipToCanonical map[string]string,
	clips []*asset.Asset,
	renderableIDs []string,
	excludedClips []scriptpkg.ExcludedClip,
	missingClipIDs []scriptpkg.MissingClipID,
	sourceText string,
	narrativeText string,
	clipDetails map[string]scriptpkg.ClipDetail,
	resolvedTracks map[string]*asset.TextTrack,
) *scriptpkg.ClipEvidence {
	clipDriveLinks := make(map[string]string, len(clips))
	for _, clip := range clips {
		if link := canonicalClipDriveLink(clip); link != "" {
			canonicalID := clipToCanonical[clip.ID]
			if canonicalID == "" {
				canonicalID = clip.ID
			}
			clipDriveLinks[canonicalID] = link
		}
	}

	clipNameMap := make(map[string]string, len(canonicalIDs))
	for i, id := range canonicalIDs {
		if i < len(clipNames) && clipNames[i] != "" {
			clipNameMap[id] = clipNames[i]
		}
	}

	// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): populate
	// the 3 new fingerprint fields from the FIRST non-nil
	// resolved track. When no track is available (legacy
	// path, missing-track path, mixed-with-no-ready), the
	// fields are left empty (the per-evidence fingerprint
	// inherits the per-clip fingerprint only when at least
	// one clip has a READY track).
	var lang, version, hash string
	clipTranscriptHashes := make([]string, 0, len(canonicalIDs))
	for _, id := range canonicalIDs {
		if t, ok := resolvedTracks[id]; ok && t != nil {
			clipTranscriptHashes = append(clipTranscriptHashes, t.TextHash)
			lang = t.LanguageCode
			version = t.SourceVersion
			hash = t.TextHash
			break
		}
	}

	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs:      canonicalIDs,
		RenderableClipIDs:    renderableIDs,
		ClipCount:            len(canonicalIDs),
		AssembledText:        sourceText,
		NarrativeText:        narrativeText,
		DriveLinks:           clipDriveLinks,
		ClipNames:            clipNameMap,
		Excluded:             excludedClips,
		MissingClipIDs:       missingClipIDs,
		ClipTranscriptHashes: clipTranscriptHashes,
		ClipDetails:          clipDetails,
		LanguageCode:         lang,
		TextTrackVersion:     version,
		TranscriptHash:       hash,
	}
	if len(ev.MissingClipIDs) == 0 {
		ev.MissingClipIDs = nil
	}
	if len(ev.Excluded) == 0 {
		ev.Excluded = nil
	}
	if len(ev.RenderableClipIDs) == 0 {
		ev.RenderableClipIDs = nil
	}
	return ev
}

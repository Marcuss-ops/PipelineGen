package usecase

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// appendClipSourceText writes the per-clip source text block
// (CLIP header + Description + Transcript + Tags + blank-line
// terminator) to the source-text writer.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// transcript string is PRE-RESOLVED by the caller (the main
// per-clip loop calls resolveTranscript exactly once and
// threads the result through). This method does NOT call
// resolveTranscript itself.
func (c *ClipSourceBuilder) appendClipSourceText(w *strings.Builder, id string, clip *asset.Asset, transcript string) {
	w.WriteString(fmt.Sprintf("CLIP %s: %s\n", id, clipDisplayName(clip, id)))
	if searchText := strings.TrimSpace(clip.SearchText); searchText != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", searchText))
	} else if desc := strings.TrimSpace(clip.GetMetadataString("description")); desc != "" {
		w.WriteString(fmt.Sprintf("  Description: %s\n", desc))
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

// appendClipDetail populates the per-clip detail map with the
// primary evidence used for clip-native scene construction.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 4 (July 2026): the
// transcript string is PRE-RESOLVED by the caller.
func (c *ClipSourceBuilder) appendClipDetail(details map[string]scriptpkg.ClipDetail, id string, clip *asset.Asset, transcript string) {
	if details == nil || clip == nil || c == nil {
		return
	}
	desc := strings.TrimSpace(clip.SearchText)
	if desc == "" {
		desc = strings.TrimSpace(clip.GetMetadataString("description"))
	}
	startMs := parseMetadataMs(clip.GetMetadataString("start_ms"))
	endMs := parseMetadataMs(clip.GetMetadataString("end_ms"))
	details[id] = scriptpkg.ClipDetail{
		Name:        clipDisplayName(clip, id),
		Description: desc,
		Transcript:  transcript,
		Tags:        append([]string(nil), clip.Tags...),
		StartMs:     startMs,
		EndMs:       endMs,
		DriveLink:   clip.DriveLink(),
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
	clipDetails map[string]scriptpkg.ClipDetail,
	resolvedTracks map[string]*asset.TextTrack,
) *scriptpkg.ClipEvidence {
	clipDriveLinks := make(map[string]string, len(clips))
	for _, clip := range clips {
		if link := clip.DriveLink(); link != "" {
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
	for _, id := range canonicalIDs {
		if t, ok := resolvedTracks[id]; ok && t != nil {
			lang = t.LanguageCode
			version = t.SourceVersion
			hash = t.TextHash
			break
		}
	}

	ev := &scriptpkg.ClipEvidence{
		AcceptedClipIDs:   canonicalIDs,
		RenderableClipIDs: renderableIDs,
		ClipCount:         len(canonicalIDs),
		AssembledText:     sourceText,
		DriveLinks:        clipDriveLinks,
		ClipNames:         clipNameMap,
		Excluded:          excludedClips,
		MissingClipIDs:    missingClipIDs,
		ClipDetails:       clipDetails,
		LanguageCode:      lang,
		TextTrackVersion:  version,
		TranscriptHash:    hash,
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

package script

import (
	"maps"
	"slices"
	"strconv"
	"strings"
)

// ClipEvidence is the canonical clip context produced by clip-based source
// resolvers.
type ClipEvidence struct {
	AcceptedClipIDs      []string              `json:"accepted_clip_ids"`
	RenderableClipIDs    []string              `json:"renderable_clip_ids,omitempty"`
	ClipCount            int                   `json:"clip_count"`
	AssembledText        string                `json:"assembled_text,omitempty"`
	NarrativeText        string                `json:"narrative_text,omitempty"`
	DriveLinks           map[string]string     `json:"drive_links,omitempty"`
	ClipNames            map[string]string     `json:"clip_names,omitempty"`
	Excluded             []ExcludedClip        `json:"excluded,omitempty"`
	MissingClipIDs       []MissingClipID       `json:"missing_clip_ids,omitempty"`
	ClipTranscriptHashes []string              `json:"clip_transcript_hashes,omitempty"`
	ClipDetails          map[string]ClipDetail `json:"clip_details,omitempty"`
	SegmentEvidence      []SegmentClipEvidence `json:"segment_evidence,omitempty"`
	LanguageCode         string                `json:"language_code,omitempty"`
	TextTrackVersion     string                `json:"text_track_version,omitempty"`
	TranscriptHash       string                `json:"transcript_hash,omitempty"`
}

// ModelSourceText returns the narration-safe projection of the evidence.
func (e *ClipEvidence) ModelSourceText() string {
	if e == nil {
		return ""
	}
	// Explicit segment evidence is rendered by buildSegmentInstructions;
	// returning the global narrative projection as well would duplicate the
	// same clips in one ungrouped block and can make the model merge segments.
	if len(e.SegmentEvidence) > 0 {
		return ""
	}
	if text := strings.TrimSpace(e.NarrativeText); text != "" {
		return text
	}
	return ""
}

// CoverageSourceText removes structural labels before editorial-overlap checks.
func (e *ClipEvidence) CoverageSourceText() string {
	if e == nil {
		return ""
	}
	text := strings.TrimSpace(e.NarrativeText)
	if text == "" {
		return ""
	}
	var parts []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "",
			strings.HasPrefix(line, "NARRATIVE EVIDENCE "),
			strings.HasPrefix(line, "Ref:"),
			strings.HasPrefix(line, "VisualSummary:"),
			strings.HasPrefix(line, "Description:"),
			strings.HasPrefix(line, "Transcript:"),
			strings.HasPrefix(line, "DurationMs:"):
			continue
		default:
			parts = append(parts, line)
		}
	}
	return strings.Join(parts, " ")
}

func cloneClipDetails(in map[string]ClipDetail) map[string]ClipDetail {
	if in == nil {
		return nil
	}
	out := make(map[string]ClipDetail, len(in))
	for clipID, detail := range in {
		detail.Tags = slices.Clone(detail.Tags)
		out[clipID] = detail
	}
	return out
}

// NewClipEvidence creates a snapshot-safe copy of the supplied evidence.
func NewClipEvidence(e ClipEvidence) *ClipEvidence {
	e.AcceptedClipIDs = slices.Clone(e.AcceptedClipIDs)
	e.RenderableClipIDs = slices.Clone(e.RenderableClipIDs)
	e.ClipTranscriptHashes = slices.Clone(e.ClipTranscriptHashes)
	e.Excluded = slices.Clone(e.Excluded)
	e.MissingClipIDs = slices.Clone(e.MissingClipIDs)
	e.DriveLinks = maps.Clone(e.DriveLinks)
	e.ClipNames = maps.Clone(e.ClipNames)
	e.ClipDetails = cloneClipDetails(e.ClipDetails)
	if e.SegmentEvidence != nil {
		segments := e.SegmentEvidence
		e.SegmentEvidence = make([]SegmentClipEvidence, len(segments))
		for i, segment := range segments {
			e.SegmentEvidence[i] = segment
			e.SegmentEvidence[i].ClipIDs = slices.Clone(segment.ClipIDs)
			e.SegmentEvidence[i].Clips = cloneClipDetails(segment.Clips)
		}
	}
	return &e
}

// SegmentClipEvidence keeps the caller's editorial grouping alongside the
// resolved clip evidence. An empty ClipIDs list is intentional: a segment can
// be narrative-only while the surrounding source still uses clips.
type SegmentClipEvidence struct {
	SegmentID  string                `json:"segment_id,omitempty"`
	Kind       string                `json:"kind,omitempty"`
	Topic      string                `json:"topic,omitempty"`
	SourceText string                `json:"source_text,omitempty"`
	ClipIDs    []string              `json:"clip_ids,omitempty"`
	Clips      map[string]ClipDetail `json:"clips,omitempty"`
}

// BuildSegmentClipEvidence projects resolved clip details into the exact
// segment ownership declared by the caller. It never searches or reallocates
// clips; missing details remain absent and are surfaced by the resolver.
func BuildSegmentClipEvidence(segments []ScriptSegment, evidence *ClipEvidence) []SegmentClipEvidence {
	if len(segments) == 0 || evidence == nil {
		return nil
	}
	out := make([]SegmentClipEvidence, len(segments))
	for i, segment := range segments {
		segmentID := strings.TrimSpace(segment.ID)
		if segmentID == "" {
			segmentID = "segment-" + strconv.Itoa(i+1)
		}
		out[i] = SegmentClipEvidence{
			SegmentID:  segmentID,
			Kind:       strings.TrimSpace(segment.Kind),
			Topic:      segment.Topic,
			SourceText: segment.SourceText,
			ClipIDs:    slices.Clone(segment.ClipIDs),
			Clips:      make(map[string]ClipDetail, len(segment.ClipIDs)),
		}
		for _, clipID := range segment.ClipIDs {
			if detail, ok := evidence.ClipDetails[clipID]; ok {
				out[i].Clips[clipID] = detail
			}
		}
		if len(out[i].Clips) == 0 {
			out[i].Clips = nil
		}
	}
	return out
}

// ClipDetail carries the primary evidence for one accepted clip.
type ClipDetail struct {
	Name           string   `json:"name,omitempty"`
	Description    string   `json:"description,omitempty"`
	Transcript     string   `json:"transcript,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	StartMs        int64    `json:"start_ms,omitempty"`
	EndMs          int64    `json:"end_ms,omitempty"`
	DriveLink      string   `json:"drive_link,omitempty"`
	SubtitleLink   string   `json:"subtitle_link,omitempty"`
	SubtitleFileID string   `json:"subtitle_file_id,omitempty"`
}

// ModelClipView is the model-facing projection of one clip.
type ModelClipView struct {
	Ref           string `json:"ref"`
	Description   string `json:"description,omitempty"`
	VisualSummary string `json:"visual_summary,omitempty"`
	Transcript    string `json:"transcript,omitempty"`
	DurationMs    int64  `json:"duration_ms,omitempty"`
}

type ExcludedClip struct {
	ClipID string `json:"clip_id"`
	Reason string `json:"reason"`
}

type MissingClipID struct {
	ClipID string `json:"clip_id"`
	Reason string `json:"reason"`
}

const (
	MissingClipReasonNotFound      = "not_found"
	MissingClipReasonDriveNotFound = "drivenotfound"
)

type SearchResultItem struct {
	ClipID    string  `json:"clip_id"`
	AssetID   string  `json:"asset_id,omitempty"`
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	Source    string  `json:"source"`
	DriveLink string  `json:"drive_link,omitempty"`
}

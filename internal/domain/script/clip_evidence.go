package script

import (
	"maps"
	"slices"
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
	LanguageCode         string                `json:"language_code,omitempty"`
	TextTrackVersion     string                `json:"text_track_version,omitempty"`
	TranscriptHash       string                `json:"transcript_hash,omitempty"`
}

// ModelSourceText returns the narration-safe projection of the evidence.
func (e *ClipEvidence) ModelSourceText() string {
	if e == nil {
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

// NewClipEvidence creates a snapshot-safe copy of the supplied evidence.
func NewClipEvidence(e ClipEvidence) *ClipEvidence {
	e.AcceptedClipIDs = slices.Clone(e.AcceptedClipIDs)
	e.RenderableClipIDs = slices.Clone(e.RenderableClipIDs)
	e.ClipTranscriptHashes = slices.Clone(e.ClipTranscriptHashes)
	e.Excluded = slices.Clone(e.Excluded)
	e.MissingClipIDs = slices.Clone(e.MissingClipIDs)
	e.DriveLinks = maps.Clone(e.DriveLinks)
	e.ClipNames = maps.Clone(e.ClipNames)
	e.ClipDetails = maps.Clone(e.ClipDetails)
	return &e
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

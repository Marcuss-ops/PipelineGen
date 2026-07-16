// Package visualanalysis owns the import contract emitted by the CPU-first
// AI stock analyser. It converts the external document into canonical domain
// visual events without creating transcript rows or leaking Drive locators
// into semantic text.
package visualanalysis

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
)

const SchemaVersion = "ai_stock_visual_analysis.v1"

type Document struct {
	SchemaVersion    string          `json:"schema_version"`
	Asset            AssetInput      `json:"asset"`
	Visual           VisualInput     `json:"visual_analysis"`
	TimedEvents      []EventInput    `json:"timed_events"`
	RecommendedClips []ClipInput     `json:"recommended_clips"`
	SoundCues        []SoundCueInput `json:"sound_cues"`
}
type AssetInput struct {
	ProposedAssetID        string  `json:"proposed_asset_id"`
	Source                 string  `json:"source"`
	AssetRole              string  `json:"asset_role"`
	MediaType              string  `json:"media_type"`
	FolderPath             string  `json:"folder_path"`
	NormalizedGroup        string  `json:"normalized_group"`
	Title                  string  `json:"title"`
	DurationMs             int64   `json:"duration_ms"`
	Width                  int     `json:"width"`
	Height                 int     `json:"height"`
	FPS                    float64 `json:"fps"`
	Orientation            string  `json:"orientation"`
	HasAudio               bool    `json:"has_audio"`
	HasDialogue            bool    `json:"has_dialogue"`
	DialogueConfidence     float64 `json:"dialogue_confidence"`
	NeedsAudioManualReview bool    `json:"needs_audio_manual_review"`
	AudioProfile           string  `json:"audio_profile"`
	PreserveNativeAudio    bool    `json:"preserve_native_audio"`
}
type VisualInput struct {
	SummaryEN   string   `json:"summary_en"`
	SummaryIT   string   `json:"summary_it"`
	Subjects    []string `json:"subjects"`
	Environment []string `json:"environment"`
	Actions     []string `json:"actions"`
}
type EventInput struct {
	SequenceNo int      `json:"sequence_no"`
	StartMs    int64    `json:"start_ms"`
	EndMs      int64    `json:"end_ms"`
	EventType  string   `json:"event_type"`
	ActionEN   string   `json:"action_en"`
	ActionIT   string   `json:"action_it"`
	Subjects   []string `json:"subjects"`
	Confidence float64  `json:"confidence"`
}
type ClipInput struct {
	ClipKey     string `json:"clip_key"`
	StartMs     int64  `json:"start_ms"`
	EndMs       int64  `json:"end_ms"`
	DurationMs  int64  `json:"duration_ms"`
	Title       string `json:"title"`
	Purpose     string `json:"purpose"`
	Recommended bool   `json:"recommended"`
}
type SoundCueInput struct {
	EventSequenceNo     int     `json:"event_sequence_no"`
	TriggerMs           int64   `json:"trigger_ms"`
	EndMs               int64   `json:"end_ms"`
	SoundIntent         string  `json:"sound_intent"`
	SoundType           string  `json:"sound_type"`
	Intensity           float64 `json:"intensity"`
	Policy              string  `json:"policy"`
	PreserveNativeAudio bool    `json:"preserve_native_audio"`
}

func Parse(data []byte) (Document, error) {
	var d Document
	if err := json.Unmarshal(data, &d); err != nil {
		return d, fmt.Errorf("visual analysis: decode: %w", err)
	}
	if err := d.Validate(); err != nil {
		return d, err
	}
	return d, nil
}

func (d Document) Validate() error {
	if d.SchemaVersion != SchemaVersion {
		return fmt.Errorf("visual analysis: unsupported schema_version %q", d.SchemaVersion)
	}
	if d.Asset.ProposedAssetID == "" || d.Asset.Source != string(asset.SourceAIGenerated) || d.Asset.AssetRole != asset.AssetRoleStock || d.Asset.MediaType != "video" {
		return fmt.Errorf("visual analysis: invalid AI stock identity")
	}
	if d.Asset.DurationMs <= 0 || d.Asset.Width <= 0 || d.Asset.Height <= 0 || d.Asset.FPS <= 0 {
		return fmt.Errorf("visual analysis: invalid media dimensions")
	}
	if d.Asset.HasDialogue {
		return fmt.Errorf("visual analysis: dialogue assets require the speech pipeline")
	}
	if !strings.HasPrefix(d.Asset.FolderPath, "Stock/AI/") || d.Asset.NormalizedGroup != asset.NormalizedGroupStock {
		return fmt.Errorf("visual analysis: invalid Stock/AI routing")
	}
	last := int64(-1)
	for i, e := range d.TimedEvents {
		if e.SequenceNo != i || e.StartMs < 0 || e.EndMs <= e.StartMs || e.EndMs > d.Asset.DurationMs || e.StartMs < last || e.ActionEN == "" {
			return fmt.Errorf("visual analysis: invalid timed_events[%d]", i)
		}
		last = e.EndMs
	}
	for i, c := range d.RecommendedClips {
		if c.EndMs <= c.StartMs || c.DurationMs != c.EndMs-c.StartMs || c.EndMs > d.Asset.DurationMs {
			return fmt.Errorf("visual analysis: invalid recommended_clips[%d]", i)
		}
	}
	return nil
}

func (d Document) VisualAnalysis() asset.VisualAnalysis {
	events := make([]asset.VisualEvent, len(d.TimedEvents))
	for i, e := range d.TimedEvents {
		events[i] = asset.VisualEvent{StartMs: e.StartMs, EndMs: e.EndMs, Text: e.ActionEN}
	}
	return asset.VisualAnalysis{Summary: d.Visual.SummaryEN, Events: events}
}

func (d Document) Metadata() asset.AIStockMetadata {
	return asset.AIStockMetadata{AssetRole: d.Asset.AssetRole, NormalizedGroup: d.Asset.NormalizedGroup, FolderPath: d.Asset.FolderPath, VisualSummary: d.Visual.SummaryEN, Subjects: d.Visual.Subjects, Actions: d.Visual.Actions, HasDialogue: d.Asset.HasDialogue, HasNativeAudio: d.Asset.HasAudio, AudioProfile: asset.AudioProfile(d.Asset.AudioProfile)}
}

var driveIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,}$`)

func DriveFileID(ref string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := range parts {
		if parts[i] == "d" && i+1 < len(parts) && driveIDPattern.MatchString(parts[i+1]) {
			return parts[i+1], nil
		}
	}
	return "", fmt.Errorf("visual analysis: no Drive file id in %q", ref)
}
func FolderCategory(folderPath string) string {
	parts := strings.Split(strings.Trim(path.Clean(folderPath), "/"), "/")
	if len(parts) >= 3 {
		return parts[2]
	}
	return "Generico"
}

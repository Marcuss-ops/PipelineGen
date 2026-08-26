package detail

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	SourceAIGenerated    asset.Source = "ai_generated"
	AssetRoleStock                    = "stock"
	NormalizedGroupStock              = "stock"
)

var AIStockLanguages = []string{"it", "pl", "ru", "de", "es", "pt-BR", "fr", "tr", "en", "id"}

type AudioProfile string

const (
	AudioProfileUnknown           AudioProfile = "unknown"
	AudioProfileSilent            AudioProfile = "silent"
	AudioProfileAmbientAndEffects AudioProfile = "ambient_and_effects"
	AudioProfileSpeech            AudioProfile = "speech"
	AudioProfileMusic             AudioProfile = "music"
	AudioProfileMixed             AudioProfile = "mixed"
)

type AIStockMetadata struct {
	AssetRole       string       `json:"asset_role"`
	NormalizedGroup string       `json:"normalized_group"`
	FolderPath      string       `json:"folder_path"`
	ShortSummary    string       `json:"short_summary,omitempty"`
	VisualSummary   string       `json:"visual_summary,omitempty"`
	Subjects        []string     `json:"subjects,omitempty"`
	Actions         []string     `json:"actions,omitempty"`
	Location        string       `json:"location,omitempty"`
	Mood            string       `json:"mood,omitempty"`
	CameraMotion    string       `json:"camera_motion,omitempty"`
	ShotType        string       `json:"shot_type,omitempty"`
	Loopable        bool         `json:"loopable"`
	HasDialogue     bool         `json:"has_dialogue"`
	HasNativeAudio  bool         `json:"has_native_audio"`
	AudioProfile    AudioProfile `json:"audio_profile"`
}

func AIStockFolderPath(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		category = "Generico"
	}
	return path.Join("Stock", "AI", category)
}

func (m AIStockMetadata) Validate() error {
	if m.AssetRole != AssetRoleStock {
		return fmt.Errorf("ai stock: asset_role must be %q", AssetRoleStock)
	}
	if m.NormalizedGroup != NormalizedGroupStock {
		return fmt.Errorf("ai stock: normalized_group must be %q", NormalizedGroupStock)
	}
	if !strings.HasPrefix(m.FolderPath, "Stock/AI/") {
		return fmt.Errorf("ai stock: folder_path must be under Stock/AI")
	}
	if m.AudioProfile == "" {
		return fmt.Errorf("ai stock: audio_profile is required")
	}
	return nil
}

type VisualEvent struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}
type VisualAnalysis struct {
	Summary string
	Events  []VisualEvent
}

func (a VisualAnalysis) Validate(durationMs int64) error {
	last := int64(-1)
	for i, e := range a.Events {
		if e.StartMs < 0 || e.EndMs <= e.StartMs || (durationMs > 0 && e.EndMs > durationMs) || e.StartMs < last || strings.TrimSpace(e.Text) == "" {
			return fmt.Errorf("visual event %d: invalid interval or text", i)
		}
		last = e.EndMs
	}
	return nil
}

func (a VisualAnalysis) SortedEvents() []VisualEvent {
	out := append([]VisualEvent(nil), a.Events...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

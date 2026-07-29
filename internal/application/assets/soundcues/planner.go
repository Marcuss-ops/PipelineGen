// Package soundcues contains the shared, deterministic visual-action to SFX
// intent planner. It never selects a Drive file; SoundResolver owns that.
package soundcues

import (
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

type Policy string

const (
	PolicyNativeOnly  Policy = "native_only"
	PolicyReplace     Policy = "replace"
	PolicyEnhanceOnly Policy = "enhance_only"
	PolicyOverlay     Policy = "overlay"
	PolicySilent      Policy = "silent"
)

type Cue struct {
	EventRef            string  `json:"event_ref"`
	TriggerMs           int64   `json:"trigger_ms"`
	EndMs               int64   `json:"end_ms"`
	SoundIntent         string  `json:"sound_intent"`
	SoundType           string  `json:"sound_type"`
	Intensity           float64 `json:"intensity"`
	Placement           string  `json:"placement"`
	MixMode             string  `json:"mix_mode"`
	PreserveNativeAudio bool    `json:"preserve_native_audio"`
}

type Planner struct{ DefaultPolicy Policy }

func NewPlanner() Planner { return Planner{DefaultPolicy: PolicyEnhanceOnly} }

func (p Planner) Plan(events []asset.VisualEvent, nativeAudio bool, policy Policy) ([]Cue, error) {
	if policy == "" {
		policy = p.DefaultPolicy
	}
	if policy == PolicySilent || policy == PolicyNativeOnly {
		return nil, nil
	}
	if policy != PolicyReplace && policy != PolicyEnhanceOnly && policy != PolicyOverlay {
		return nil, fmt.Errorf("sound cues: unsupported policy %q", policy)
	}
	out := make([]Cue, 0, len(events))
	for i, event := range events {
		intent, kind, ok := intentFor(event.Text)
		if !ok {
			continue
		}
		out = append(out, Cue{EventRef: fmt.Sprintf("visual-event-%d", i), TriggerMs: event.StartMs, EndMs: event.EndMs, SoundIntent: intent, SoundType: kind, Intensity: 0.85, Placement: "sync_to_action", MixMode: "overlay", PreserveNativeAudio: nativeAudio && policy != PolicyReplace})
	}
	return out, nil
}

func intentFor(text string) (string, string, bool) {
	s := strings.ToLower(text)
	switch {
	case strings.Contains(s, "punch"), strings.Contains(s, "box"):
		return "powerful boxing glove impacts on a heavy bag", "impact_sequence", true
	case strings.Contains(s, "door") || strings.Contains(s, "close"):
		return "door closing impact", "impact", true
	case strings.Contains(s, "accelerat") || strings.Contains(s, "car"):
		return "car acceleration pass-by", "vehicle", true
	case strings.Contains(s, "fall") || strings.Contains(s, "drop"):
		return "object falling impact", "impact", true
	case strings.Contains(s, "explod"):
		return "cinematic explosion", "impact", true
	default:
		return "", "", false
	}
}

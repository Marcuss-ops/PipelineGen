// Package usecase — quality_gate_segments.go
//
// Segment-narrative rule of the editorial quality gate: caller-declared
// segment coverage, topic mention and cross-segment topic bleed.
package usecase

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// segmentNarrativeChecker runs the post-generation semantic gate for
// caller-declared segments.
type segmentNarrativeChecker struct{}

func (segmentNarrativeChecker) Name() string { return "segment_narrative" }

func (segmentNarrativeChecker) Check(in qualityGateInput) []string {
	return explicitSegmentNarrativeReasons(in.result, in.plan)
}

// explicitSegmentNarrativeReasons is the post-generation semantic gate for
// caller-declared segments. It runs on the final SpecScene surface, after
// direct stock normalization has assigned canonical segment IDs. Boxer plans
// additionally require the declared subject and reject another declared boxer
// in the same scene, catching the Floyd/Sugar Ray boundary failure.
func explicitSegmentNarrativeReasons(result *scriptpkg.GenerationResult, plan scriptpkg.ResolvedGenerationPlan) []string {
	if result == nil || len(plan.Segments) == 0 {
		return nil
	}
	scenes := result.Output.SpecScene.Scenes
	if len(scenes) != len(plan.Segments) {
		return []string{fmt.Sprintf("explicit segment scene count mismatch: got %d, want %d", len(scenes), len(plan.Segments))}
	}

	bySegmentID := make(map[string]scriptpkg.SpecScene, len(scenes))
	for _, scene := range scenes {
		if id := strings.TrimSpace(scene.SegmentID); id != "" {
			bySegmentID[id] = scene
		}
	}
	var reasons []string
	for i, segment := range plan.Segments {
		scene := scenes[i]
		if id := strings.TrimSpace(segment.ID); id != "" {
			if mapped, ok := bySegmentID[id]; ok {
				scene = mapped
			}
		}
		words := len(strings.Fields(scene.Text))
		// Clip-backed scenes are narrator intros, not documentary chapters.
		// Do not require the generic 100-word chapter minimum: that minimum
		// caused short, valid Gemma introductions to fail after generation.
		if words == 0 {
			reasons = append(reasons, fmt.Sprintf("segment %q is empty", segment.ID))
		}
		if plan.ClipEvidence == nil && words < 100 {
			reasons = append(reasons, fmt.Sprintf("segment %q contains fewer than 100 words", segment.ID))
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(segment.ID)), "boxer-") {
			continue
		}
		body := strings.ToLower(strings.Join(strings.Fields(scene.Text), " "))
		topic := strings.ToLower(strings.TrimSpace(segment.Topic))
		if topic != "" && !strings.Contains(body, topic) {
			reasons = append(reasons, fmt.Sprintf("segment %q does not mention its declared topic %q", segment.ID, segment.Topic))
		}
		for _, other := range plan.Segments {
			if other.ID == segment.ID || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(other.ID)), "boxer-") {
				continue
			}
			otherTopic := strings.ToLower(strings.TrimSpace(other.Topic))
			if otherTopic != "" && strings.Contains(body, otherTopic) {
				reasons = append(reasons, fmt.Sprintf("segment %q contains the declared topic of %q", segment.ID, other.ID))
			}
		}
	}
	return reasons
}

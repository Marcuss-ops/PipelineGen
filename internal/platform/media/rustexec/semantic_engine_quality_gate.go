package rustexec

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// qualityGateWireRequest is the minimal projection of the Go generation
// contract that the Rust script-qa worker evaluates. Unknown fields are
// ignored by serde, while every field below uses the same JSON name as the
// canonical Go types.
type qualityGateWireRequest struct {
	Operation string                `json:"operation"`
	Item      qualityGateWireItem   `json:"item"`
	Plan      qualityGateWirePlan   `json:"plan"`
	Result    qualityGateWireResult `json:"result"`
}

type qualityGateWireItem struct {
	ID string `json:"id"`
}

type qualityGateWirePlan struct {
	Language         string                          `json:"language"`
	SourceText       string                          `json:"source_text"`
	TargetWords      int                             `json:"target_words"`
	TranslateTo      string                          `json:"translate_to"`
	GroundingPolicy  string                          `json:"grounding_policy"`
	SourceKind       string                          `json:"source_kind"`
	NumClips         int                             `json:"num_clips"`
	Segments         []scriptpkg.ScriptSegment       `json:"segments,omitempty"`
	ClipEvidence     *scriptpkg.ClipEvidence         `json:"clip_evidence,omitempty"`
	ResearchEvidence *scriptpkg.ResearchEvidencePack `json:"research_evidence,omitempty"`
}

type qualityGateWireArtifacts struct {
	Entities *scriptpkg.EntityResult `json:"entities,omitempty"`
}

type qualityGateWireSpecScene struct {
	Scenes []scriptpkg.SpecScene `json:"scenes"`
}

type qualityGateWireOutput struct {
	Text      string                   `json:"text"`
	WordCount int                      `json:"word_count"`
	SpecScene qualityGateWireSpecScene `json:"specscene"`
}

type qualityGateWireResult struct {
	Output    qualityGateWireOutput    `json:"output"`
	Artifacts qualityGateWireArtifacts `json:"artifacts"`
}

type qualityGateWireResponse struct {
	OK      bool                         `json:"ok"`
	Error   string                       `json:"error,omitempty"`
	Quality *scriptpkg.GenerationQuality `json:"quality"`
	Reasons []string                     `json:"reasons"`
}

// EvaluateQualityGate runs the Rust script-qa quality gate through the same
// semantic-engine worker used by the other BrainRust adapters. A protocol
// failure is returned as a regular error (the finalizer fails closed); a gate
// verdict is returned as the canonical *GenerationQuality plus a typed
// *QualityGateError when the verdict did not pass.
func (a *SemanticEngineAdapter) EvaluateQualityGate(
	ctx context.Context,
	result *scriptpkg.GenerationResult,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
) (*scriptpkg.GenerationQuality, error) {
	if result == nil {
		return nil, nil
	}
	if a == nil || a.executor == nil {
		return nil, fmt.Errorf("semantic-engine: executor is not configured")
	}

	wire := buildQualityGateRequest(result, item, plan)
	payload, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: encode quality_gate: %w", err)
	}
	stdout, _, err := a.executor.Run(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("semantic-engine: quality_gate execute: %w", err)
	}
	var response qualityGateWireResponse
	if err := json.Unmarshal(stdout, &response); err != nil {
		return nil, fmt.Errorf("semantic-engine: decode quality_gate: %w", err)
	}
	if !response.OK {
		return nil, fmt.Errorf("semantic-engine: quality_gate worker: %s", strings.TrimSpace(response.Error))
	}
	if response.Quality == nil {
		return nil, nil
	}
	if response.Quality.Passed {
		if len(response.Reasons) > 0 {
			return response.Quality, fmt.Errorf("semantic-engine: quality_gate returned a passing verdict with reasons")
		}
		return response.Quality, nil
	}
	return response.Quality, &scriptpkg.QualityGateError{
		ItemID:  item.ID,
		Reasons: response.Reasons,
		Quality: *response.Quality,
	}
}

// buildQualityGateRequest projects the canonical Go generation contract onto
// the script-qa wire shape. It is the single request builder: the adapter and
// the Go↔Rust parity gate both use it, so the parity corpus exercises exactly
// the payload production sends.
func buildQualityGateRequest(
	result *scriptpkg.GenerationResult,
	item scriptpkg.GenerationItemV2,
	plan scriptpkg.ResolvedGenerationPlan,
) qualityGateWireRequest {
	return qualityGateWireRequest{
		Operation: "quality_gate",
		Item:      qualityGateWireItem{ID: item.ID},
		Plan: qualityGateWirePlan{
			Language:         plan.Language,
			SourceText:       plan.SourceText,
			TargetWords:      plan.TargetWords,
			TranslateTo:      plan.TranslateTo,
			GroundingPolicy:  plan.GroundingPolicy,
			SourceKind:       plan.SourceKind,
			NumClips:         plan.NumClips,
			Segments:         plan.Segments,
			ClipEvidence:     plan.ClipEvidence,
			ResearchEvidence: plan.ResearchEvidence,
		},
		Result: qualityGateWireResult{
			Output: qualityGateWireOutput{
				Text:      result.Output.Text,
				WordCount: result.Output.WordCount,
				SpecScene: qualityGateWireSpecScene{Scenes: nonNilScenes(result.Output.SpecScene.Scenes)},
			},
			Artifacts: qualityGateWireArtifacts{
				Entities: result.Artifacts.Entities,
			},
		},
	}
}

func nonNilScenes(scenes []scriptpkg.SpecScene) []scriptpkg.SpecScene {
	if scenes == nil {
		return []scriptpkg.SpecScene{}
	}
	return scenes
}

var _ scriptpkg.QualityGateEvaluator = (*SemanticEngineAdapter)(nil)

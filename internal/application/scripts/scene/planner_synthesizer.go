package scene

import (
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
	"go.uber.org/zap"
)

// NarrativeDraft is the minimal planner input shape kept for the
// binder's clip-native synthesis path.
type NarrativeDraft struct {
	Text       string
	Scenes     []scriptpkg.SpecScene
	SourceKind string
}

// ScenePlan is the planner output shape.
type ScenePlan struct {
	Scenes      []scriptpkg.SpecScene
	Synthesized bool
	Suppressed  bool
}

type ScenePlanner struct {
	log *zap.Logger
}

func NewScenePlanner(log *zap.Logger) *ScenePlanner {
	return &ScenePlanner{log: log}
}

// PlanFromClipEvidence builds a deterministic scene per accepted
// clip. It does not perform prose fallback; the binder only uses
// this helper when the caller supplied clip evidence but no scenes.
func (p *ScenePlanner) PlanFromClipEvidence(plan *scriptpkg.ResolvedGenerationPlan) []scriptpkg.SpecScene {
	if plan == nil || plan.ClipEvidence == nil {
		return nil
	}
	clipIDs := clipIDsFromPlan(plan)
	if len(clipIDs) == 0 {
		return nil
	}

	ev := plan.ClipEvidence
	scenes := make([]scriptpkg.SpecScene, len(clipIDs))
	for i, clipID := range clipIDs {
		detail, ok := ev.ClipDetails[clipID]
		if !ok {
			detail = scriptpkg.ClipDetail{
				Name:      ev.ClipNames[clipID],
				DriveLink: ev.DriveLinks[clipID],
			}
		}

		text := strings.TrimSpace(detail.Transcript)
		if text == "" {
			text = strings.TrimSpace(detail.Description)
		}
		if text == "" {
			text = strings.TrimSpace(detail.Name)
		}
		if text == "" {
			text = fmt.Sprintf("Scene %d", i+1)
		}

		kind := scriptpkg.SceneClip
		if len(clipIDs) >= 3 {
			switch i {
			case 0:
				kind = scriptpkg.SceneIntro
			case len(clipIDs) - 1:
				kind = scriptpkg.SceneOutro
			}
		}

		binding := &scriptpkg.ClipBinding{
			ClipID:     clipID,
			ClipTitle:  detail.Name,
			DriveLink:  detail.DriveLink,
			StartMs:    detail.StartMs,
			EndMs:      detail.EndMs,
			DurationMs: scriptpkg.ClipDurationMs(detail.StartMs, detail.EndMs),
		}
		if binding.DurationMs <= 0 {
			binding.DurationMs = scriptpkg.ClipDurationMsFromAssetID(clipID)
		}
		if binding.ClipTitle == "" {
			binding.ClipTitle = ev.ClipNames[clipID]
		}
		if binding.DriveLink == "" {
			binding.DriveLink = ev.DriveLinks[clipID]
		}

		scenes[i] = scriptpkg.SpecScene{
			ID:           fmt.Sprintf("scene-%s", clipID),
			Index:        i,
			Text:         text,
			Kind:         kind,
			EvidenceRefs: []string{"slot-" + itoaLen(i+1)},
			Bindings: scriptpkg.SceneBindings{
				Clip: binding,
			},
		}
	}

	return scenes
}

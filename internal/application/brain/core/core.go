// Package core is the canonical orchestrator of the Brain capability.
//
// core.go wires the four brain services (normalizer, intent
// resolver, ranker, planner) with the MediaMemoryResolutionPort to
// implement the Brain port. It performs no IO directly: every
// backend access flows through the MediaMemoryResolutionPort.
package core

import (
	"context"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/application/brain"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/intent"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/normalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/planner"
	"github.com/Marcuss-ops/PipelineGen/internal/application/brain/ranker"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/media"
)

// orchestratorVersion is the canonical version stamp of the Brain
// orchestrator. It is the only version constant owned by core.go
// because the orchestrator itself is the canonical source of truth
// for its behaviour.
const orchestratorVersion = media.VersionBrain

// CanonicalBrain is the canonical implementation of the Brain port.
// It composes the four pure brain services and delegates candidate
// search to the MediaMemoryResolutionPort, which is the only place
// where IO happens.
type CanonicalBrain struct {
	normalizer normalizer.PhraseNormalizer
	resolver   intent.VisualIntentResolver
	searcher   brain.MediaMemoryResolutionPort
	ranker     ranker.CandidateRanker
	planner    planner.SceneVisualPlanner
}

// NewCanonicalBrain constructs a CanonicalBrain from the four brain
// services and a MediaMemoryResolutionPort. Any nil dependency
// triggers a panic at construction time (fail-fast) so a mis-wired
// composition is caught at boot.
func NewCanonicalBrain(
	n normalizer.PhraseNormalizer,
	res intent.VisualIntentResolver,
	searcher brain.MediaMemoryResolutionPort,
	r ranker.CandidateRanker,
	p planner.SceneVisualPlanner,
) *CanonicalBrain {
	if n == nil {
		panic("brain: normalizer is required")
	}
	if res == nil {
		panic("brain: intent resolver is required")
	}
	if searcher == nil {
		panic("brain: candidate searcher is required")
	}
	if r == nil {
		panic("brain: ranker is required")
	}
	if p == nil {
		panic("brain: planner is required")
	}
	return &CanonicalBrain{
		normalizer: n,
		resolver:   res,
		searcher:   searcher,
		ranker:     r,
		planner:    p,
	}
}

// Compile-time assertion: CanonicalBrain satisfies Brain.
var _ brain.Brain = (*CanonicalBrain)(nil)

// Resolve is the single canonical entry point. It processes every
// scene in the request, normalizing text, resolving intent,
// searching candidates, ranking them, and producing a visual plan.
// Each scene carries its own ResolutionTrace and DecisionFingerprint
// so every decision is versioned and reproducible.
// Errors from any step surface in the per-scene plan status and the
// trace, and processing continues with the remaining scenes.
func (b *CanonicalBrain) Resolve(ctx context.Context, req brain.BrainRequest) (brain.BrainResult, error) {
	result := brain.BrainResult{
		ProjectID: req.ProjectID,
	}

	for _, scene := range req.Scenes {
		scenePlan := b.resolveScene(ctx, req.Language, scene, req.Policy)
		result.Scenes = append(result.Scenes, scenePlan)
	}

	return result, nil
}

func (b *CanonicalBrain) resolveScene(ctx context.Context, language string, scene brain.SceneRequest, policy brain.ResolutionPolicy) brain.SceneVisualPlan {
	versions := brain.ResolutionVersionSet{
		BrainVersion:            orchestratorVersion,
		NormalizerVersion:       b.normalizer.Version(),
		IntentResolverVersion:   b.resolver.Version(),
		EmbeddingVersion:        b.searcher.EmbeddingVersion(),
		RankingPolicyVersion:    b.ranker.Version(),
		DiversityPolicyVersion:  b.ranker.DiversityPolicyVersion(),
		SlotPolicyVersion:       b.planner.Version(),
		ProviderRegistryVersion: asset.ProviderRegistryVersion,
	}

	trace := &brain.ResolutionTrace{
		Versions: versions,
	}

	// 1. Normalize phrase.
	normalized, err := b.normalizer.Normalize(ctx, language, scene.Text)
	if err != nil {
		trace.Reasons = append(trace.Reasons, "normalizer failed: "+err.Error())
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Status:  "error",
			Trace:   *trace,
		}
	}
	trace.NormalizedText = normalized.Normalized

	// 2. Resolve visual intent.
	// Pass both the original scene text and the normalized text so
	// the resolver can detect capitalisation-based entities while
	// building keywords from the canonical lowercase form.
	intentOut, err := b.resolver.Resolve(ctx, language, scene.Text, normalized.Normalized)
	if err != nil {
		trace.Reasons = append(trace.Reasons, "intent resolver failed: "+err.Error())
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Status:  "error",
			Trace:   *trace,
		}
	}

	// 3. Search candidates through the canonical port.
	query := brain.SearchQuery{
		Text:         strings.Join(intentOut.Keywords, " "),
		Language:     language,
		MediaTypes:   mediaTypesForSlots(scene.Slots),
		Limit:        policy.MaxCandidatesPerSlot,
		SearchPolicy: policy.SearchPolicy,
	}
	searchResult, err := b.searcher.Search(ctx, query)
	if err != nil {
		trace.Reasons = append(trace.Reasons, "search failed: "+err.Error())
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Intent:  intentOut,
			Status:  "error",
			Trace:   *trace,
		}
	}
	trace.BackendCalls = append(trace.BackendCalls, brain.BackendCall{
		Backend: "search",
		Hits:    len(searchResult.Candidates),
	})

	// 4. Rank candidates.
	ranked, err := b.ranker.Rank(ctx, scene, intentOut, searchResult.Candidates, policy)
	if err != nil {
		trace.Reasons = append(trace.Reasons, "ranker failed: "+err.Error())
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Intent:  intentOut,
			Status:  "error",
			Trace:   *trace,
		}
	}

	// 5. Plan visual layers.
	plan, err := b.planner.Plan(ctx, scene, intentOut, ranked)
	if err != nil {
		trace.Reasons = append(trace.Reasons, "planner failed: "+err.Error())
		return brain.SceneVisualPlan{
			SceneID: scene.ID,
			Intent:  intentOut,
			Status:  "error",
			Trace:   *trace,
		}
	}

	for _, layer := range plan.Layers {
		trace.Selected = append(trace.Selected, brain.SelectedRecord{
			Slot:        layer.Slot,
			AssetID:     layer.AssetID,
			CandidateID: layer.CandidateID,
			Score:       layer.Score,
		})
	}

	plan.Trace = *trace
	plan.DecisionFingerprint = versions.DecisionFingerprint(language, trace.NormalizedText)

	return plan
}

func mediaTypesForSlots(slots []media.SlotKind) []string {
	var out []string
	seen := make(map[string]struct{})
	for _, slot := range slots {
		mt := ""
		switch slot {
		case media.SlotPrimaryVideo:
			mt = "video"
		case media.SlotSecondaryImage, media.SlotEvidenceOverlay, media.SlotMap,
			media.SlotPortrait, media.SlotDocument, media.SlotBackground:
			mt = "image"
		}
		if mt == "" {
			continue
		}
		if _, ok := seen[mt]; ok {
			continue
		}
		seen[mt] = struct{}{}
		out = append(out, mt)
	}
	return out
}

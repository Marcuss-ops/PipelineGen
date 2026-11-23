package adapters

import (
	"context"
	"fmt"

	visual "github.com/Marcuss-ops/PipelineGen/internal/capabilities/visual"
	mediadomain "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// VisualSlotsProcessor resolves timeline-level intro and post-segment clips
// into the same SpecScene envelope consumed by persistence and rendering.
type VisualSlotsProcessor struct {
	planner visual.Planner
}

func NewVisualSlotsProcessor(planner visual.Planner) *VisualSlotsProcessor {
	return &VisualSlotsProcessor{planner: planner}
}

func (p *VisualSlotsProcessor) Name() ProcessorName { return ProcessorVisualSlots }
func (p *VisualSlotsProcessor) Policy(plan *scriptpkg.ResolvedGenerationPlan) ProcessorPolicy {
	if plan != nil && (plan.MediaPlan.Intro != nil || len(plan.MediaPlan.PostSegments) > 0) {
		// A requested timeline is part of the caller's media contract. Invalid
		// manual positions, duplicates, or unavailable closed candidates must
		// fail the job instead of producing a successful document with missing
		// visual assignments.
		return ProcessorRequired
	}
	return ProcessorBestEffort
}

func (p *VisualSlotsProcessor) Process(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput) (*PostProcessResult, error) {
	if plan == nil || (plan.MediaPlan.Intro == nil && len(plan.MediaPlan.PostSegments) == 0) {
		return &PostProcessResult{}, nil
	}
	seed := int64(0)
	if plan.MediaPlan.Variation != nil {
		seed = plan.MediaPlan.Variation.Seed
	}
	var all []mediadomain.VisualAssignment
	var warnings []string
	if plan.MediaPlan.Intro != nil && !sceneIsFixedMedia(input.SpecScene, "", "", 0) {
		res, err := resolveSlot(ctx, plan, input, "", mediadomain.VisualSlotIntro, *plan.MediaPlan.Intro, seed, p.planner)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Assignments...)
		warnings = append(warnings, res.Warnings...)
	}
	for _, post := range plan.MediaPlan.PostSegments {
		res, err := resolveSlot(ctx, plan, input, post.SegmentID, mediadomain.VisualSlotPostSegment, post.VisualSlotPlan, seed, p.planner)
		if err != nil {
			return nil, err
		}
		all = append(all, res.Assignments...)
		warnings = append(warnings, res.Warnings...)
	}
	updated := input.SpecScene
	updated.VisualAssignments = append([]mediadomain.VisualAssignment(nil), all...)
	ProjectPostSegmentClipBindings(updated.Scenes, all)
	return &PostProcessResult{VisualAssignments: all, UpdatedSpecScene: updated, Warnings: warnings, Changed: len(all) > 0}, nil
}

// ProjectPostSegmentClipBindings exposes the primary post-segment clip in the
// per-scene binding surface as well as in the independent timeline contract.
// The timeline remains the authoritative place for multiple clips (for
// example the two Sugar Ray Robinson outro clips); ClipBinding is singular,
// so it carries the first clip while VisualAssignments retains every clip and
// its exact position.
func ProjectPostSegmentClipBindings(scenes []scriptpkg.SpecScene, assignments []mediadomain.VisualAssignment) {
	for _, assignment := range assignments {
		if assignment.Slot != mediadomain.VisualSlotPostSegment || assignment.AssetID == "" || assignment.Position != 0 {
			continue
		}
		for i := range scenes {
			matchesScene := assignment.SceneID != "" && scenes[i].ID == assignment.SceneID
			matchesSegment := assignment.SegmentID != "" && scenes[i].SegmentID == assignment.SegmentID
			if !matchesScene && !matchesSegment {
				continue
			}
			if !scenes[i].AllowsMediaReplacement() {
				continue
			}
			scenes[i].Bindings.Clip = &scriptpkg.ClipBinding{
				ClipID:     assignment.AssetID,
				StartMs:    assignment.StartMs,
				EndMs:      assignment.StartMs + assignment.DurationMs,
				DurationMs: assignment.DurationMs,
			}
			break
		}
	}
}

func resolveSlot(ctx context.Context, plan *scriptpkg.ResolvedGenerationPlan, input ProcessInput, segmentID string, slot mediadomain.VisualSlot, spec mediadomain.VisualSlotPlan, seed int64, planner visual.Planner) (visual.Result, error) {
	if sceneIsFixedMedia(input.SpecScene, "", segmentID, 0) {
		return visual.Result{}, nil
	}
	ids := append([]string(nil), spec.CandidateAssetIDs...)
	for _, clip := range spec.Clips {
		if clip.AssetID != "" {
			ids = append(ids, clip.AssetID)
		}
	}
	candidates := make([]visual.Candidate, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			candidates = append(candidates, visual.Candidate{AssetID: id})
		}
	}
	return visual.Resolve(ctx, visual.Request{SceneID: sceneIDForSegment(input.SpecScene.Scenes, plan.Segments, segmentID), SegmentID: segmentID, Slot: slot, Plan: spec, Candidates: candidates, Seed: seed, PromptVersion: plan.PromptVersion, ForceRefresh: plan.MediaPlan.ForceRefreshBindings}, planner)
}

func sceneIDForSegment(scenes []scriptpkg.SpecScene, segments []scriptpkg.ScriptSegment, segmentID string) string {
	for _, scene := range scenes {
		if segmentID != "" && scene.SegmentID == segmentID {
			return scene.ID
		}
	}
	// Explicit segments are normalized into scene slots by the stock
	// bindings processor, which runs after this processor. Resolve the
	// scene identity by the canonical segment order instead of falling back
	// to scene-0 while that normalization is still pending.
	for i, segment := range segments {
		if segmentID != "" && segment.ID == segmentID {
			if i < len(scenes) {
				return scenes[i].ID
			}
			// The generated prose may contain fewer scenes than the
			// explicit segment contract. Stock binding normalization
			// creates the missing canonical slot as scene-i later.
			return fmt.Sprintf("scene-%d", i)
		}
	}
	if len(scenes) > 0 {
		return scenes[0].ID
	}
	return ""
}

// ProcessorName is a typed identifier for a registered postprocessor.
type ProcessorName string

// ProcessorPolicy classifies a postprocessor's failure mode for
// composition and preflight decisions.
type ProcessorPolicy string

const (
	// ProcessorRequired marks a processor whose missing-registered
	// status is a composition-level failure (composition refuses
	// to start) AND whose runtime error or empty output causes the
	// overall Run to return a non-nil error.
	ProcessorRequired ProcessorPolicy = "required"

	// ProcessorBestEffort marks a processor whose missing-registered
	// status is a non-fatal warning (composition continues) AND
	// whose runtime error or empty output is a warning rather than
	// a hard failure.
	ProcessorBestEffort ProcessorPolicy = "best_effort"
)

// Canonical postprocessor names. Each concrete processor's Name()
// method MUST return one of these constants. This is the SOLE
// canonical home for every postprocessor identifier in the package
// (godlike/06 SSOT one-owner-per-fact) — do NOT redeclare these in
// sibling files.
const (
	ProcessorClipSearch                  ProcessorName = "clip_search"
	ProcessorMetadata                    ProcessorName = "metadata"
	ProcessorTranslation                 ProcessorName = "translation"
	ProcessorClipBindings                ProcessorName = "clip_bindings"
	ProcessorStockBindings               ProcessorName = "stock_bindings"
	ProcessorAssetLocationReconciliation ProcessorName = "asset_location_reconciliation"
	ProcessorVisualPlanning              ProcessorName = "visual_planning"
	ProcessorVisualSlots                 ProcessorName = "visual_slots"
	ProcessorVoiceover                   ProcessorName = "voiceover"
	ProcessorImages                      ProcessorName = "images"
	ProcessorInternetImages              ProcessorName = "internet_images"
	ProcessorVidRushMaterialization      ProcessorName = "vidrush_materialization"
	ProcessorPersistence                 ProcessorName = "persistence"
	ProcessorNarrationSanitizer          ProcessorName = "narration_sanitizer"
)

// ProcessorDescriptor is the single source of truth for a
// postprocessor's canonical metadata.
type ProcessorDescriptor struct {
	Name   ProcessorName
	Policy ProcessorPolicy
	Order  int
	Active bool
}

// canonicalDescriptors is the authoritative registry of postprocessors
// metadata. The Order field encodes the canonical EXECUTION order
// (the order the registry walks processors in Run()).
var canonicalDescriptors = []ProcessorDescriptor{
	{Name: ProcessorClipSearch, Policy: ProcessorBestEffort, Order: 0, Active: true},
	{Name: ProcessorMetadata, Policy: ProcessorRequired, Order: 2, Active: true},
	{Name: ProcessorTranslation, Policy: ProcessorBestEffort, Order: 3, Active: true},
	{Name: ProcessorClipBindings, Policy: ProcessorBestEffort, Order: 4, Active: true},
	{Name: ProcessorStockBindings, Policy: ProcessorRequired, Order: 5, Active: true},
	{Name: ProcessorVisualPlanning, Policy: ProcessorBestEffort, Order: 6, Active: true},
	{Name: ProcessorVisualSlots, Policy: ProcessorBestEffort, Order: 7, Active: true},
	{Name: ProcessorVoiceover, Policy: ProcessorBestEffort, Order: 8, Active: true},
	{Name: ProcessorImages, Policy: ProcessorBestEffort, Order: 9, Active: true},
	{Name: ProcessorInternetImages, Policy: ProcessorBestEffort, Order: 10, Active: true},
	{Name: ProcessorVidRushMaterialization, Policy: ProcessorBestEffort, Order: 11, Active: true},
	{Name: ProcessorAssetLocationReconciliation, Policy: ProcessorBestEffort, Order: 12, Active: true},
	{Name: ProcessorPersistence, Policy: ProcessorRequired, Order: 13, Active: true},
	{Name: ProcessorNarrationSanitizer, Policy: ProcessorRequired, Order: 14, Active: true},
}

// descriptorByName provides O(1) lookup over canonicalDescriptors.
var descriptorByName = func() map[ProcessorName]ProcessorDescriptor {
	m := make(map[ProcessorName]ProcessorDescriptor, len(canonicalDescriptors))
	for _, d := range canonicalDescriptors {
		m[d.Name] = d
	}
	return m
}()

// DefaultPolicyFor returns the canonical default policy for a named
// postprocessor. Returns empty string for unknown names — callers
// MUST treat unknown names as a hard fail or warn per their own
// classification logic.
func DefaultPolicyFor(name ProcessorName) ProcessorPolicy {
	if d, ok := descriptorByName[name]; ok {
		return d.Policy
	}
	return ""
}

// IsCanonicalProcessor reports whether name is part of the canonical set.

// CanonicalProcessorNames returns the closed set of all active
// canonical postprocessors in their canonical EXECUTION order.
func CanonicalProcessorNames() []ProcessorName {
	names := make([]ProcessorName, 0, len(canonicalDescriptors))
	for _, d := range canonicalDescriptors {
		if !d.Active {
			continue
		}
		names = append(names, d.Name)
	}
	return names
}

// RequiredProcessorNames returns the names of all active canonical
// processors whose default policy is Required.
func RequiredProcessorNames() []ProcessorName {
	var names []ProcessorName
	for _, d := range canonicalDescriptors {
		if !d.Active || d.Policy != ProcessorRequired {
			continue
		}
		names = append(names, d.Name)
	}
	return names
}

// ProcessorNamesToStrings converts a typed slice to plain []string.
// Used at the boundary where ResolvedGenerationPlan.Postprocessors
// (which stays []string in the domain layer) receives the output of
// buildPostprocessorList.
func ProcessorNamesToStrings(names []ProcessorName) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	return out
}

// ProcessorNamesFromStrings converts a []string to typed
// []ProcessorName. Non-canonical strings are preserved as typed
// values; callers should validate with IsCanonicalProcessor if needed.
func ProcessorNamesFromStrings(names []string) []ProcessorName {
	out := make([]ProcessorName, len(names))
	for i, n := range names {
		out[i] = ProcessorName(n)
	}
	return out
}

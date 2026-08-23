// Package visual resolves timeline visual slots without provider or renderer
// dependencies. Gemma receives only the closed candidate set supplied here.
package visual

import (
	"context"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"math/rand"
	"sort"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
)

type Candidate struct {
	AssetID    string
	DurationMs int64
}

type PlannerRequest struct {
	Slot             media.VisualSlot
	SegmentID        string
	Goal             string
	TargetDurationMs int64
	MaxClips         int
	CandidateIDs     []string
	LockedAssetIDs   []string
}

type PlannerResult struct {
	SelectedAssetIDs []string
	Reason           string
}

type Planner interface {
	Select(context.Context, PlannerRequest) (PlannerResult, error)
}

type Request struct {
	SceneID        string
	SegmentID      string
	Slot           media.VisualSlot
	Plan           media.VisualSlotPlan
	Candidates     []Candidate
	Seed           int64
	PromptVersion  string
	ForceRefresh   bool
	PreviousAssets map[string]bool
}

type Result struct {
	Assignments []media.VisualAssignment
	Warnings    []string
}

// Resolve applies Manual first, then Gemma, then a deterministic sampler.
// Locked assignments are reserved at their requested positions and never
// passed to Planner.
func Resolve(ctx context.Context, req Request, planner Planner) (Result, error) {
	if !req.Slot.IsValid() {
		return Result{}, fmt.Errorf("unknown visual slot %q", req.Slot)
	}
	candidates := uniqueCandidates(req.Candidates)
	allowed := make(map[string]Candidate, len(candidates))
	for _, c := range candidates {
		if c.AssetID != "" {
			allowed[c.AssetID] = c
		}
	}
	closed := make(map[string]bool)
	manual := make(map[string]bool)
	for _, clip := range req.Plan.Clips {
		if clip.AssetID != "" {
			manual[clip.AssetID] = true
		}
	}
	for _, id := range req.Plan.CandidateAssetIDs {
		if id != "" {
			closed[id] = true
		}
	}
	if len(closed) > 0 {
		for id := range allowed {
			if !closed[id] && !manual[id] {
				delete(allowed, id)
			}
		}
		for id := range closed {
			if _, ok := allowed[id]; !ok {
				return Result{}, fmt.Errorf("candidate asset %q is unavailable", id)
			}
		}
	}

	max := req.Plan.MaxClips
	if max <= 0 {
		max = len(req.Plan.Clips)
		if max == 0 {
			max = len(allowed)
		}
	}
	assignments := make([]media.VisualAssignment, 0, max)
	used := map[string]bool{}
	positions := map[int]bool{}
	for i, clip := range req.Plan.Clips {
		if clip.AssetID == "" {
			return Result{}, fmt.Errorf("manual clip %d has empty asset_id", i)
		}
		if _, ok := allowed[clip.AssetID]; !ok && len(allowed) > 0 {
			return Result{}, fmt.Errorf("manual asset %q is not in the candidate registry", clip.AssetID)
		}
		if used[clip.AssetID] {
			return Result{}, fmt.Errorf("duplicate manual asset %q", clip.AssetID)
		}
		pos := i
		if clip.Position != nil {
			pos = *clip.Position
		}
		if pos < 0 || positions[pos] {
			return Result{}, fmt.Errorf("invalid or duplicate manual position %d", pos)
		}
		if pos >= max {
			return Result{}, fmt.Errorf("manual position %d exceeds max clips %d", pos, max)
		}
		positions[pos] = true
		used[clip.AssetID] = true
		assignments = append(assignments, assignment(req, clip.AssetID, pos, clip.DurationMs, clip.StartMs, clip.Locked, media.VisualSelectedByUser, "manual"))
	}

	mode := req.Plan.Mode
	needAI := mode == media.VisualSelectionGemma || mode == media.VisualSelectionAutomatic || mode == media.VisualSelectionAuto || mode == media.VisualSelectionAssisted || mode == media.VisualSelectionHybrid
	remaining := max - len(assignments)
	if remaining > 0 && needAI {
		ids := availableIDs(allowed, used)
		if planner != nil && !req.ForceRefresh {
			locked := make([]string, 0)
			for _, a := range assignments {
				if a.Locked {
					locked = append(locked, a.AssetID)
				}
			}
			p, err := planner.Select(ctx, PlannerRequest{Slot: req.Slot, SegmentID: req.SegmentID, Goal: req.Plan.Goal, TargetDurationMs: req.Plan.TargetDurationMs, MaxClips: remaining, CandidateIDs: ids, LockedAssetIDs: locked})
			if err == nil {
				valid := validateSelection(p.SelectedAssetIDs, ids, remaining)
				for _, id := range valid {
					used[id] = true
					pos := firstFreePosition(positions, max)
					positions[pos] = true
					assignments = append(assignments, assignment(req, id, pos, allowed[id].DurationMs, 0, false, media.VisualSelectedByGemma, p.Reason))
				}
			} else {
				return samplerResult(req, allowed, used, positions, assignments, max, fmt.Sprintf("gemma unavailable: %v", err))
			}
		}
	}
	if len(assignments) < max {
		return samplerResult(req, allowed, used, positions, assignments, max, "deterministic fallback")
	}
	sort.Slice(assignments, func(i, j int) bool { return assignments[i].Position < assignments[j].Position })
	return Result{Assignments: assignments}, nil
}

func samplerResult(req Request, allowed map[string]Candidate, used map[string]bool, positions map[int]bool, out []media.VisualAssignment, max int, warning string) (Result, error) {
	ids := availableIDs(allowed, used)
	r := rand.New(rand.NewSource(req.Seed))
	r.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	for len(out) < max && len(ids) > 0 {
		id := ids[0]
		ids = ids[1:]
		pos := firstFreePosition(positions, max)
		positions[pos] = true
		out = append(out, assignment(req, id, pos, allowed[id].DurationMs, 0, false, media.VisualSelectedBySampler, warning))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Position < out[j].Position })
	return Result{Assignments: out, Warnings: []string{warning}}, nil
}

func assignment(req Request, id string, pos int, duration, start int64, locked bool, by media.VisualSelectedBy, reason string) media.VisualAssignment {
	raw := fmt.Sprintf("%s|%s|%s|%s|%d|%d", req.SceneID, req.SegmentID, req.Slot, id, pos, req.Seed)
	h := digest.SHA256Bytes([]byte(raw))
	return media.VisualAssignment{AssignmentID: h[:12], MediaType: media.VisualMediaTypeClip, SceneID: req.SceneID, SegmentID: req.SegmentID, Slot: req.Slot, AssetID: id, Position: pos, DurationMs: duration, StartMs: start, Locked: locked, SelectedBy: by, SelectionReason: reason, VariationSeed: req.Seed, PromptVersion: req.PromptVersion}
}

func uniqueCandidates(in []Candidate) []Candidate {
	out := make([]Candidate, 0, len(in))
	seen := map[string]bool{}
	for _, c := range in {
		if c.AssetID != "" && !seen[c.AssetID] {
			seen[c.AssetID] = true
			out = append(out, c)
		}
	}
	return out
}
func availableIDs(m map[string]Candidate, used map[string]bool) []string {
	out := make([]string, 0, len(m))
	for id := range m {
		if !used[id] {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
func validateSelection(selected, allowed []string, max int) []string {
	set := map[string]bool{}
	for _, id := range allowed {
		set[id] = true
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range selected {
		if len(out) >= max {
			break
		}
		if set[id] && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
func firstFreePosition(used map[int]bool, max int) int {
	for i := 0; i < max; i++ {
		if !used[i] {
			return i
		}
	}
	return max
}

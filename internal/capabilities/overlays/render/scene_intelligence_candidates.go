package render

// Package overlays — scene_intelligence_candidates.go owns the Scene
// Intelligence → planner-input mapping.
//
// It creates NO new selector, NO new renderer kind and NO second planner:
// every signal becomes one of the existing planner-input categories
// (phrase, keyword, number, multi_entity, timeline_labels) that BuildPlan
// and the Visual Admission Resolver already own. Admission, budget,
// collision and timing gates stay exactly where they are; this mapping only
// proposes candidates. Deterministic: sorted outputs, no randomness.

import (
	capoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	"sort"
	"strings"
)

// SICandidateCategory is one of the existing planner-input categories.
type SICandidateCategory string

const (
	SICategoryPhrase      SICandidateCategory = "phrase"
	SICategoryKeyword     SICandidateCategory = "keyword"
	SICategoryNumber      SICandidateCategory = "number"
	SICategoryMultiEntity SICandidateCategory = "multi_entity"
)

// SICandidate is one proposed planner input derived from manifest signals.
// Evidence carries the source sentence ids; Text carries ONLY script words
// (values only when parsed from text, never invented).
type SICandidate struct {
	Category SICandidateCategory
	SceneID  string
	Text     string
	Value    *float64
	Score    float64
	Evidence []int
}

// ComparisonCandidates maps one detected comparison to planner inputs:
// quantitative → one number candidate per parsed value (verified values
// only); qualitative → one multi_entity split-screen candidate and NO
// number. A qualitative comparison therefore can never invent numbers. A
// negated comparison keeps its [negated] tag so it is never treated as a
// positive claim downstream.
func ComparisonCandidates(sceneID string, subjects []string, candidateVisual string, values []float64, evidence []int, score float64, negated bool) []SICandidate {
	sub := append([]string(nil), subjects...)
	sort.Strings(sub)
	ev := append([]int(nil), evidence...)
	sort.Ints(ev)
	text := strings.Join(sub, " vs ")
	if negated {
		text += " [negated]"
	}
	if candidateVisual == "value_comparison" && len(values) > 0 && !negated {
		out := make([]SICandidate, 0, len(values))
		for _, v := range values {
			v := v
			out = append(out, SICandidate{
				Category: SICategoryNumber, SceneID: sceneID,
				Text:  text,
				Value: &v, Score: score, Evidence: ev,
			})
		}
		return out
	}
	return []SICandidate{{
		Category: SICategoryMultiEntity, SceneID: sceneID,
		Text: text, Score: score, Evidence: ev,
	}}
}

// CausalCandidates maps one causal relation to a keyword candidate carrying
// the verbatim-derived cause→effect text. Sequence markers ("dopo") arrive
// here as precedes-relations, preserving the sequence/causation split. A
// hypothetical relation is tagged and its score halved: the renderer gate
// must never draw it as a certain arrow.
func CausalCandidates(sceneID, cause, effect, relation string, evidence []int, score float64, hypothetical bool) []SICandidate {
	text := strings.TrimSpace(cause)
	if effect != "" {
		text += " → " + strings.TrimSpace(effect)
	}
	ev := append([]int(nil), evidence...)
	sort.Ints(ev)
	if hypothetical {
		text += " [hypothetical]"
		score /= 2
	}
	return []SICandidate{{
		Category: SICategoryKeyword, SceneID: sceneID,
		Text: text + " [" + relation + "]", Score: score, Evidence: ev,
	}}
}

// TimelineLabelCandidates maps orderable temporal events to keyword label
// candidates in chronological order (narration order preserved alongside in
// Evidence). Relative-only events (no absolute year) yield nothing: an
// ambiguous "three years later" is never converted to a year.
func TimelineLabelCandidates(sceneID string, years []int, labels []string, evidence []int, score float64) []SICandidate {
	if len(years) == 0 {
		return nil
	}
	type pair struct {
		year  int
		label string
		ev    int
	}
	pairs := make([]pair, 0, len(years))
	for i, y := range years {
		label := ""
		if i < len(labels) {
			label = labels[i]
		}
		ev := 0
		if i < len(evidence) {
			ev = evidence[i]
		}
		pairs = append(pairs, pair{y, label, ev})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].year < pairs[j].year })
	out := make([]SICandidate, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, SICandidate{
			Category: SICategoryKeyword, SceneID: sceneID,
			Text: p.label, Score: score, Evidence: []int{p.ev},
		})
	}
	return out
}

// DensityOverlapBudget translates the density hint into the existing
// per-scene overlap budget: dense scenes admit fewer simultaneous items.
// It only narrows the planner's own budget, never widens it.
func DensityOverlapBudget(densityScore float64, plannerDefault int) int {
	if plannerDefault <= 0 {
		plannerDefault = capoverlay.DefaultOverlapBudget
	}
	if densityScore >= 0.66 && plannerDefault > 2 {
		return 2
	}
	return plannerDefault
}

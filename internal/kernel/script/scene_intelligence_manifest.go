// Merged 2026-10-10 with scene_optimization.go: the manifest and the
// optimization decisions derived from it are one scene-intelligence surface.
package script

// Scene Intelligence canonical manifest (analysis-only, shadow mode).
//
// Single shared analysis → specialized detectors → ONE manifest → Visual
// Admission → RenderingGen. Seven modular capabilities, never seven
// pipelines: no new NER, no new embedding engine, no second MediaMemory
// ranking, no second overlay planner. Detectors read the shared sentence
// index and the existing NLP/semantic context; planners consume this
// manifest through the existing admission and timeline gates.
//
// The Rust module (`pipelinegen-muscles::scene_intelligence`) is the
// authority for text signals; this file is the Go-side canonical contract
// (JSON shape, relation registry, validation, fingerprint). Shadow mode:
// manifests are saved without changing videos; rendering wiring belongs to
// a later explicit phase.

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// SceneIntelligenceManifestVersion is the canonical manifest schema.
const SceneIntelligenceManifestVersion = "scene_intelligence.v1"

// SceneIntelligenceAnalyzerVersion pins the signal implementation.
const SceneIntelligenceAnalyzerVersion = "scene_intelligence.v1"

// RelationRegistryVersion is the single typed contract shared by
// comparisons, causal links and the entity graph.
const RelationRegistryVersion = "relation_registry.v1"

// MaxRelationNodesPerScene caps one screen composition, not the knowledge
// graph: the full graph may be larger, only relevant nodes show.
const MaxRelationNodesPerScene = 6

// RelationRegistry kinds: the ONLY relation vocabulary detectors may emit.
// Comparisons, causal links and entity edges share these strings instead of
// inventing one structure each.
const (
	RelationCauses        = "causes"
	RelationContributesTo = "contributes_to"
	RelationPrecedes      = "precedes"
	RelationContrastsWith = "contrasts_with"
	RelationOwns          = "owns"
	RelationFunds         = "funds"
	RelationPartnersWith  = "partners_with"
	RelationCompetesWith  = "competes_with"
)

// IsKnownRelation reports whether kind belongs to the registry.
func IsKnownRelation(kind string) bool {
	switch kind {
	case RelationCauses, RelationContributesTo, RelationPrecedes,
		RelationContrastsWith, RelationOwns, RelationFunds,
		RelationPartnersWith, RelationCompetesWith:
		return true
	}
	return false
}

// SIDensity is the per-scene information-load signal. Score is 0..1 over
// DISTINCT signals (repetition never inflates it). SuggestedVisualBudget is
// "low" for cognitively dense scenes (few simultaneous overlays) else
// "standard": a budget hint for Visual Admission, never a render order.
type SIDensity struct {
	SceneID               string  `json:"scene_id"`
	Score                 float64 `json:"score"`
	Level                 string  `json:"level"`
	NewEntities           int     `json:"new_entities"`
	NumericClaims         int     `json:"numeric_claims"`
	NewConcepts           int     `json:"new_concepts"`
	SuggestedVisualBudget string  `json:"suggested_visual_budget"`
}

// SIComparison is a detected comparison. Qualitative comparisons MUST carry
// no values: a split-screen is shown, never invented numbers. Quantitative
// ones carry ONLY parsed values.
type SIComparison struct {
	SceneID             string    `json:"scene_id"`
	Kind                string    `json:"kind"`
	Subjects            []string  `json:"subjects"`
	Dimension           string    `json:"dimension"`
	Relation            string    `json:"relation"`
	EvidenceSentenceIDs []int     `json:"evidence_sentence_ids"`
	CandidateVisual     string    `json:"candidate_visual"`
	Values              []float64 `json:"values,omitempty"`
	// Negated preserves "non è superiore": a negated comparison is never
	// treated as a positive claim downstream.
	Negated bool `json:"negated,omitempty"`
}

// SICausalRelation is a directed textual relation. ExtractionConfidence
// measures extraction from text, never real-world truth.
type SICausalRelation struct {
	Kind                 string  `json:"kind"`
	Cause                string  `json:"cause"`
	Effect               string  `json:"effect"`
	Relation             string  `json:"relation"`
	EvidenceSentenceIDs  []int   `json:"evidence_sentence_ids"`
	ExtractionConfidence float64 `json:"extraction_confidence"`
	// Attribution carries the verbatim source ("Secondo gli analisti").
	// Hypothetical marks conditional claims ("avrebbe provocato") that
	// must never render as certain arrows.
	Attribution  *string `json:"attribution,omitempty"`
	Hypothetical bool    `json:"hypothetical,omitempty"`
}

// SITemporalEvent is one historical mention. NormalizedYear is set ONLY for
// explicit absolute years; relative expressions stay relative (nil) unless
// resolved against a reliable anchor. Video-playback time lives on the
// scene, never here: event time and video time are separate axes.
type SITemporalEvent struct {
	EventID            string `json:"event_id"`
	TextSpan           string `json:"text_span"`
	TemporalExpression string `json:"temporal_expression"`
	NormalizedYear     *int   `json:"normalized_year,omitempty"`
	// NormalizedEndYear spans intervals ("2020–2023"): never collapsed
	// to an instant. Video-playback time lives on the scene, never here.
	NormalizedEndYear *int    `json:"normalized_end_year,omitempty"`
	Certainty         float64 `json:"certainty"`
	NarrationIndex    int     `json:"narration_index"`
}

// SIEntityRelation is a typed entity edge. Co-mention without a marker
// yields no relation: mentioning two companies is not a partnership.
type SIEntityRelation struct {
	Subject             string `json:"subject"`
	Object              string `json:"object"`
	Relation            string `json:"relation"`
	EvidenceSentenceIDs []int  `json:"evidence_sentence_ids"`
}

// SISceneSignals bundles one scene's signals.
type SISceneSignals struct {
	SceneID        string             `json:"scene_id"`
	Density        SIDensity          `json:"density"`
	Comparisons    []SIComparison     `json:"comparisons,omitempty"`
	Causal         []SICausalRelation `json:"causal,omitempty"`
	TemporalEvents []SITemporalEvent  `json:"temporal_events,omitempty"`
	Relations      []SIEntityRelation `json:"relations,omitempty"`
}

// SceneIntelligenceManifest is the single analysis product planners consume.
type SceneIntelligenceManifest struct {
	SchemaVersion           string           `json:"schema_version"`
	AnalyzerVersion         string           `json:"analyzer_version"`
	RelationRegistryVersion string           `json:"relation_registry_version"`
	Scenes                  []SISceneSignals `json:"scenes"`
}

// Validate performs fail-closed structural checks (no semantic judgment).
func (m *SceneIntelligenceManifest) Validate() error {
	if m == nil {
		return errors.New("nil scene intelligence manifest")
	}
	if m.SchemaVersion != SceneIntelligenceManifestVersion {
		return fmt.Errorf("unknown scene intelligence schema %q", m.SchemaVersion)
	}
	if strings.TrimSpace(m.AnalyzerVersion) == "" || strings.TrimSpace(m.RelationRegistryVersion) == "" {
		return errors.New("scene intelligence manifest requires version pins")
	}
	seen := map[string]struct{}{}
	for _, s := range m.Scenes {
		if strings.TrimSpace(s.SceneID) == "" {
			return errors.New("scene with empty id")
		}
		if _, dup := seen[s.SceneID]; dup {
			return fmt.Errorf("duplicate scene %q", s.SceneID)
		}
		seen[s.SceneID] = struct{}{}
		if s.Density.Score < 0 || s.Density.Score > 1 {
			return fmt.Errorf("density out of range in %q", s.SceneID)
		}
		for _, c := range s.Comparisons {
			if c.Kind == "comparison" && c.CandidateVisual == "split_comparison" && len(c.Values) != 0 {
				return fmt.Errorf("qualitative comparison must not carry values in %q", s.SceneID)
			}
			if !IsKnownRelation(c.Relation) {
				return fmt.Errorf("unknown relation %q in %q", c.Relation, s.SceneID)
			}
		}
		for _, c := range s.Causal {
			if !IsKnownRelation(c.Relation) {
				return fmt.Errorf("unknown relation %q in %q", c.Relation, s.SceneID)
			}
		}
		for _, r := range s.Relations {
			if !IsKnownRelation(r.Relation) {
				return fmt.Errorf("unknown relation %q in %q", r.Relation, s.SceneID)
			}
		}
		if len(s.Relations) > MaxRelationNodesPerScene {
			return fmt.Errorf("too many relation nodes in %q", s.SceneID)
		}
	}
	return nil
}

// SceneIDs returns sorted scene ids for deterministic iteration.
func (m *SceneIntelligenceManifest) SceneIDs() []string {
	if m == nil {
		return nil
	}
	out := make([]string, 0, len(m.Scenes))
	for _, s := range m.Scenes {
		out = append(out, s.SceneID)
	}
	sort.Strings(out)
	return out
}

// SignalsFor returns the signals of one scene, or nil.
func (m *SceneIntelligenceManifest) SignalsFor(sceneID string) *SISceneSignals {
	if m == nil {
		return nil
	}
	for i := range m.Scenes {
		if m.Scenes[i].SceneID == sceneID {
			cp := m.Scenes[i]
			return &cp
		}
	}
	return nil
}

// SIFingerprintInput covers everything determining a manifest: scene texts,
// entity surfaces, language, analyzer and registry versions. Word counts and
// durations may differ per language — the fingerprint must not.
type SIFingerprintInput struct {
	SceneIDs       []string
	SceneTexts     []string
	EntitySurfaces [][]string
	Language       string
	Analyzer       string
	Registry       string
}

// ComputeSIFingerprint hashes the canonical JSON of the full input.
func ComputeSIFingerprint(in SIFingerprintInput) (string, error) {
	if len(in.SceneIDs) == 0 {
		return "", errors.New("scene intelligence fingerprint requires scenes")
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	return digest.SHA256Bytes(raw), nil
}

// Montage optimization: content facts in, montage decisions out.
//
// A 20-second scene with phrases, companies, a percentage, a comparison,
// clips and compatible animations is a constrained selection problem, not a
// pile of display rules. Per candidate i:
//
//	x_i in {0,1} (shown / discarded)
//	max sum x_i (R_i + Q_i + N_i - D_i)
//	R relevance to speech, Q visual quality, N novelty vs previous
//	elements, D complexity/repetition penalty.
//
// Constraints (objective, automatically verifiable):
//
//	Overlap(t) <= K            (at most K items at any moment)
//	Start(i) >= SpeechAnchor(i) (never before the certified spoken anchor)
//	Duration(i) >= MinReadable(i) (legibility floor)
//
// Local-vs-global: picking every scene's best animation independently can
// still make a bad video (five bar charts in a row). The sequence planner
// optimizes the whole run:
//
//	max sum Q(v_i,s_i) - lambda sum C(v_{i-1},v_i)
//	Q compatibility of visual v_i with scene s_i, C repetition/transition
//	penalty. Solved exactly by dynamic programming (Viterbi) over the
//	scene chain.
//
// Validity (correct, synchronized, legible) is checked exactly; estimated
// quality (pertinent, clear, coherent) is scored; perceived quality (does a
// real viewer find it engaging) needs humans or empirical data and is OUT
// of scope here. Same script, three montages: Documentary / News /
// Educational change only objectives and constraints, never facts.

// OptCandidate is one show/discard contender.
type OptCandidate struct {
	ID            string
	SceneID       string
	R, Q, N, D    float64
	StartMS       int64
	EndMS         int64
	AnchorMS      int64
	MinDurationMS int64
}

// OptScore is the linear objective contribution of one candidate.
func OptScore(c OptCandidate, w OptWeights) float64 {
	return w.WR*c.R + w.WQ*c.Q + w.WN*c.N - w.WD*c.D
}

// OptWeights versions the objective. Tune only against a held-out annotated
// set, never to pass a single example.
type OptWeights struct {
	WR, WQ, WN, WD float64
}

// DefaultOptWeights is the canonical balanced objective.
func DefaultOptWeights() OptWeights { return OptWeights{WR: 1, WQ: 1, WN: 1, WD: 1} }

// ValidityReason explains a rejected candidate (validity, not taste).
type ValidityReason string

const (
	ValidOK             ValidityReason = ""
	InvalidBeforeAnchor ValidityReason = "start_before_speech_anchor"
	InvalidTooShort     ValidityReason = "below_min_readable_duration"
	InvalidWindow       ValidityReason = "invalid_window"
	InvalidOverBudget   ValidityReason = "overlap_budget_exceeded"
)

// CheckValidity verifies the objective constraints for one candidate:
// certified anchor respected, legibility floor met, sane window. Overlap is
// checked during selection against already-admitted items.
func CheckValidity(c OptCandidate) ValidityReason {
	if c.EndMS <= c.StartMS || c.StartMS < 0 {
		return InvalidWindow
	}
	if c.StartMS < c.AnchorMS {
		return InvalidBeforeAnchor
	}
	if c.MinDurationMS > 0 && c.EndMS-c.StartMS < c.MinDurationMS {
		return InvalidTooShort
	}
	return ValidOK
}

// SelectLocal admits candidates greedily by objective score (deterministic:
// score desc, then ID). Each admission must keep Overlap(t) <= maxOverlap
// and pass validity; losers are reported with their reason. This is the
// single-owner selection for one scene-window: no parallel per-kind caps
// elsewhere may contradict it.
func SelectLocal(cands []OptCandidate, w OptWeights, maxOverlap int) (admitted []OptCandidate, rejected map[string]ValidityReason) {
	if maxOverlap <= 0 {
		maxOverlap = 3
	}
	rejected = map[string]ValidityReason{}
	ordered := append([]OptCandidate(nil), cands...)
	sort.SliceStable(ordered, func(i, j int) bool {
		si, sj := OptScore(ordered[i], w), OptScore(ordered[j], w)
		if si != sj {
			return si > sj
		}
		return ordered[i].ID < ordered[j].ID
	})
	for _, c := range ordered {
		if reason := CheckValidity(c); reason != ValidOK {
			rejected[c.ID] = reason
			continue
		}
		overlap := 0
		for _, a := range admitted {
			if c.StartMS < a.EndMS && a.StartMS < c.EndMS {
				overlap++
			}
		}
		if overlap >= maxOverlap {
			rejected[c.ID] = InvalidOverBudget
			continue
		}
		admitted = append(admitted, c)
	}
	if admitted == nil {
		admitted = []OptCandidate{}
	}
	return admitted, rejected
}

// ── Global sequence planning ────────────────────────────────────

// SeqOption is one eligible visual for a scene. Eligibility is decided
// upstream (Visual Admission over certified recipes): variety never converts
// a bar-only datum into a pie.
type SeqOption struct {
	VisualID string
	Recipe   string
	Family   string
	Motion   string
	Dense    bool
	Quality  float64 // Q(v_i, s_i): compatibility with the scene
}

// TransitionCost penalizes repetitive or inappropriate transitions:
// same recipe 1.0, same motion 0.4, same family 0.3 (additive, capped).
func TransitionCost(prev, curr SeqOption) float64 {
	if prev.VisualID == "" {
		return 0
	}
	cost := 0.0
	if prev.Recipe != "" && prev.Recipe == curr.Recipe {
		cost += 1.0
	}
	if prev.Motion != "" && prev.Motion == curr.Motion {
		cost += 0.4
	}
	if prev.Family != "" && prev.Family == curr.Family {
		cost += 0.3
	}
	if cost > 1.0 {
		cost = 1.0
	}
	return cost
}

// PlanSequence chooses one option per scene maximizing
// sum Q - lambda sum C via exact dynamic programming. Deterministic:
// ties break by option order. Empty option lists fail closed (no plan is
// fabricated for a scene with nothing eligible).
func PlanSequence(scenes [][]SeqOption, lambda float64) ([]SeqOption, error) {
	if len(scenes) == 0 {
		return nil, errors.New("sequence planning requires scenes")
	}
	for i, opts := range scenes {
		if len(opts) == 0 {
			return nil, errors.New("scene has no eligible visual")
		}
		_ = i
	}
	n := len(scenes)
	// dp[i][j] = best value reaching option j of scene i; back[i][j] = prev index.
	dp := make([][]float64, n)
	back := make([][]int, n)
	for i, opts := range scenes {
		dp[i] = make([]float64, len(opts))
		back[i] = make([]int, len(opts))
		for j, o := range opts {
			if i == 0 {
				dp[i][j] = o.Quality
				back[i][j] = -1
				continue
			}
			best := -1e18
			bestK := 0
			for k, p := range scenes[i-1] {
				v := dp[i-1][k] + o.Quality - lambda*TransitionCost(p, o)
				if v > best {
					best = v
					bestK = k
				}
			}
			dp[i][j] = best
			back[i][j] = bestK
		}
	}
	last := 0
	for j := 1; j < len(scenes[n-1]); j++ {
		if dp[n-1][j] > dp[n-1][last] {
			last = j
		}
	}
	choice := make([]int, n)
	choice[n-1] = last
	for i := n - 1; i > 0; i-- {
		choice[i-1] = back[i][choice[i]]
	}
	out := make([]SeqOption, n)
	for i := range scenes {
		out[i] = scenes[i][choice[i]]
	}
	return out, nil
}

// ── Montage profiles ────────────────────────────────────────────

// MontageProfile changes objectives and constraints, never facts: the same
// script yields Documentary / News / Educational cuts without re-calling
// any extractor.
type MontageProfile struct {
	Name    string
	Weights OptWeights
	Lambda  float64
	// Boosts add per-family quality bias (e.g. News favors numbers).
	Boosts map[string]float64
}

// Canonical profiles (versioned by name; retune as a new named profile).
func DocumentaryProfile() MontageProfile {
	return MontageProfile{
		Name:    "documentary",
		Weights: DefaultOptWeights(),
		Lambda:  0.6,
		Boosts:  map[string]float64{"broll": 0.1, "map": 0.1, "timeline": 0.05},
	}
}

// NewsProfile favors titles, numbers and fast comparisons.
func NewsProfile() MontageProfile {
	return MontageProfile{
		Name:    "news",
		Weights: OptWeights{WR: 1.2, WQ: 1, WN: 0.8, WD: 1},
		Lambda:  0.3,
		Boosts:  map[string]float64{"number": 0.2, "comparison": 0.15, "title": 0.1},
	}
}

// EducationalProfile favors diagrams, timelines, bullets and recap.
func EducationalProfile() MontageProfile {
	return MontageProfile{
		Name:    "educational",
		Weights: OptWeights{WR: 1, WQ: 1.1, WN: 0.9, WD: 0.8},
		Lambda:  0.6,
		Boosts:  map[string]float64{"diagram": 0.2, "timeline": 0.2, "bullet": 0.1},
	}
}

// ApplyBoosts returns quality with the profile family bias added.
func (p MontageProfile) ApplyBoosts(o SeqOption) float64 {
	return o.Quality + p.Boosts[o.Family]
}

// DensityPerSecond converts a content-load score into load per second of
// speech: the same content spoken faster raises the per-second load the
// viewer must absorb. Non-positive durations fail closed (no load can be
// computed without a certified duration).
func DensityPerSecond(score, durationSecs float64) (float64, error) {
	if durationSecs <= 0 {
		return 0, errors.New("density per second requires a positive certified duration")
	}
	if score < 0 || score > 1 {
		return 0, errors.New("density score out of range")
	}
	return score / durationSecs, nil
}

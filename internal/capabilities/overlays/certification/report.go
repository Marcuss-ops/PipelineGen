// Package certification owns the ONE overlay certification verdict.
//
// The producers of overlay evidence stay where they are: PipelineGen owns the
// plan and the selection lineage, RenderingGen owns the execution receipt and
// the composition check, Chronon owns the GPU telemetry. This package does not
// re-run any of them. It joins their evidence into one document with one
// verdict per overlay and one verdict for the run, and it never promotes an
// unmeasured fact to PASS: every check is PASS, FAIL, SKIPPED or UNVERIFIABLE.
//
// The mode selects what the run promises:
//
//	fast     contracts, receipt identity, plan timing, explicit fallback and
//	         the artifact hash when it is present. Pixel visibility and the
//	         variety gate are SKIPPED by design.
//	normal   fast plus sampled visibility. Edge coverage that the sample did
//	         not reach is UNVERIFIABLE, not PASS.
//	certify  normal plus interval-by-interval entry/exit coverage and the
//	         variety gate. Missing evidence is UNVERIFIABLE or FAIL, never
//	         silently green.
//
// The canonical schema is pipelinegen.overlay-certification.v1. It is a
// projection: consumers must not infer a PASS for a check the document marks
// SKIPPED or UNVERIFIABLE.
package certification

import (
	"fmt"
	"sort"
	"strings"
)

const (
	// SchemaVersion is the overlay certification report contract.
	SchemaVersion = "pipelinegen.overlay-certification.v1"
	// ExecutionSchemaVersion is the neutral evidence document the worker side
	// may emit. It is deliberately not a RenderingGen wire contract: the
	// worker can project its receipt into this shape without importing this
	// package or exposing a second source of truth.
	ExecutionSchemaVersion = "pipelinegen.overlay-execution-evidence.v1"
)

// Mode is the amount of proof the certification run promises.
type Mode string

const (
	ModeFast    Mode = "fast"
	ModeNormal  Mode = "normal"
	ModeCertify Mode = "certify"
)

// ParseMode validates the operator spelling. An empty value is fast: a caller
// that did not ask for proof must not receive the certify verdict by accident.
func ParseMode(value string) (Mode, error) {
	switch Mode(strings.ToLower(strings.TrimSpace(value))) {
	case "", ModeFast:
		return ModeFast, nil
	case ModeNormal:
		return ModeNormal, nil
	case ModeCertify:
		return ModeCertify, nil
	default:
		return "", fmt.Errorf("unknown overlay certification mode %q (allowed: fast, normal, certify)", value)
	}
}

// Verdict is the closed result vocabulary. SKIPPED means the mode does not
// promise the check; UNVERIFIABLE means the mode promises it but the evidence
// is missing or incomplete. Neither is a synonym for PASS.
type Verdict string

const (
	VerdictPass         Verdict = "PASS"
	VerdictFail         Verdict = "FAIL"
	VerdictSkipped      Verdict = "SKIPPED"
	VerdictUnverifiable Verdict = "UNVERIFIABLE"
)

// Check is one measured fact in the report. Name is stable so an operator can
// diff two runs; Detail carries the numbers or the reason, never a secret.
type Check struct {
	Name    string  `json:"name"`
	Verdict Verdict `json:"verdict"`
	Detail  string  `json:"detail,omitempty"`
}

// ArtifactEvidence binds the report to bytes. Kind is overlay_artifact for a
// per-plan render and final_video for the assembled master.
type ArtifactEvidence struct {
	Kind       string `json:"kind,omitempty"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	FrameCount int    `json:"frame_count,omitempty"`
	FPSNum     int    `json:"fps_num,omitempty"`
	FPSDen     int    `json:"fps_den,omitempty"`
}

// FrameInterval is a closed visibility interval. Both ends are inclusive, the
// same convention the plan uses for StartFrame/EndFrame.
type FrameInterval struct {
	StartFrame int `json:"start_frame"`
	EndFrame   int `json:"end_frame"`
}

// OverlayExecution is the worker-side evidence for one overlay item. Pointer
// booleans distinguish "not reported" from "reported false": the former is
// UNVERIFIABLE, the latter is FAIL.
type OverlayExecution struct {
	OverlayID           string          `json:"overlay_id"`
	Rendered            *bool           `json:"rendered,omitempty"`
	ArtifactSHA256      string          `json:"artifact_sha256,omitempty"`
	ExecutedMotion      string          `json:"executed_motion,omitempty"`
	FallbackUsed        *bool           `json:"fallback_used,omitempty"`
	PixelVerified       *bool           `json:"pixel_verified,omitempty"`
	VisibleIntervals    []FrameInterval `json:"visible_intervals,omitempty"`
	SampledFrames       []int           `json:"sampled_frames,omitempty"`
	CompositionVerified *bool           `json:"composition_verified,omitempty"`
	Reason              string          `json:"reason,omitempty"`
}

// ExecutionEvidence is the neutral, worker-owned side of the join.
type ExecutionEvidence struct {
	SchemaVersion  string             `json:"schema_version,omitempty"`
	RunID          string             `json:"run_id,omitempty"`
	WorkerID       string             `json:"worker_id,omitempty"`
	ArtifactSHA256 string             `json:"artifact_sha256,omitempty"`
	FrameCount     int                `json:"frame_count,omitempty"`
	FPSNum         int                `json:"fps_num,omitempty"`
	FPSDen         int                `json:"fps_den,omitempty"`
	Overlays       []OverlayExecution `json:"overlays"`
}

// VarietyPolicy is the sampler-quality gate. It is applied only in certify:
// fast and normal certify the contract, not the editorial distribution.
type VarietyPolicy struct {
	Enabled     bool
	MinItems    int
	MinDistinct int
	MaxShare    float64
}

// Input is one certification request. Plan is required; everything else is
// optional evidence and absence is classified, never guessed.
type Input struct {
	Plan            *Plan
	Execution       *ExecutionEvidence
	Artifact        *ArtifactEvidence
	Mode            Mode
	ToleranceFrames int
	Variety         VarietyPolicy
}

// Plan is the minimal plan projection this package needs. It is structurally
// compatible with capmodel.OverlayPlan and with the plan fixtures, so callers
// do not have to translate before certifying.
type Plan struct {
	SchemaVersion string     `json:"schema_version,omitempty"`
	PlanID        string     `json:"plan_id,omitempty"`
	VideoID       string     `json:"video_id,omitempty"`
	Fingerprint   string     `json:"fingerprint,omitempty"`
	Items         []PlanItem `json:"items"`
}

// PlanItem is the subset of an overlay item the certification reads. Params is
// the open map that already carries motion_origin (ANIMRAND-16).
type PlanItem struct {
	ID              string         `json:"id"`
	SceneID         string         `json:"scene_id,omitempty"`
	Kind            string         `json:"kind,omitempty"`
	TemplateID      string         `json:"template_id,omitempty"`
	PresetID        string         `json:"preset_id,omitempty"`
	ImagePresetID   string         `json:"image_preset_id,omitempty"`
	MotionID        string         `json:"motion_id,omitempty"`
	GroupMotionID   string         `json:"group_motion_id,omitempty"`
	CaptionMotionID string         `json:"caption_motion_id,omitempty"`
	StartMs         int64          `json:"start_ms"`
	EndMs           int64          `json:"end_ms"`
	StartUS         int64          `json:"start_us,omitempty"`
	DurationUS      int64          `json:"duration_us,omitempty"`
	Params          map[string]any `json:"params,omitempty"`
}

// Receipt is the per-overlay answer: what was asked, what was compiled, what
// the worker executed, which frames were visible, and whether a fallback was
// declared. Every dimension keeps its own verdict; the rollup never hides a
// failed dimension inside a green one.
type Receipt struct {
	OverlayID       string   `json:"overlay_id"`
	SceneID         string   `json:"scene_id,omitempty"`
	Kind            string   `json:"kind,omitempty"`
	StartMS         int64    `json:"start_ms"`
	EndMS           int64    `json:"end_ms"`
	StartFrame      int      `json:"start_frame"`
	EndFrame        int      `json:"end_frame"`
	RequestedMotion string   `json:"requested_motion,omitempty"`
	CompiledMotion  string   `json:"compiled_motion,omitempty"`
	ExecutedMotion  string   `json:"executed_motion,omitempty"`
	SelectionOrigin string   `json:"selection_origin,omitempty"`
	FallbackUsed    bool     `json:"fallback_used"`
	ArtifactSHA256  string   `json:"artifact_sha256,omitempty"`
	Verdict         Verdict  `json:"verdict"`
	Checks          []Check  `json:"checks"`
	Reasons         []string `json:"reasons,omitempty"`
}

// Counts answers "how many" without requiring a consumer to walk the receipts.
type Counts struct {
	Planned      int `json:"planned"`
	Rendered     int `json:"rendered"`
	Verified     int `json:"verified"`
	Failed       int `json:"failed"`
	Unverifiable int `json:"unverifiable"`
	Skipped      int `json:"skipped"`
}

// MotionCount is one row of the distribution, sorted by motion id.
type MotionCount struct {
	Motion string `json:"motion"`
	Count  int    `json:"count"`
}

// VarietyReport is the measured distribution. Verdict is the gate result; the
// metrics stay present even when the gate is SKIPPED so a later run can compare
// numbers instead of impressions.
type VarietyReport struct {
	AnimatedItems     int           `json:"animated_items"`
	DistinctMotions   int           `json:"distinct_motions"`
	ByMotion          []MotionCount `json:"by_motion"`
	MaxMotion         string        `json:"max_motion,omitempty"`
	MaxShare          float64       `json:"max_share"`
	EntropyBits       float64       `json:"entropy_bits"`
	NormalizedEntropy float64       `json:"normalized_entropy"`
	Verdict           Verdict       `json:"verdict"`
	Detail            string        `json:"detail,omitempty"`
}

// Report is the canonical document. It is deterministic: no timestamps, no
// local paths, no map iteration order.
type Report struct {
	SchemaVersion string            `json:"schema_version"`
	Mode          Mode              `json:"mode"`
	Verdict       Verdict           `json:"verdict"`
	PlanID        string            `json:"plan_id,omitempty"`
	PlanSHA256    string            `json:"plan_sha256,omitempty"`
	RunID         string            `json:"run_id,omitempty"`
	Artifact      *ArtifactEvidence `json:"artifact,omitempty"`
	Counts        Counts            `json:"counts"`
	Checks        []Check           `json:"checks"`
	Variety       *VarietyReport    `json:"variety,omitempty"`
	Overlays      []Receipt         `json:"overlays"`
	Limitations   []string          `json:"limitations,omitempty"`
}

// Aggregate folds checks with FAIL > UNVERIFIABLE > PASS. SKIPPED never
// upgrades or downgrades a verdict; it only documents that the tier did not
// promise the check.
func Aggregate(checks []Check) Verdict {
	verdict := VerdictPass
	for _, check := range checks {
		switch check.Verdict {
		case VerdictFail:
			return VerdictFail
		case VerdictUnverifiable:
			verdict = VerdictUnverifiable
		}
	}
	return verdict
}

func makeCheck(name string, verdict Verdict, detail string) Check {
	return Check{Name: name, Verdict: verdict, Detail: detail}
}

func (a *ArtifactEvidence) normalize() *ArtifactEvidence {
	if a == nil {
		return nil
	}
	out := *a
	out.Kind = strings.TrimSpace(out.Kind)
	if out.Kind == "" {
		out.Kind = "overlay_artifact"
	}
	out.SHA256 = strings.ToLower(strings.TrimSpace(out.SHA256))
	if out.FPSNum <= 0 || out.FPSDen <= 0 {
		out.FPSNum, out.FPSDen = 0, 0
	}
	return &out
}

func (a *ArtifactEvidence) frameRate(fallbackNum, fallbackDen int) (int, int) {
	if a != nil && a.FPSNum > 0 && a.FPSDen > 0 {
		return a.FPSNum, a.FPSDen
	}
	if fallbackNum > 0 && fallbackDen > 0 {
		return fallbackNum, fallbackDen
	}
	return 24, 1
}

func (e *ExecutionEvidence) frameRate(fallbackNum, fallbackDen int) (int, int) {
	if e != nil && e.FPSNum > 0 && e.FPSDen > 0 {
		return e.FPSNum, e.FPSDen
	}
	return fallbackNum, fallbackDen
}

func motionOf(item PlanItem) string {
	for _, value := range []string{item.MotionID, item.GroupMotionID, item.CaptionMotionID, item.ImagePresetID, item.PresetID} {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// requestedMotionOf reads the producer stamp when it exists. The overlay plan
// wire carries the compiled motion; params.requested_motion is the optional
// intent stamp, so its absence is a measurement gap, not a match.
func requestedMotionOf(item PlanItem, compiled string) (string, bool) {
	if value, ok := item.Params["requested_motion"].(string); ok {
		if value = strings.TrimSpace(value); value != "" {
			return value, true
		}
	}
	return compiled, false
}

func originOf(item PlanItem) string {
	for _, key := range []string{"motion_origin", "caption_motion_origin"} {
		if value, ok := item.Params[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.ToLower(strings.TrimSpace(value))
		}
	}
	return ""
}

func sortedMotions(counts map[string]int) []MotionCount {
	out := make([]MotionCount, 0, len(counts))
	for motion, count := range counts {
		out = append(out, MotionCount{Motion: motion, Count: count})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Motion < out[j].Motion })
	return out
}

package certification

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const defaultToleranceFrames = 2

// Build joins the plan and the optional evidence into the canonical report.
// A missing plan is a FAIL, not an error: the caller still gets a document
// that says why the run cannot be certified.
func Build(input Input) (*Report, error) {
	mode, err := ParseMode(string(input.Mode))
	if err != nil {
		return nil, err
	}
	tolerance := input.ToleranceFrames
	if tolerance <= 0 {
		tolerance = defaultToleranceFrames
	}
	artifact := input.Artifact.normalize()
	fpsNum, fpsDen := artifact.frameRate(24, 1)
	if input.Execution != nil {
		fpsNum, fpsDen = input.Execution.frameRate(fpsNum, fpsDen)
	}

	report := &Report{
		SchemaVersion: SchemaVersion,
		Mode:          mode,
		Verdict:       VerdictPass,
		Artifact:      artifact,
		Overlays:      make([]Receipt, 0),
	}
	if input.Plan == nil {
		report.Checks = append(report.Checks, makeCheck("plan.present", VerdictFail, "no overlay plan supplied"))
		report.Verdict = VerdictFail
		report.Limitations = append(report.Limitations, "no overlay plan: nothing can be certified")
		return report, nil
	}

	plan := input.Plan
	report.PlanID = strings.TrimSpace(plan.PlanID)
	report.PlanSHA256 = strings.TrimSpace(plan.Fingerprint)
	if report.PlanSHA256 == "" {
		if encoded, marshalErr := json.Marshal(plan); marshalErr == nil {
			report.PlanSHA256 = digest.SHA256Bytes(encoded)
		}
	}
	if input.Execution != nil {
		report.RunID = strings.TrimSpace(input.Execution.RunID)
	}

	report.Checks = append(report.Checks, planChecks(plan)...)
	index := newExecutionIndex(plan, input.Execution, mode)
	report.Checks = append(report.Checks, index.checks...)
	report.Checks = append(report.Checks, artifactBindingCheck(artifact, input.Execution, mode))

	for _, item := range plan.Items {
		exec := index.byID[strings.TrimSpace(item.ID)]
		report.Overlays = append(report.Overlays, buildReceipt(item, exec, artifact, mode, tolerance, fpsNum, fpsDen))
	}

	var varietyCheck Check
	report.Variety, varietyCheck = buildVariety(plan.Items, input.Variety, mode)
	report.Checks = append(report.Checks, varietyCheck)
	report.Checks = append(report.Checks, receiptsRollup(report.Overlays))

	for _, receipt := range report.Overlays {
		report.Counts.Planned++
		switch receipt.Verdict {
		case VerdictFail:
			report.Counts.Failed++
		case VerdictUnverifiable:
			report.Counts.Unverifiable++
		case VerdictSkipped:
			report.Counts.Skipped++
		}
		if checkVerdict(receipt.Checks, "overlay.rendered") == VerdictPass {
			report.Counts.Rendered++
		}
		if checkVerdict(receipt.Checks, "overlay.visibility") == VerdictPass {
			report.Counts.Verified++
		}
	}

	report.Verdict = Aggregate(report.Checks)
	report.Limitations = limitations(mode, input.Execution, artifact)
	return report, nil
}

func planChecks(plan *Plan) []Check {
	checks := make([]Check, 0, 3)
	if len(plan.Items) == 0 {
		checks = append(checks, makeCheck("plan.items", VerdictFail, "the plan has no overlay items"))
	} else {
		checks = append(checks, makeCheck("plan.items", VerdictPass, fmt.Sprintf("%d items", len(plan.Items))))
	}

	seen := make(map[string]struct{}, len(plan.Items))
	empty, duplicate := 0, 0
	for _, item := range plan.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			empty++
			continue
		}
		if _, ok := seen[id]; ok {
			duplicate++
			continue
		}
		seen[id] = struct{}{}
	}
	switch {
	case empty > 0 || duplicate > 0:
		checks = append(checks, makeCheck("plan.unique_overlay_ids", VerdictFail,
			fmt.Sprintf("%d empty ids, %d duplicate ids", empty, duplicate)))
	default:
		checks = append(checks, makeCheck("plan.unique_overlay_ids", VerdictPass, fmt.Sprintf("%d unique ids", len(seen))))
	}

	invalidTiming := 0
	for _, item := range plan.Items {
		if item.StartMs < 0 || item.DurationUS < 0 || (item.DurationUS == 0 && item.EndMs <= item.StartMs) || (item.DurationUS > 0 && item.StartUS < 0) {
			invalidTiming++
		}
	}
	if invalidTiming > 0 {
		checks = append(checks, makeCheck("plan.timing", VerdictFail, fmt.Sprintf("%d items have invalid start/end timing", invalidTiming)))
	} else {
		checks = append(checks, makeCheck("plan.timing", VerdictPass, "every item has a positive window"))
	}
	return checks
}

type executionIndex struct {
	byID   map[string]*OverlayExecution
	checks []Check
}

func newExecutionIndex(plan *Plan, evidence *ExecutionEvidence, mode Mode) executionIndex {
	index := executionIndex{byID: make(map[string]*OverlayExecution, len(plan.Items))}
	if evidence == nil {
		detail := "no execution evidence supplied"
		if mode == ModeFast {
			index.checks = append(index.checks, makeCheck("execution.coverage", VerdictSkipped, detail))
		} else {
			index.checks = append(index.checks, makeCheck("execution.coverage", VerdictUnverifiable, detail))
		}
		return index
	}
	if version := strings.TrimSpace(evidence.SchemaVersion); version != "" && version != ExecutionSchemaVersion {
		index.checks = append(index.checks, makeCheck("execution.schema", VerdictFail,
			fmt.Sprintf("evidence schema %q, want %q", version, ExecutionSchemaVersion)))
	}
	if evidence.FrameCount < 0 {
		index.checks = append(index.checks, makeCheck("execution.frame_count", VerdictFail, "negative frame count"))
	}
	if hash := strings.ToLower(strings.TrimSpace(evidence.ArtifactSHA256)); hash != "" && !validSHA256(hash) {
		index.checks = append(index.checks, makeCheck("execution.artifact_sha256", VerdictFail, "evidence artifact hash is not 64 hex characters"))
	}

	unknown, duplicate := 0, 0
	for i := range evidence.Overlays {
		entry := &evidence.Overlays[i]
		id := strings.TrimSpace(entry.OverlayID)
		if id == "" {
			unknown++
			continue
		}
		if _, exists := index.byID[id]; exists {
			duplicate++
			continue
		}
		index.byID[id] = entry
	}
	planned := make(map[string]struct{}, len(plan.Items))
	for _, item := range plan.Items {
		planned[strings.TrimSpace(item.ID)] = struct{}{}
	}
	unplanned := 0
	for id := range index.byID {
		if _, ok := planned[id]; !ok {
			unplanned++
		}
	}
	if unknown > 0 || duplicate > 0 || unplanned > 0 {
		index.checks = append(index.checks, makeCheck("execution.overlay_ids", VerdictFail,
			fmt.Sprintf("%d entries without id, %d duplicate ids, %d ids not in the plan", unknown, duplicate, unplanned)))
	}

	missing := make([]string, 0)
	for _, item := range plan.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		if _, ok := index.byID[id]; !ok {
			missing = append(missing, id)
		}
	}
	switch {
	case len(missing) == 0:
		index.checks = append(index.checks, makeCheck("execution.coverage", VerdictPass,
			fmt.Sprintf("%d/%d planned overlays have evidence", len(plan.Items), len(plan.Items))))
	case mode == ModeFast:
		index.checks = append(index.checks, makeCheck("execution.coverage", VerdictSkipped,
			fmt.Sprintf("%d planned overlays have no evidence: %s", len(missing), sampleIDs(missing))))
	default:
		index.checks = append(index.checks, makeCheck("execution.coverage", VerdictUnverifiable,
			fmt.Sprintf("%d planned overlays have no evidence: %s", len(missing), sampleIDs(missing))))
	}
	return index
}

func artifactBindingCheck(artifact *ArtifactEvidence, evidence *ExecutionEvidence, mode Mode) Check {
	if artifact == nil || strings.TrimSpace(artifact.SHA256) == "" {
		if mode == ModeFast {
			return makeCheck("artifact.binding", VerdictSkipped, "no artifact hash supplied")
		}
		return makeCheck("artifact.binding", VerdictUnverifiable, "no artifact hash supplied")
	}
	if !validSHA256(artifact.SHA256) {
		return makeCheck("artifact.binding", VerdictFail, "artifact hash is not 64 hex characters")
	}
	if evidence == nil || strings.TrimSpace(evidence.ArtifactSHA256) == "" {
		if mode == ModeFast {
			return makeCheck("artifact.binding", VerdictSkipped, "execution evidence does not carry an artifact hash")
		}
		return makeCheck("artifact.binding", VerdictUnverifiable, "execution evidence does not carry an artifact hash")
	}
	execHash := strings.ToLower(strings.TrimSpace(evidence.ArtifactSHA256))
	if execHash != artifact.SHA256 {
		return makeCheck("artifact.binding", VerdictFail,
			fmt.Sprintf("artifact %s… != evidence %s…", shortHash(artifact.SHA256), shortHash(execHash)))
	}
	if artifact.FrameCount > 0 && evidence.FrameCount > 0 && artifact.FrameCount != evidence.FrameCount {
		return makeCheck("artifact.binding", VerdictFail,
			fmt.Sprintf("artifact frame_count=%d, evidence frame_count=%d", artifact.FrameCount, evidence.FrameCount))
	}
	return makeCheck("artifact.binding", VerdictPass, "receipt and artifact share the same sha256")
}

func buildReceipt(item PlanItem, exec *OverlayExecution, artifact *ArtifactEvidence, mode Mode, tolerance, fpsNum, fpsDen int) Receipt {
	startFrame, endFrame, startMS, endMS := windowOf(item, fpsNum, fpsDen)
	motion := motionOf(item)
	requested, requestedRecorded := requestedMotionOf(item, motion)
	origin := originOf(item)
	receipt := Receipt{
		OverlayID:       strings.TrimSpace(item.ID),
		SceneID:         strings.TrimSpace(item.SceneID),
		Kind:            strings.TrimSpace(item.Kind),
		StartMS:         startMS,
		EndMS:           endMS,
		StartFrame:      startFrame,
		EndFrame:        endFrame,
		RequestedMotion: requested,
		CompiledMotion:  motion,
		SelectionOrigin: origin,
	}
	if exec != nil {
		receipt.ExecutedMotion = strings.TrimSpace(exec.ExecutedMotion)
		receipt.ArtifactSHA256 = strings.ToLower(strings.TrimSpace(exec.ArtifactSHA256))
		if exec.FallbackUsed != nil {
			receipt.FallbackUsed = *exec.FallbackUsed
		}
		if receipt.ArtifactSHA256 == "" && artifact != nil {
			receipt.ArtifactSHA256 = artifact.SHA256
		}
	}

	receipt.Checks = []Check{
		renderedCheck(mode, exec),
		visibilityCheck(mode, exec),
		entryExitCheck(mode, exec, startFrame, endFrame, tolerance),
		requestLineageCheck(requested, motion, origin, requestedRecorded),
		motionLineageCheck(mode, exec, motion),
		fallbackCheck(mode, exec, motion, origin),
		originCheck(mode, origin, motion),
		compositionCheck(mode, exec),
		receiptArtifactCheck(mode, exec, artifact),
	}
	receipt.Verdict = Aggregate(receipt.Checks)
	for _, check := range receipt.Checks {
		if check.Verdict == VerdictFail || check.Verdict == VerdictUnverifiable {
			receipt.Reasons = append(receipt.Reasons, check.Name+": "+check.Detail)
		}
	}
	return receipt
}

func renderedCheck(mode Mode, exec *OverlayExecution) Check {
	if exec == nil {
		if mode == ModeFast {
			return makeCheck("overlay.rendered", VerdictSkipped, "no execution evidence supplied")
		}
		return makeCheck("overlay.rendered", VerdictUnverifiable, "no execution evidence supplied")
	}
	if exec.Rendered == nil {
		return makeCheck("overlay.rendered", VerdictUnverifiable, "evidence does not report rendered")
	}
	if !*exec.Rendered {
		reason := strings.TrimSpace(exec.Reason)
		if reason == "" {
			reason = "worker reported rendered=false"
		}
		return makeCheck("overlay.rendered", VerdictFail, reason)
	}
	hash := strings.ToLower(strings.TrimSpace(exec.ArtifactSHA256))
	if hash == "" {
		return makeCheck("overlay.rendered", VerdictUnverifiable, "rendered=true without an artifact hash")
	}
	if !validSHA256(hash) {
		return makeCheck("overlay.rendered", VerdictFail, "rendered=true with a malformed artifact hash")
	}
	return makeCheck("overlay.rendered", VerdictPass, "artifact "+shortHash(hash))
}

func visibilityCheck(mode Mode, exec *OverlayExecution) Check {
	if mode == ModeFast {
		return makeCheck("overlay.visibility", VerdictSkipped, "pixel visibility is not promised by mode fast")
	}
	if exec == nil || exec.PixelVerified == nil {
		return makeCheck("overlay.visibility", VerdictUnverifiable, "evidence does not report pixel_verified")
	}
	if !*exec.PixelVerified {
		return makeCheck("overlay.visibility", VerdictFail, "pixel verification reported false")
	}
	if len(exec.VisibleIntervals) == 0 && len(exec.SampledFrames) == 0 {
		return makeCheck("overlay.visibility", VerdictUnverifiable, "pixel_verified=true without intervals or samples")
	}
	return makeCheck("overlay.visibility", VerdictPass,
		fmt.Sprintf("%d intervals, %d sampled frames", len(exec.VisibleIntervals), len(exec.SampledFrames)))
}

func entryExitCheck(mode Mode, exec *OverlayExecution, startFrame, endFrame, tolerance int) Check {
	if mode == ModeFast {
		return makeCheck("overlay.entry_exit", VerdictSkipped, "entry/exit coverage is not promised by mode fast")
	}
	if exec == nil {
		return makeCheck("overlay.entry_exit", VerdictUnverifiable, "no execution evidence supplied")
	}
	windowStart := startFrame - tolerance
	windowEnd := endFrame + tolerance
	if windowStart < 0 {
		windowStart = 0
	}

	if len(exec.VisibleIntervals) == 0 {
		if len(exec.SampledFrames) == 0 {
			return makeCheck("overlay.entry_exit", VerdictUnverifiable, "no visible intervals and no sampled frames")
		}
		inside := 0
		for _, frame := range exec.SampledFrames {
			if frame < windowStart || frame > windowEnd {
				return makeCheck("overlay.entry_exit", VerdictFail,
					fmt.Sprintf("sampled frame %d is outside the plan window %d..%d", frame, startFrame, endFrame))
			}
			inside++
		}
		if mode == ModeCertify && inside < 2 {
			return makeCheck("overlay.entry_exit", VerdictUnverifiable, "certify needs interval evidence or at least two samples")
		}
		return makeCheck("overlay.entry_exit", VerdictPass, fmt.Sprintf("%d sampled frames inside the window", inside))
	}

	first, last := exec.VisibleIntervals[0].StartFrame, exec.VisibleIntervals[0].EndFrame
	for _, interval := range exec.VisibleIntervals {
		if interval.EndFrame < interval.StartFrame {
			return makeCheck("overlay.entry_exit", VerdictFail,
				fmt.Sprintf("invalid visible interval %d..%d", interval.StartFrame, interval.EndFrame))
		}
		if interval.StartFrame < windowStart || interval.EndFrame > windowEnd {
			return makeCheck("overlay.entry_exit", VerdictFail,
				fmt.Sprintf("visibility %d..%d leaks outside the plan window %d..%d", interval.StartFrame, interval.EndFrame, startFrame, endFrame))
		}
		if interval.StartFrame < first {
			first = interval.StartFrame
		}
		if interval.EndFrame > last {
			last = interval.EndFrame
		}
	}
	if first > startFrame+tolerance {
		if mode == ModeCertify {
			return makeCheck("overlay.entry_exit", VerdictFail,
				fmt.Sprintf("entry at frame %d is later than %d", first, startFrame+tolerance))
		}
		return makeCheck("overlay.entry_exit", VerdictUnverifiable,
			fmt.Sprintf("entry at frame %d is later than the sampled window", first))
	}
	if last < endFrame-tolerance {
		if mode == ModeCertify {
			return makeCheck("overlay.entry_exit", VerdictFail,
				fmt.Sprintf("exit at frame %d is earlier than %d", last, endFrame-tolerance))
		}
		return makeCheck("overlay.entry_exit", VerdictUnverifiable,
			fmt.Sprintf("exit at frame %d is earlier than the sampled window", last))
	}
	return makeCheck("overlay.entry_exit", VerdictPass,
		fmt.Sprintf("visible %d..%d within %d..%d", first, last, startFrame, endFrame))
}

// requestLineageCheck answers the first half of the lineage: did compilation
// keep the motion the intent asked for? Plans written before the producer
// stamps params.requested_motion cannot answer, so they are SKIPPED rather than
// credited with a match nobody measured.
func requestLineageCheck(requested, compiled, origin string, recorded bool) Check {
	if compiled == "" {
		return makeCheck("overlay.motion_request", VerdictSkipped, "the item declares no motion or preset")
	}
	if !recorded {
		return makeCheck("overlay.motion_request", VerdictSkipped, "the plan records only the compiled motion")
	}
	if strings.EqualFold(requested, compiled) {
		return makeCheck("overlay.motion_request", VerdictPass, "requested and compiled motion are identical")
	}
	if origin == "fallback" {
		return makeCheck("overlay.motion_request", VerdictPass,
			fmt.Sprintf("compilation fallback is explicit: requested=%s compiled=%s", requested, compiled))
	}
	return makeCheck("overlay.motion_request", VerdictFail,
		fmt.Sprintf("requested motion %s but the plan compiled %s with no declared fallback", requested, compiled))
}

func motionLineageCheck(mode Mode, exec *OverlayExecution, motion string) Check {
	if motion == "" {
		return makeCheck("overlay.motion_lineage", VerdictSkipped, "the item declares no motion or preset")
	}
	if exec == nil || strings.TrimSpace(exec.ExecutedMotion) == "" {
		if mode == ModeFast {
			return makeCheck("overlay.motion_lineage", VerdictSkipped, "execution evidence does not report executed_motion")
		}
		return makeCheck("overlay.motion_lineage", VerdictUnverifiable, "execution evidence does not report executed_motion")
	}
	executed := strings.TrimSpace(exec.ExecutedMotion)
	if strings.EqualFold(executed, motion) {
		return makeCheck("overlay.motion_lineage", VerdictPass, "compiled and executed motion are identical")
	}
	if exec.FallbackUsed != nil && *exec.FallbackUsed {
		return makeCheck("overlay.motion_lineage", VerdictPass,
			fmt.Sprintf("fallback is explicit: compiled=%s executed=%s", motion, executed))
	}
	return makeCheck("overlay.motion_lineage", VerdictFail,
		fmt.Sprintf("compiled motion %s but worker executed %s with no declared fallback", motion, executed))
}

func fallbackCheck(mode Mode, exec *OverlayExecution, motion, origin string) Check {
	if motion == "" {
		return makeCheck("overlay.fallback", VerdictSkipped, "the item declares no motion or preset")
	}
	if exec == nil {
		if mode == ModeFast {
			return makeCheck("overlay.fallback", VerdictSkipped, "no execution evidence supplied")
		}
		return makeCheck("overlay.fallback", VerdictUnverifiable, "no execution evidence supplied")
	}
	executed := strings.TrimSpace(exec.ExecutedMotion)
	declared := exec.FallbackUsed != nil && *exec.FallbackUsed
	mismatch := executed != "" && !strings.EqualFold(executed, motion)
	switch {
	case mismatch && (declared || origin == "fallback"):
		return makeCheck("overlay.fallback", VerdictPass,
			fmt.Sprintf("declared fallback: compiled=%s executed=%s", motion, executed))
	case mismatch:
		return makeCheck("overlay.fallback", VerdictFail,
			fmt.Sprintf("silent substitution: compiled=%s executed=%s origin=%s", motion, executed, valueOr(origin, "unknown")))
	case declared && executed == "":
		if mode == ModeFast {
			return makeCheck("overlay.fallback", VerdictSkipped, "fallback declared but executed_motion is missing")
		}
		return makeCheck("overlay.fallback", VerdictUnverifiable, "fallback declared but executed_motion is missing")
	case declared:
		return makeCheck("overlay.fallback", VerdictFail, "fallback_used=true but the executed motion matches the compiled one")
	case executed == "":
		if mode == ModeFast {
			return makeCheck("overlay.fallback", VerdictSkipped, "execution evidence does not report executed_motion")
		}
		return makeCheck("overlay.fallback", VerdictUnverifiable, "execution evidence does not report executed_motion")
	default:
		return makeCheck("overlay.fallback", VerdictPass, "no fallback used")
	}
}

func originCheck(mode Mode, origin, motion string) Check {
	if motion == "" {
		return makeCheck("overlay.selection_origin", VerdictSkipped, "the item declares no motion or preset")
	}
	switch origin {
	case "explicit", "auto", "group", "fallback":
		return makeCheck("overlay.selection_origin", VerdictPass, origin)
	case "":
		if mode == ModeCertify {
			return makeCheck("overlay.selection_origin", VerdictFail, "certify requires params.motion_origin on every animated item")
		}
		return makeCheck("overlay.selection_origin", VerdictSkipped, "the plan does not record params.motion_origin")
	default:
		return makeCheck("overlay.selection_origin", VerdictFail, fmt.Sprintf("unknown motion_origin %q", origin))
	}
}

func compositionCheck(mode Mode, exec *OverlayExecution) Check {
	if exec == nil {
		if mode == ModeFast {
			return makeCheck("overlay.composition", VerdictSkipped, "no execution evidence supplied")
		}
		return makeCheck("overlay.composition", VerdictUnverifiable, "no execution evidence supplied")
	}
	if exec.CompositionVerified == nil {
		return makeCheck("overlay.composition", VerdictUnverifiable, "evidence does not report composition_verified")
	}
	if !*exec.CompositionVerified {
		return makeCheck("overlay.composition", VerdictFail, "the worker reported a composition divergence")
	}
	return makeCheck("overlay.composition", VerdictPass, "composition prediction agreed with the engine")
}

func receiptArtifactCheck(mode Mode, exec *OverlayExecution, artifact *ArtifactEvidence) Check {
	if artifact == nil || strings.TrimSpace(artifact.SHA256) == "" {
		if mode == ModeFast {
			return makeCheck("overlay.artifact", VerdictSkipped, "no artifact hash supplied")
		}
		return makeCheck("overlay.artifact", VerdictUnverifiable, "no artifact hash supplied")
	}
	if exec == nil || strings.TrimSpace(exec.ArtifactSHA256) == "" {
		if mode == ModeFast {
			return makeCheck("overlay.artifact", VerdictSkipped, "evidence does not carry a per-overlay artifact hash")
		}
		return makeCheck("overlay.artifact", VerdictUnverifiable, "evidence does not carry a per-overlay artifact hash")
	}
	execHash := strings.ToLower(strings.TrimSpace(exec.ArtifactSHA256))
	if !validSHA256(execHash) {
		return makeCheck("overlay.artifact", VerdictFail, "per-overlay artifact hash is not 64 hex characters")
	}
	if artifact.Kind == "final_video" {
		// A per-overlay render cannot share the hash of the assembled master.
		// The run-level binding is artifact.binding; here we only require that
		// the worker recorded which bytes it rendered.
		return makeCheck("overlay.artifact", VerdictPass, "per-overlay render hash recorded; master binding is run-level")
	}
	if execHash != artifact.SHA256 {
		return makeCheck("overlay.artifact", VerdictFail, "per-overlay artifact hash does not match the run artifact")
	}
	return makeCheck("overlay.artifact", VerdictPass, "per-overlay hash matches the run artifact")
}

func receiptsRollup(receipts []Receipt) Check {
	checks := make([]Check, 0, len(receipts))
	for _, receipt := range receipts {
		checks = append(checks, Check{Name: receipt.OverlayID, Verdict: receipt.Verdict})
	}
	verdict := Aggregate(checks)
	detail := fmt.Sprintf("%d receipts", len(receipts))
	if verdict == VerdictFail {
		failed := 0
		for _, receipt := range receipts {
			if receipt.Verdict == VerdictFail {
				failed++
			}
		}
		detail = fmt.Sprintf("%d/%d overlays failed", failed, len(receipts))
	}
	return makeCheck("overlays.verdict", verdict, detail)
}

func buildVariety(items []PlanItem, policy VarietyPolicy, mode Mode) (*VarietyReport, Check) {
	if !policy.Enabled {
		policy.Enabled = true
	}
	if policy.MinItems <= 0 {
		policy.MinItems = 4
	}
	if policy.MinDistinct <= 0 {
		policy.MinDistinct = 2
	}
	if policy.MaxShare <= 0 || policy.MaxShare > 1 {
		policy.MaxShare = 0.75
	}

	counts := make(map[string]int)
	total := 0
	for _, item := range items {
		if motion := motionOf(item); motion != "" {
			counts[motion]++
			total++
		}
	}
	report := &VarietyReport{
		AnimatedItems:   total,
		DistinctMotions: len(counts),
		ByMotion:        sortedMotions(counts),
	}
	if total == 0 {
		report.Verdict = VerdictSkipped
		report.Detail = "no animated items"
		return report, makeCheck("variety.motion_distribution", VerdictSkipped, report.Detail)
	}

	maxCount := 0
	for motion, count := range counts {
		if count > maxCount || (count == maxCount && (report.MaxMotion == "" || motion < report.MaxMotion)) {
			maxCount = count
			report.MaxMotion = motion
		}
	}
	report.MaxShare = round4(float64(maxCount) / float64(total))
	entropy := 0.0
	for _, count := range counts {
		share := float64(count) / float64(total)
		entropy -= share * math.Log2(share)
	}
	report.EntropyBits = round4(entropy)
	if len(counts) > 1 {
		report.NormalizedEntropy = round4(entropy / math.Log2(float64(len(counts))))
	}

	switch {
	case total < policy.MinItems:
		report.Verdict = VerdictSkipped
		report.Detail = fmt.Sprintf("%d animated items < gate minimum %d", total, policy.MinItems)
	case mode != ModeCertify:
		report.Verdict = VerdictSkipped
		report.Detail = "the variety gate applies to mode certify"
	case report.DistinctMotions < policy.MinDistinct:
		report.Verdict = VerdictFail
		report.Detail = fmt.Sprintf("%d distinct motions < %d", report.DistinctMotions, policy.MinDistinct)
	case report.MaxShare > policy.MaxShare:
		report.Verdict = VerdictFail
		report.Detail = fmt.Sprintf("%s dominates %.0f%% > %.0f%%", report.MaxMotion, report.MaxShare*100, policy.MaxShare*100)
	default:
		report.Verdict = VerdictPass
		report.Detail = fmt.Sprintf("%d distinct motions, max share %.0f%%", report.DistinctMotions, report.MaxShare*100)
	}
	return report, makeCheck("variety.motion_distribution", report.Verdict, report.Detail)
}

func limitations(mode Mode, execution *ExecutionEvidence, artifact *ArtifactEvidence) []string {
	out := make([]string, 0, 3)
	switch mode {
	case ModeFast:
		out = append(out, "mode fast: pixel visibility, entry/exit coverage and the variety gate are SKIPPED by design")
	case ModeNormal:
		out = append(out, "mode normal: visibility is sampled; edge coverage the sample did not reach stays UNVERIFIABLE")
	case ModeCertify:
		out = append(out, "mode certify: every rendered overlay needs pixel-verified visible intervals covering entry and exit")
	}
	if execution == nil {
		out = append(out, "no execution evidence: rendered, visibility, entry/exit and composition are not measured")
	}
	if artifact == nil || artifact.SHA256 == "" {
		out = append(out, "no artifact hash: the receipt is not bound to the final artifact")
	}
	return out
}

func windowOf(item PlanItem, fpsNum, fpsDen int) (startFrame, endFrame int, startMS, endMS int64) {
	startMS, endMS = item.StartMs, item.EndMs
	startUS, endUS := item.StartMs*1000, item.EndMs*1000
	if item.DurationUS > 0 {
		startUS = item.StartUS
		if startUS == 0 {
			startUS = item.StartMs * 1000
		}
		endUS = startUS + item.DurationUS
		startMS = startUS / 1000
		endMS = (endUS + 999) / 1000
	}
	if fpsNum <= 0 || fpsDen <= 0 {
		fpsNum, fpsDen = 24, 1
	}
	startFrame = frameAt(startUS, fpsNum, fpsDen)
	endFrame = frameAt(endUS, fpsNum, fpsDen) - 1
	if endFrame < startFrame {
		endFrame = startFrame
	}
	return startFrame, endFrame, startMS, endMS
}

func frameAt(us int64, fpsNum, fpsDen int) int {
	if us <= 0 {
		return 0
	}
	return int(us * int64(fpsNum) / (1_000_000 * int64(fpsDen)))
}

func validSHA256(value string) bool {
	return digest.IsCanonicalSHA256(value)
}

func shortHash(value string) string {
	if len(value) > 12 {
		return value[:12]
	}
	return value
}

func sampleIDs(ids []string) string {
	const max = 3
	if len(ids) <= max {
		return strings.Join(ids, ", ")
	}
	return strings.Join(ids[:max], ", ") + fmt.Sprintf(" (+%d)", len(ids)-max)
}

func checkVerdict(checks []Check, name string) Verdict {
	for _, check := range checks {
		if check.Name == name {
			return check.Verdict
		}
	}
	return ""
}

func round4(value float64) float64 {
	return math.Round(value*10000) / 10000
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

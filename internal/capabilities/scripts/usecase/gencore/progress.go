// Package scripts — progress.go provides the unified ProgressTracker
// used by the generation pipeline. Each phase of the pipeline emits
// a progress percentage and human-readable message.
//
// The phases are:
//
//	0-10%:  Normalize
//	10-20%: Validate
//	20-40%: Resolve source
//	40-50%: Build plan
//	50-85%: Generate script (engine)
//	85-100%: Postprocessors
//
// A nil ProgressTracker is a no-op — all calls silently succeed.
// Issue 8 / P2 (June 2026): the pre-Issue-8 Phase* methods
// formatted the message (accessing p.item) BEFORE calling Emit,
// which panicked on a nil receiver because p.item dereference
// happened before the nil-guard. The fix routes all Phase* methods
// through a centralized `phase(percent, format, args...)` helper
// that does the nil-guard FIRST, then item-prefixed formatting,
// then Emit. The intent of the package-doc "A nil ProgressTracker
// is a no-op" is now actually true.
package gencore

import (
	"context"
	"fmt"
	"sync"
	"time"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ProgressFn is the callback signature for progress updates.
// percent is 0-100; message is a human-readable description.
type ProgressFn func(percent int, message string)

// EventFn is the callback signature for timeline events.
type EventFn func(eventType string, message string, data map[string]any)

// ProgressTracker emits progress updates through the pipeline phases.
// It is safe for concurrent use when the underlying ProgressFn is
// goroutine-safe.
type ProgressTracker struct {
	fn      ProgressFn
	eventFn EventFn
	item    string // item ID or name for log context
	kind    string // canonical job kind, e.g. script.generate

	activityMu      sync.Mutex
	activityContext context.Context
	currentSubKind  string
	currentDetail   string
	currentPayload  map[string]any
	stageMu         sync.Mutex
	stageProgress   map[string]job.StageProgress
}

// NewProgressTracker creates a ProgressTracker. fn may be nil
// (updates are silently dropped).
func NewProgressTracker(fn ProgressFn, item string) *ProgressTracker {
	return &ProgressTracker{
		fn:            fn,
		item:          item,
		kind:          item,
		stageProgress: make(map[string]job.StageProgress),
	}
}

// SetEventFn wires an event callback. Callers may pass nil to
// disable event emission; the tracker remains nil-safe.
func (p *ProgressTracker) SetEventFn(fn EventFn) {
	if p == nil {
		return
	}
	p.activityMu.Lock()
	p.eventFn = fn
	p.activityMu.Unlock()
}

// SetKind sets the canonical job kind attached to subsequent activity
// events. It is intentionally separate from item: one job may process
// multiple items while retaining the same kind.
func (p *ProgressTracker) SetKind(kind string) {
	if p == nil {
		return
	}
	p.activityMu.Lock()
	p.kind = kind
	p.activityMu.Unlock()
}

// TrackActivity emits a normalized, detailed activity event. payload keeps
// the existing event-specific fields together while the root fields provide
// a stable kind/sub_kind/status/detail contract for API consumers.
func (p *ProgressTracker) TrackActivity(subKind, status, detail string, payload map[string]any) {
	if p == nil {
		return
	}
	p.emitActivity("activity", subKind, status, detail, payload)
}

// TrackEvent emits a typed timeline event if a callback is configured. It
// preserves the existing data keys and adds the common activity envelope.
// A nil receiver or nil callback is a no-op.
func (p *ProgressTracker) TrackEvent(eventType, message string, data map[string]any) {
	if p == nil {
		return
	}
	payload := cloneActivityPayload(data)
	subKind := eventType
	if phase, ok := payload["phase"].(string); ok && phase != "" {
		subKind = phase
	} else if stage, ok := payload["stage"].(string); ok && stage != "" {
		subKind = stage
	}
	status := activityStatus(eventType, payload)
	p.emitActivity(eventType, subKind, status, message, payload)
}

func (p *ProgressTracker) activityData(subKind, status, detail string, payload map[string]any) map[string]any {
	p.activityMu.Lock()
	kind, ctx := p.kind, p.activityContext
	p.activityMu.Unlock()
	if kind == "" {
		kind = p.item
	}
	data := job.ActivityDataWithTrace(kind, subKind, status, detail, payload, job.ActivityTraceFromContext(ctx))
	if stringValue, ok := payload["item_id"].(string); ok {
		data["item_id"] = stringValue
	}
	return data
}

// SetContext binds the execution context so every event carries the stable
// run/attempt identity when the observability run is available.
func (p *ProgressTracker) SetContext(ctx context.Context) {
	if p == nil {
		return
	}
	p.activityMu.Lock()
	p.activityContext = ctx
	p.activityMu.Unlock()
}

func (p *ProgressTracker) emitActivity(eventType, subKind, status, detail string, payload map[string]any) {
	if p == nil {
		return
	}
	p.activityMu.Lock()
	fn := p.eventFn
	p.activityMu.Unlock()
	if fn != nil {
		fn(eventType, detail, p.activityData(subKind, status, detail, payload))
	}
}

func cloneActivityPayload(payload map[string]any) map[string]any {
	out := make(map[string]any, len(payload))
	for key, value := range payload {
		out[key] = value
	}
	return out
}

func activityStatus(eventType string, payload map[string]any) string {
	return job.ActivityStatus(eventType, payload)
}

// TrackStage emits the canonical per-stage/per-language progress observation.
// The event payload is intentionally aligned with kernel/job.StageLanguageStatus
// so parent aggregators and API consumers can use one wire shape.
func (p *ProgressTracker) TrackStage(stage, language string, status, jobID, errMsg string) {
	if p == nil {
		return
	}
	p.stageMu.Lock()
	progress := p.stageProgress[stage]
	progress.Stage = job.StageName(stage)
	updated := false
	for i := range progress.Languages {
		if progress.Languages[i].Language == language && progress.Languages[i].JobID == jobID {
			progress.Languages[i] = job.StageLanguageStatus{
				Stage: job.StageName(stage), Language: language,
				Status: job.StageStatus(status), JobID: jobID, Error: errMsg,
			}
			updated = true
			break
		}
	}
	if !updated {
		progress.Languages = append(progress.Languages, job.StageLanguageStatus{
			Stage: job.StageName(stage), Language: language,
			Status: job.StageStatus(status), JobID: jobID, Error: errMsg,
		})
	}
	progress.Total = len(progress.Languages)
	progress.Completed = 0
	for _, item := range progress.Languages {
		if item.Status == job.StageCompleted {
			progress.Completed++
		}
	}
	p.stageProgress[stage] = progress
	snapshot := make(map[string]job.StageProgress, len(p.stageProgress))
	for key, item := range p.stageProgress {
		item.Languages = append([]job.StageLanguageStatus(nil), item.Languages...)
		snapshot[key] = item
	}
	p.stageMu.Unlock()
	stageData := map[string]any{
		"stage":          stage,
		"language":       language,
		"status":         status,
		"job_id":         jobID,
		"stage_progress": snapshot,
	}
	p.TrackEvent("stage_progress", "Stage progress updated", stageData)
}

// SetStageProgress replaces the current aggregate with a defensive copy
// and emits one structured snapshot event. It is used when an inline
// processor has its own detailed fan-out and returns the completed totals.
func (p *ProgressTracker) SetStageProgress(progress map[string]job.StageProgress) {
	if p == nil {
		return
	}
	p.stageMu.Lock()
	p.stageProgress = make(map[string]job.StageProgress, len(progress))
	for key, item := range progress {
		item.Languages = append([]job.StageLanguageStatus(nil), item.Languages...)
		p.stageProgress[key] = item
	}
	snapshot := make(map[string]job.StageProgress, len(p.stageProgress))
	for key, item := range p.stageProgress {
		item.Languages = append([]job.StageLanguageStatus(nil), item.Languages...)
		snapshot[key] = item
	}
	p.stageMu.Unlock()
	stageData := map[string]any{
		"item_id":        p.item,
		"stage_progress": snapshot,
	}
	p.TrackEvent("stage_progress", "Generation stage progress aggregated", stageData)
}

// StageProgress returns a defensive snapshot of the current counters.
func (p *ProgressTracker) StageProgress() map[string]job.StageProgress {
	if p == nil {
		return nil
	}
	p.stageMu.Lock()
	defer p.stageMu.Unlock()
	out := make(map[string]job.StageProgress, len(p.stageProgress))
	for key, item := range p.stageProgress {
		item.Languages = append([]job.StageLanguageStatus(nil), item.Languages...)
		out[key] = item
	}
	return out
}

// Emit sends a progress update if a callback is configured.
func (p *ProgressTracker) Emit(percent int, message string) {
	if p == nil || p.fn == nil {
		return
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	p.fn(percent, message)
}

// phase is a nil-safe wrapper around Emit. Each Phase* method
// delegates here so the nil-guard + item-prefixed formatting are
// centralized. Issue 8 / P2 (June 2026): pre-Issue-8 Phase* methods
// accessed p.item BEFORE calling Emit, which panicked on a nil
// receiver. The phase helper does the nil-guard FIRST, then formats
// with `[item]` prefix, then calls Emit (which has its own nil-guard
// for the inner ProgressFn path). The format string is the unprefixed
// message; the `[item]` prefix is injected automatically so the
// Phase* call sites stay a single line.
//
// Signature note: (percent int, format string, args ...any) instead
// of the literal (percent int, message string) the user spec
// described. The variadic form lets the helper own the item-prefix
// formatting while keeping the Phase* call sites a single line each.
// If a future Phase* needs a fully pre-formatted message without
// the `[item]` prefix, it can call Emit directly (which is also
// nil-safe via its own guard).
func (p *ProgressTracker) phase(percent int, subKind, format string, args ...any) {
	if p == nil {
		return
	}
	fullArgs := append([]any{p.item}, args...)
	msg := fmt.Sprintf("[%s] "+format, fullArgs...)
	p.Emit(percent, msg)
	p.startActivity(subKind, msg, map[string]any{"item_id": p.item, "progress": percent})
}

func (p *ProgressTracker) startActivity(subKind, detail string, payload map[string]any) {
	if p == nil {
		return
	}
	p.finishActivity("completed", nil)
	p.activityMu.Lock()
	if p.eventFn == nil {
		p.activityMu.Unlock()
		return
	}
	p.currentSubKind = subKind
	p.currentDetail = detail
	p.currentPayload = cloneActivityPayload(payload)
	fn := p.eventFn
	p.activityMu.Unlock()
	fn("activity", detail, p.activityData(subKind, "running", detail, payload))
}

func (p *ProgressTracker) finishActivity(status string, extra map[string]any) {
	if p == nil {
		return
	}
	p.activityMu.Lock()
	if p.currentSubKind == "" || p.eventFn == nil {
		p.activityMu.Unlock()
		return
	}
	subKind, detail, payload, fn := p.currentSubKind, p.currentDetail, cloneActivityPayload(p.currentPayload), p.eventFn
	p.currentSubKind = ""
	p.currentDetail = ""
	p.currentPayload = nil
	p.activityMu.Unlock()
	for key, value := range extra {
		payload[key] = value
	}
	fn("activity", detail, p.activityData(subKind, status, detail, payload))
}

// FailActivity closes the current phase with a reproducible error payload.
func (p *ProgressTracker) FailActivity(err error) {
	if p == nil {
		return
	}
	extra := map[string]any{}
	if err != nil {
		extra["error"] = err.Error()
	}
	p.finishActivity("failed", extra)
}

// Phase helpers emit progress at pre-defined percentage points.
// Each Phase* delegates to the centralized `phase` helper so the
// nil-guard is enforced uniformly. Issue 8 / P2 (June 2026).

func (p *ProgressTracker) PhaseNormalize() {
	p.phase(5, "script.prepare.normalize", "Normalizing request...")
}

func (p *ProgressTracker) PhaseValidate() {
	p.phase(15, "script.prepare.validate", "Validating parameters...")
}

func (p *ProgressTracker) PhaseResolveSource() {
	p.phase(25, "script.prepare.resolve_source", "Resolving source material...")
}

func (p *ProgressTracker) PhaseBuildPlan() {
	p.phase(45, "script.prepare.build_plan", "Building generation plan...")
}

func (p *ProgressTracker) PhaseGenerateStart() {
	p.phase(55, "script.generate", "Generating script via AI...")
}

func (p *ProgressTracker) PhaseGenerateDone() {
	if p == nil {
		return
	}
	p.Emit(85, fmt.Sprintf("[%s] Script generated.", p.item))
	p.finishActivity("completed", nil)
}

func (p *ProgressTracker) PhasePostprocess(processor string) {
	p.PhasePostprocessProgress(0, 1, processor)
}

// PhasePostprocessProgress maps the current processor onto the postprocess
// window instead of emitting the same static 90% value for every processor.
func (p *ProgressTracker) PhasePostprocessProgress(index, total int, processor string) {
	if total <= 0 {
		return
	}
	percent := 55 + ((index + 1) * 40 / total)
	if percent > 95 {
		percent = 95
	}
	p.phase(percent, "script.postprocess."+processor, "Running postprocessor %d/%d: %s...", index+1, total, processor)
}

// PhasePostprocessEvent reports the real registry execution boundary. The
// legacy PhasePostprocessProgress helper remains available for older callers,
// but new flows must use this event-aware method.
func (p *ProgressTracker) PhasePostprocessEvent(index, total int, processor, status string, duration time.Duration, err error) {
	if p == nil || total <= 0 {
		return
	}
	percent := 55 + ((index + 1) * 40 / total)
	if percent > 95 {
		percent = 95
	}
	var detail string
	switch status {
	case "started":
		detail = fmt.Sprintf("[%s] Running postprocessor %d/%d: %s...", p.item, index+1, total, processor)
	case "completed":
		detail = fmt.Sprintf("[%s] Completed postprocessor %d/%d: %s (%s)", p.item, index+1, total, processor, duration.Round(time.Millisecond))
	case "failed":
		reason := "unknown error"
		if err != nil {
			reason = err.Error()
		}
		detail = fmt.Sprintf("[%s] Failed postprocessor %d/%d: %s (%s): %s", p.item, index+1, total, processor, duration.Round(time.Millisecond), reason)
	default:
		return
	}
	p.Emit(percent, detail)
	payload := map[string]any{
		"item_id":     p.item,
		"processor":   processor,
		"index":       index + 1,
		"total":       total,
		"duration_ms": duration.Milliseconds(),
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	subKind := "script.postprocess." + processor
	if status == "started" {
		p.finishActivity("completed", nil)
		p.TrackActivity(subKind, "running", detail, payload)
		p.activityMu.Lock()
		p.currentSubKind = subKind
		p.currentDetail = detail
		p.currentPayload = cloneActivityPayload(payload)
		p.activityMu.Unlock()
	} else if status == "completed" || status == "failed" {
		p.finishActivity(status, payload)
	}
}

func (p *ProgressTracker) PhaseComplete() {
	if p == nil {
		return
	}
	p.finishActivity("completed", nil)
	p.Emit(100, fmt.Sprintf("[%s] Generation complete.", p.item))
	p.TrackActivity("script.complete", "completed", fmt.Sprintf("[%s] Generation complete.", p.item), map[string]any{
		"item_id":  p.item,
		"progress": 100,
	})
}

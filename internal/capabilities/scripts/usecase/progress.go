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
package usecase

import (
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

	stageMu       sync.Mutex
	stageProgress map[string]job.StageProgress
}

// NewProgressTracker creates a ProgressTracker. fn may be nil
// (updates are silently dropped).
func NewProgressTracker(fn ProgressFn, item string) *ProgressTracker {
	return &ProgressTracker{
		fn:            fn,
		item:          item,
		stageProgress: make(map[string]job.StageProgress),
	}
}

// SetEventFn wires an event callback. Callers may pass nil to
// disable event emission; the tracker remains nil-safe.
func (p *ProgressTracker) SetEventFn(fn EventFn) {
	if p == nil {
		return
	}
	p.eventFn = fn
}

// TrackEvent emits a typed timeline event if a callback is configured.
// A nil receiver or nil callback is a no-op.
func (p *ProgressTracker) TrackEvent(eventType, message string, data map[string]any) {
	if p == nil || p.eventFn == nil {
		return
	}
	p.eventFn(eventType, message, data)
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

	p.TrackEvent("stage_progress", "Stage progress updated", map[string]any{
		"stage":          stage,
		"language":       language,
		"status":         status,
		"job_id":         jobID,
		"stage_progress": snapshot,
	})
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
	p.TrackEvent("stage_progress", "Generation stage progress aggregated", map[string]any{
		"item_id":        p.item,
		"stage_progress": snapshot,
	})
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
func (p *ProgressTracker) phase(percent int, format string, args ...any) {
	if p == nil {
		return
	}
	fullArgs := append([]any{p.item}, args...)
	msg := fmt.Sprintf("[%s] "+format, fullArgs...)
	p.Emit(percent, msg)
}

// Phase helpers emit progress at pre-defined percentage points.
// Each Phase* delegates to the centralized `phase` helper so the
// nil-guard is enforced uniformly. Issue 8 / P2 (June 2026).

func (p *ProgressTracker) PhaseNormalize() {
	p.phase(5, "Normalizing request...")
}

func (p *ProgressTracker) PhaseValidate() {
	p.phase(15, "Validating parameters...")
}

func (p *ProgressTracker) PhaseResolveSource() {
	p.phase(25, "Resolving source material...")
}

func (p *ProgressTracker) PhaseBuildPlan() {
	p.phase(45, "Building generation plan...")
}

func (p *ProgressTracker) PhaseGenerateStart() {
	p.phase(55, "Generating script via AI...")
}

func (p *ProgressTracker) PhaseGenerateDone() {
	p.phase(85, "Script generated.")
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
	p.phase(percent, "Running postprocessor %d/%d: %s...", index+1, total, processor)
}

// PhasePostprocessEvent reports the real registry execution boundary. The
// legacy PhasePostprocessProgress helper remains available for older callers,
// but new flows must use this event-aware method.
func (p *ProgressTracker) PhasePostprocessEvent(index, total int, processor, status string, duration time.Duration, err error) {
	if total <= 0 {
		return
	}
	percent := 55 + ((index + 1) * 40 / total)
	if percent > 95 {
		percent = 95
	}
	switch status {
	case "started":
		p.phase(percent, "Running postprocessor %d/%d: %s...", index+1, total, processor)
	case "completed":
		p.phase(percent, "Completed postprocessor %d/%d: %s (%s)", index+1, total, processor, duration.Round(time.Millisecond))
	case "failed":
		reason := "unknown error"
		if err != nil {
			reason = err.Error()
		}
		p.phase(percent, "Failed postprocessor %d/%d: %s (%s): %s", index+1, total, processor, duration.Round(time.Millisecond), reason)
	}
}

func (p *ProgressTracker) PhaseComplete() {
	p.phase(100, "Generation complete.")
}

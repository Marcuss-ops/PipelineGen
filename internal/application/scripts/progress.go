// Package scripts — progress.go provides the unified ProgressTracker
// used by the generation pipeline. Each phase of the pipeline emits
// a progress percentage and human-readable message.
//
// The phases are:
//   0-10%:  Normalize
//   10-20%: Validate
//   20-40%: Resolve source
//   40-50%: Build plan
//   50-85%: Generate script (engine)
//   85-100%: Postprocessors
//
// A nil ProgressTracker is a no-op — all calls silently succeed.
package scripts

import "fmt"

// ProgressFn is the callback signature for progress updates.
// percent is 0-100; message is a human-readable description.
type ProgressFn func(percent int, message string)

// ProgressTracker emits progress updates through the pipeline phases.
// It is safe for concurrent use when the underlying ProgressFn is
// goroutine-safe.
type ProgressTracker struct {
	fn   ProgressFn
	item string // item ID or name for log context
}

// NewProgressTracker creates a ProgressTracker. fn may be nil
// (updates are silently dropped).
func NewProgressTracker(fn ProgressFn, item string) *ProgressTracker {
	return &ProgressTracker{fn: fn, item: item}
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

// Phase helpers emit progress at pre-defined percentage points.

func (p *ProgressTracker) PhaseNormalize() {
	p.Emit(5, fmt.Sprintf("[%s] Normalizing request...", p.item))
}

func (p *ProgressTracker) PhaseValidate() {
	p.Emit(15, fmt.Sprintf("[%s] Validating parameters...", p.item))
}

func (p *ProgressTracker) PhaseResolveSource() {
	p.Emit(25, fmt.Sprintf("[%s] Resolving source material...", p.item))
}

func (p *ProgressTracker) PhaseBuildPlan() {
	p.Emit(45, fmt.Sprintf("[%s] Building generation plan...", p.item))
}

func (p *ProgressTracker) PhaseGenerateStart() {
	p.Emit(55, fmt.Sprintf("[%s] Generating script via AI...", p.item))
}

func (p *ProgressTracker) PhaseGenerateDone() {
	p.Emit(85, fmt.Sprintf("[%s] Script generated.", p.item))
}

func (p *ProgressTracker) PhasePostprocess(processor string) {
	p.Emit(90, fmt.Sprintf("[%s] Running postprocessor: %s...", p.item, processor))
}

func (p *ProgressTracker) PhaseComplete() {
	p.Emit(100, fmt.Sprintf("[%s] Generation complete.", p.item))
}

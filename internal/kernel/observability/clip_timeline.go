package observability

import (
	"context"
	"time"
)

// ClipTimelinePhase identifies a canonical clip lifecycle boundary.
type ClipTimelinePhase string

const (
	ClipPhaseSubmitted  ClipTimelinePhase = "submitted"
	ClipPhaseClaimed    ClipTimelinePhase = "claimed"
	ClipPhasePrepare    ClipTimelinePhase = "prepare"
	ClipPhaseRenderSlot ClipTimelinePhase = "render_slot"
	ClipPhaseFFmpeg     ClipTimelinePhase = "ffmpeg"
	ClipPhaseHashProbe  ClipTimelinePhase = "hash_probe"
	ClipPhaseUploadSlot ClipTimelinePhase = "upload_slot"
	ClipPhaseDrive      ClipTimelinePhase = "drive"
	ClipPhaseFinalize   ClipTimelinePhase = "finalize"
)

// ClipTimelineEntry is one owner-measured interval in a RunReport. Identity
// is inherited from the enclosing RunReport, so each entry is automatically
// linked to JobID, AttemptID and RunID.
type ClipTimelineEntry struct {
	Phase       ClipTimelinePhase `json:"phase"`
	StartedAt   time.Time         `json:"started_at,omitempty"`
	FinishedAt  time.Time         `json:"finished_at,omitempty"`
	DurationMs  int64             `json:"duration_ms"`
	Status      string            `json:"status"`
	ErrorCode   string            `json:"error_code,omitempty"`
	QueueWaitMs int64             `json:"queue_wait_ms,omitempty"`
}

// ClipTimeline is the canonical ordered clip lifecycle projection.
type ClipTimeline struct {
	Entries []ClipTimelineEntry `json:"entries,omitempty"`
}

func (t *ClipTimeline) append(entry ClipTimelineEntry) {
	if t == nil || entry.Phase == "" {
		return
	}
	t.Entries = append(t.Entries, entry)
}

// ClipTimeline returns the current timeline snapshot for the run.
func (r *Run) ClipTimeline() *ClipTimeline {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneClipTimeline(r.report.ClipTimeline)
}

// RecordClipPhase records an owner-measured clip phase. Invalid or reversed
// intervals are ignored; instrumentation never changes functional behavior.
func (r *Run) RecordClipPhase(phase ClipTimelinePhase, startedAt, finishedAt time.Time, status string, err error) {
	if r == nil || phase == "" || startedAt.IsZero() || finishedAt.IsZero() || finishedAt.Before(startedAt) {
		return
	}
	if status == "" {
		status = StageStatusCompleted
	}
	entry := ClipTimelineEntry{
		Phase: phase, StartedAt: startedAt, FinishedAt: finishedAt,
		DurationMs: nonNegative(finishedAt.Sub(startedAt).Milliseconds()), Status: status,
	}
	if err != nil {
		entry.Status = StageStatusFailed
		entry.ErrorCode = errorCode(err)
	}
	// Slot phases expose the actual blocked interval in the typed wait model.
	if phase == ClipPhaseRenderSlot || phase == ClipPhaseUploadSlot {
		entry.QueueWaitMs = entry.DurationMs
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	if r.report.ClipTimeline == nil {
		r.report.ClipTimeline = &ClipTimeline{}
	}
	r.report.ClipTimeline.append(entry)
	r.mu.Unlock()
}

// RecordClipPhase is the context-bound, no-op-safe form.
func RecordClipPhase(ctx context.Context, phase ClipTimelinePhase, startedAt, finishedAt time.Time, status string, err error) {
	if run := FromContext(ctx); run != nil {
		run.RecordClipPhase(phase, startedAt, finishedAt, status, err)
	}
}

func cloneClipTimeline(in *ClipTimeline) *ClipTimeline {
	if in == nil {
		return nil
	}
	out := &ClipTimeline{Entries: append([]ClipTimelineEntry(nil), in.Entries...)}
	return out
}

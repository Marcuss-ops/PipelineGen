// kernel/observability/scene_timeline.go —
//
// Per-scene lifecycle trace (§7.3 of the pipeline roadmap: "registrare
// ready_at/enqueued_at/started_at/completed_at per testo, traduzione, NLP,
// TTS, asset e audio ... calcolare il critical path dalla trace, senza sommare
// work concorrente").
//
// One SceneStageEntry is the owner-measured lifecycle of ONE scene through ONE
// pipeline stage. The four §7.3 instants are carried verbatim; the derived
// metrics (queue wait, work, total) are computed only from timestamps the
// owner actually supplied — the kernel never guesses an instant it was not
// given (godlike/07 no-fake-availability: an unmeasured half produces no
// derived number, never a zero that reads as measured).
//
// The trace feeds SceneTimeline.CriticalPath (scene_timeline_critical_path.go),
// which computes the longest dependency chain WITHOUT summing concurrent work.
package observability

import (
	"context"
	"time"
)

// SceneStage identifies one per-scene pipeline stage (the §7.3 vocabulary:
// testo, traduzione, NLP, TTS, asset e audio).
type SceneStage string

const (
	SceneStageText        SceneStage = "text"
	SceneStageTranslation SceneStage = "translation"
	SceneStageNLP         SceneStage = "nlp"
	SceneStageTTS         SceneStage = "tts"
	SceneStageAsset       SceneStage = "asset"
	SceneStageAudio       SceneStage = "audio"
)

// SceneStageTimes are the four §7.3 lifecycle instants of one (scene, stage).
// Any subset may be zero when the owner cannot observe it; the derived metrics
// on the entry are computed only from the instants that are present (godlike/07
// no-fake-availability: an unmeasured half produces NO derived number, never a
// zero that reads as measured). The one hard requirement (enforced by
// RecordSceneStage) is CompletedAt — the entry's anchor.
type SceneStageTimes struct {
	// ReadyAt is when the upstream dependency was satisfied: the scene may
	// enter this stage from this instant on.
	ReadyAt time.Time
	// EnqueuedAt is when the work was handed to the queue/pool that owns it.
	EnqueuedAt time.Time
	// StartedAt is when execution actually began.
	StartedAt time.Time
	// CompletedAt is when the stage finished for this scene.
	CompletedAt time.Time
}

// SceneStageEntry is one projected row of the per-scene trace. Identity is
// inherited from the enclosing RunReport (JobID / AttemptID / RunID), exactly
// like ClipTimelineEntry.
type SceneStageEntry struct {
	SceneID     string     `json:"scene_id"`
	Stage       SceneStage `json:"stage"`
	ReadyAt     time.Time  `json:"ready_at,omitempty"`
	EnqueuedAt  time.Time  `json:"enqueued_at,omitempty"`
	StartedAt   time.Time  `json:"started_at,omitempty"`
	CompletedAt time.Time  `json:"completed_at,omitempty"`
	// QueueWaitMs = StartedAt − EnqueuedAt (both present).
	QueueWaitMs int64 `json:"queue_wait_ms,omitempty"`
	// WorkMs = CompletedAt − StartedAt (present only when the owner measured
	// the work span; a boundary anchor entry carries no work).
	WorkMs int64 `json:"work_ms,omitempty"`
	// TotalMs = CompletedAt − ReadyAt (both present) — stage latency as the
	// upstream observed it, wait included.
	TotalMs   int64  `json:"total_ms,omitempty"`
	Status    string `json:"status"`
	ErrorCode string `json:"error_code,omitempty"`
	Language  string `json:"language,omitempty"`
}

// SceneTimeline is the canonical per-scene lifecycle projection. Entries are
// append-only and ordered by recording time; grouping by (scene, stage) is the
// reader's projection (translation/TTS record one entry per language).
type SceneTimeline struct {
	Entries []SceneStageEntry `json:"entries,omitempty"`
}

func (t *SceneTimeline) append(entry SceneStageEntry) {
	if t == nil || entry.SceneID == "" || entry.Stage == "" {
		return
	}
	t.Entries = append(t.Entries, entry)
}

// SceneTimeline returns the current per-scene trace snapshot for the run.
func (r *Run) SceneTimeline() *SceneTimeline {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneSceneTimeline(r.report.SceneTimeline)
}

// RecordSceneStage records one owner-measured per-scene stage lifecycle.
// CompletedAt is the required anchor; the other instants are recorded when the
// owner observed them. Entries with a reversed interval are ignored;
// instrumentation never changes functional behavior. Derived metrics are
// computed only from supplied instants.
func (r *Run) RecordSceneStage(sceneID string, stage SceneStage, times SceneStageTimes, language, status string, err error) {
	if r == nil || sceneID == "" || stage == "" || times.CompletedAt.IsZero() {
		return
	}
	if !times.StartedAt.IsZero() && times.CompletedAt.Before(times.StartedAt) {
		return
	}
	if status == "" {
		status = StageStatusCompleted
	}
	entry := SceneStageEntry{
		SceneID:     sceneID,
		Stage:       stage,
		ReadyAt:     times.ReadyAt,
		EnqueuedAt:  times.EnqueuedAt,
		StartedAt:   times.StartedAt,
		CompletedAt: times.CompletedAt,
		Status:      status,
		Language:    language,
	}
	if !times.StartedAt.IsZero() {
		entry.WorkMs = nonNegative(times.CompletedAt.Sub(times.StartedAt).Milliseconds())
	}
	if !times.EnqueuedAt.IsZero() && !times.StartedAt.Before(times.EnqueuedAt) {
		entry.QueueWaitMs = nonNegative(times.StartedAt.Sub(times.EnqueuedAt).Milliseconds())
	}
	if !times.ReadyAt.IsZero() && !times.CompletedAt.Before(times.ReadyAt) {
		entry.TotalMs = nonNegative(times.CompletedAt.Sub(times.ReadyAt).Milliseconds())
	}
	if err != nil {
		entry.Status = StageStatusFailed
		entry.ErrorCode = errorCode(err)
	}
	r.mu.Lock()
	if r.finished {
		r.mu.Unlock()
		return
	}
	if r.report.SceneTimeline == nil {
		r.report.SceneTimeline = &SceneTimeline{}
	}
	r.report.SceneTimeline.append(entry)
	r.mu.Unlock()
}

// RecordSceneStage is the context-bound, no-op-safe form: it records on the
// Run bound to ctx, or does nothing when no Run is bound.
func RecordSceneStage(ctx context.Context, sceneID string, stage SceneStage, times SceneStageTimes, language, status string, err error) {
	if run := FromContext(ctx); run != nil {
		run.RecordSceneStage(sceneID, stage, times, language, status, err)
	}
}

func cloneSceneTimeline(in *SceneTimeline) *SceneTimeline {
	if in == nil {
		return nil
	}
	return &SceneTimeline{Entries: append([]SceneStageEntry(nil), in.Entries...)}
}

// entryStart is the earliest OBSERVED instant of an entry: its measured start
// when the owner has one, otherwise the closest upstream anchor (enqueued →
// ready → completed). The critical-path gaps are computed against it so an
// anchor-only entry never fabricates a start it did not measure.
func entryStart(e SceneStageEntry) time.Time {
	switch {
	case !e.StartedAt.IsZero():
		return e.StartedAt
	case !e.EnqueuedAt.IsZero():
		return e.EnqueuedAt
	case !e.ReadyAt.IsZero():
		return e.ReadyAt
	default:
		return e.CompletedAt
	}
}

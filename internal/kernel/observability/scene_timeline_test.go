// kernel/observability/scene_timeline_test.go —
//
// Contract tests for the per-scene lifecycle trace (§7.3) and the critical
// path computed FROM it: the four ready/enqueued/started/completed instants,
// derived metrics computed only from supplied instants, and a critical path
// that never sums concurrent work.
package observability

import (
	"context"
	"strings"
	"testing"
	"time"
)

func sceneRun() *Run {
	return NewRunObserver(nil).StartRun(context.Background(), RunInfo{RunID: "run-scene", JobID: "job-scene", AttemptID: "attempt-scene"})
}

func ms(base time.Time, offset int64) time.Time {
	return base.Add(time.Duration(offset) * time.Millisecond)
}

// TestRunSceneTimeline_DerivedMetricsComeOnlyFromSuppliedInstants pins the
// no-fake-zero rule: QueueWaitMs exists only with (enqueued, started), TotalMs
// only with (ready, completed); the work span always exists (it is the entry
// requirement).
func TestRunSceneTimeline_DerivedMetricsComeOnlyFromSuppliedInstants(t *testing.T) {
	run := sceneRun()
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	run.RecordSceneStage("scene-0", SceneStageNLP, SceneStageTimes{
		ReadyAt:     ms(base, 0),
		EnqueuedAt:  ms(base, 10),
		StartedAt:   ms(base, 30),
		CompletedAt: ms(base, 130),
	}, "", "", nil)
	// Only the work span is known.
	run.RecordSceneStage("scene-0", SceneStageText, SceneStageTimes{
		StartedAt:   ms(base, 0),
		CompletedAt: ms(base, 40),
	}, "en", "", nil)

	report := run.Report()
	if report.SceneTimeline == nil || len(report.SceneTimeline.Entries) != 2 {
		t.Fatalf("scene timeline = %+v, want 2 entries", report.SceneTimeline)
	}
	nlp := report.SceneTimeline.Entries[0]
	if nlp.QueueWaitMs != 20 || nlp.WorkMs != 100 || nlp.TotalMs != 130 {
		t.Fatalf("derived = queue %d work %d total %d, want 20/100/130", nlp.QueueWaitMs, nlp.WorkMs, nlp.TotalMs)
	}
	text := report.SceneTimeline.Entries[1]
	if text.QueueWaitMs != 0 || text.TotalMs != 0 || text.WorkMs != 40 {
		t.Fatalf("unmeasured halves must stay 0: %+v", text)
	}
	if text.Language != "en" {
		t.Fatalf("language = %q, want en", text.Language)
	}
}

// TestRunSceneTimeline_RejectsInvalidAndPostFinishEntries pins the guard
// rails: no work span, reversed intervals and post-finish recordings are
// ignored — instrumentation never invents rows.
func TestRunSceneTimeline_RejectsInvalidAndPostFinishEntries(t *testing.T) {
	run := sceneRun()
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	run.RecordSceneStage("", SceneStageNLP, SceneStageTimes{StartedAt: base, CompletedAt: ms(base, 5)}, "", "", nil)
	run.RecordSceneStage("scene-0", "", SceneStageTimes{StartedAt: base, CompletedAt: ms(base, 5)}, "", "", nil)
	// No anchor (CompletedAt missing): not recordable.
	run.RecordSceneStage("scene-0", SceneStageNLP, SceneStageTimes{ReadyAt: base, StartedAt: base}, "", "", nil)
	// Reversed interval.
	run.RecordSceneStage("scene-0", SceneStageNLP, SceneStageTimes{StartedAt: ms(base, 10), CompletedAt: base}, "", "", nil)
	if got := run.SceneTimeline(); got != nil && len(got.Entries) != 0 {
		t.Fatalf("invalid entries must be ignored, got %+v", got)
	}
	// An anchor entry (completed only) IS recordable: the text-stage anchor is
	// exactly this shape — its start is unobservable at the commit seam.
	run.RecordSceneStage("scene-0", SceneStageText, SceneStageTimes{CompletedAt: ms(base, 5)}, "en", "", nil)
	got0 := run.SceneTimeline()
	if got0 == nil || len(got0.Entries) != 1 || got0.Entries[0].WorkMs != 0 {
		t.Fatalf("anchor entry = %+v, want one entry with no work span", got0)
	}

	run.RecordSceneStage("scene-1", SceneStageNLP, SceneStageTimes{StartedAt: base, CompletedAt: ms(base, 5)}, "", "", nil)
	run.Finish()
	run.RecordSceneStage("scene-2", SceneStageNLP, SceneStageTimes{StartedAt: base, CompletedAt: ms(base, 5)}, "", "", nil)
	got := run.SceneTimeline()
	if got == nil || len(got.Entries) != 2 {
		t.Fatalf("post-finish entry must be ignored, got %+v", got)
	}
}

// TestSceneTimelineCriticalPath_PicksTheSlowBranchNotTheSum is THE §7.3 pin:
// translation (50ms) and nlp (5ms) run concurrently after text; the critical
// path must traverse translation ONLY — never sum the two branches.
func TestSceneTimelineCriticalPath_PicksTheSlowBranchNotTheSum(t *testing.T) {
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	timeline := &SceneTimeline{}
	record := func(sceneID string, stage SceneStage, lang string, ready, enqueued, started, completed int64) {
		timeline.append(SceneStageEntry{
			SceneID: sceneID, Stage: stage, Language: lang,
			ReadyAt: ms(base, ready), EnqueuedAt: ms(base, enqueued),
			StartedAt: ms(base, started), CompletedAt: ms(base, completed),
			WorkMs: completed - started, TotalMs: completed - ready, QueueWaitMs: started - enqueued,
			Status: StageStatusCompleted,
		})
	}
	// text: work 10ms (0→10)
	record("scene-0", SceneStageText, "en", 0, 0, 0, 10)
	// translation: ready at text completion, work 50ms (10→60)
	record("scene-0", SceneStageTranslation, "en", 10, 10, 10, 60)
	// nlp: concurrent with translation, work 5ms (10→15)
	record("scene-0", SceneStageNLP, "", 10, 10, 10, 15)
	// asset: after nlp, work 5ms (15→20)
	record("scene-0", SceneStageAsset, "", 15, 15, 15, 20)
	// audio: join at 60 (waits for translation), work 5ms (60→65)
	record("scene-0", SceneStageAudio, "", 60, 60, 60, 65)

	path := timeline.CriticalPath()
	if path == nil {
		t.Fatal("critical path must exist for a populated trace")
	}
	var got []string
	for _, e := range path.Chain {
		got = append(got, string(e.Stage))
	}
	want := "text→translation→audio"
	if strings.Join(got, "→") != want {
		t.Fatalf("critical chain = %v, want %s (the slow branch only — concurrent work must NOT be summed)", got, want)
	}
	// Chain weight: text 10 + gap 0 + translation 50 + gap 0 + audio 5 = 65.
	if path.TotalMs != 65 {
		t.Fatalf("critical path total = %dms, want 65ms", path.TotalMs)
	}
}

// TestSceneTimelineCriticalPath_EqualWeightEndsAtTheDeepestLeaf pins the
// selection rule the real-seam contract test exposed: a final stage with no
// measurable work (or a sub-millisecond span, which rounds to 0) ties the
// chain weight of its predecessor, and the path must still END at that leaf
// instead of stopping at the intermediate node. The text anchor is the shared
// language-agnostic root, exactly as the pipeline records it.
func TestSceneTimelineCriticalPath_EqualWeightEndsAtTheDeepestLeaf(t *testing.T) {
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	timeline := &SceneTimeline{Entries: []SceneStageEntry{
		// Text anchor: completed-only, language-agnostic root.
		{SceneID: "scene-0", Stage: SceneStageText, CompletedAt: base, Status: StageStatusCompleted},
		// Translation: queued 5ms behind the root, 10ms of real work.
		{SceneID: "scene-0", Stage: SceneStageTranslation, Language: "es", StartedAt: ms(base, 5), CompletedAt: ms(base, 15), WorkMs: 10, Status: StageStatusCompleted},
		// TTS: starts the instant the translation completes; its work rounds
		// to 0ms, so its chain weight TIES the translation's.
		{SceneID: "scene-0", Stage: SceneStageTTS, Language: "es", StartedAt: ms(base, 15), CompletedAt: ms(base, 15), Status: StageStatusCompleted},
	}}
	path := timeline.CriticalPath()
	if path == nil {
		t.Fatal("critical path must exist")
	}
	got := ""
	for i, entry := range path.Chain {
		if i > 0 {
			got += "→"
		}
		got += string(entry.Stage)
	}
	if got != "text→translation→tts" {
		t.Fatalf("chain = %s, want text→translation→tts (equal weight must end at the deepest leaf)", got)
	}
	if path.TotalMs != 15 {
		t.Fatalf("total = %dms, want 15ms (5ms root gap + 10ms translation)", path.TotalMs)
	}
}

// TestSceneTimelineCriticalPath_ChargesObservedDependencyGaps pins that the
// gap between a predecessor's completion and the successor's start is part of
// the chain: the successor queued for 25ms behind an unrecorded dependency.
func TestSceneTimelineCriticalPath_ChargesObservedDependencyGaps(t *testing.T) {
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	timeline := &SceneTimeline{Entries: []SceneStageEntry{
		{SceneID: "scene-0", Stage: SceneStageText, StartedAt: ms(base, 0), CompletedAt: ms(base, 10), WorkMs: 10, Status: StageStatusCompleted},
		// nlp starts 25ms after text completed (its queue wait against the
		// unrecorded lanes): the gap belongs to the chain.
		{SceneID: "scene-0", Stage: SceneStageNLP, StartedAt: ms(base, 35), CompletedAt: ms(base, 45), WorkMs: 10, Status: StageStatusCompleted},
	}}
	path := timeline.CriticalPath()
	if path == nil || path.TotalMs != 45 {
		t.Fatalf("path = %+v, want total 45ms (10 work + 25 gap + 10 work)", path)
	}
	if len(path.Chain) != 2 || path.Chain[0].Stage != SceneStageText || path.Chain[1].Stage != SceneStageNLP {
		t.Fatalf("chain = %+v, want text→nlp", path.Chain)
	}
}

// TestSceneTimelineCriticalPath_PicksTheBindingScene pins the parallel-scene
// dimension: two scenes run concurrently; the path is the heavier scene's
// chain, never the sum across scenes.
func TestSceneTimelineCriticalPath_PicksTheBindingScene(t *testing.T) {
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	timeline := &SceneTimeline{Entries: []SceneStageEntry{
		{SceneID: "scene-light", Stage: SceneStageText, StartedAt: ms(base, 0), CompletedAt: ms(base, 5), WorkMs: 5, Status: StageStatusCompleted},
		{SceneID: "scene-light", Stage: SceneStageNLP, StartedAt: ms(base, 5), CompletedAt: ms(base, 10), WorkMs: 5, Status: StageStatusCompleted},
		{SceneID: "scene-heavy", Stage: SceneStageText, StartedAt: ms(base, 0), CompletedAt: ms(base, 20), WorkMs: 20, Status: StageStatusCompleted},
		{SceneID: "scene-heavy", Stage: SceneStageNLP, StartedAt: ms(base, 20), CompletedAt: ms(base, 80), WorkMs: 60, Status: StageStatusCompleted},
	}}
	path := timeline.CriticalPath()
	if path == nil || path.TotalMs != 80 {
		t.Fatalf("path = %+v, want 80ms (the heavy scene's chain)", path)
	}
	for _, e := range path.Chain {
		if e.SceneID != "scene-heavy" {
			t.Fatalf("chain crossed into %q — parallel scenes must not be summed", e.SceneID)
		}
	}
}

// TestSceneTimelineCriticalPath_LanguageScopedDependencies pins the lane
// rule: tts for language L chains to translation for language L — never to
// another language's translation, even when that lane is heavier. Without the
// scoping the it lane (still running when tts(en) starts) would hijack the
// chain through the clamped pipelining gap.
func TestSceneTimelineCriticalPath_LanguageScopedDependencies(t *testing.T) {
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	timeline := &SceneTimeline{Entries: []SceneStageEntry{
		{SceneID: "scene-0", Stage: SceneStageText, StartedAt: ms(base, 0), CompletedAt: ms(base, 10), WorkMs: 10, Status: StageStatusCompleted},
		// translation(en) is the en lane; translation(it) is heavier and still
		// in flight when tts(en) starts (pipelined per-language fan-out).
		{SceneID: "scene-0", Stage: SceneStageTranslation, Language: "en", StartedAt: ms(base, 10), CompletedAt: ms(base, 20), WorkMs: 10, Status: StageStatusCompleted},
		{SceneID: "scene-0", Stage: SceneStageTranslation, Language: "it", StartedAt: ms(base, 10), CompletedAt: ms(base, 90), WorkMs: 80, Status: StageStatusCompleted},
		// tts(en) runs 20→200 and dominates the wall.
		{SceneID: "scene-0", Stage: SceneStageTTS, Language: "en", StartedAt: ms(base, 20), CompletedAt: ms(base, 200), WorkMs: 180, Status: StageStatusCompleted},
	}}
	path := timeline.CriticalPath()
	if path == nil {
		t.Fatal("critical path must exist")
	}
	// With language scoping: text(10) → translation(en)(10) → tts(en)(180) = 200.
	// Without it, the chain would enter tts through translation(it) and report
	// 270 (90 + 180) with the wrong lane.
	if path.TotalMs != 200 {
		t.Fatalf("total = %d, want 200 (the en lane must gate tts(en))", path.TotalMs)
	}
	var got []string
	for _, e := range path.Chain {
		got = append(got, string(e.Stage)+"/"+e.Language)
	}
	want := "text/→translation/en→tts/en"
	if strings.Join(got, "→") != want {
		t.Fatalf("chain = %v, want %s", got, want)
	}
}

// TestSceneTimeline_JSONProjection pins that the trace rides the canonical
// report JSON (the recorder persists the report verbatim — no extra table).
func TestSceneTimeline_JSONProjection(t *testing.T) {
	run := sceneRun()
	base := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	run.RecordSceneStage("scene-0", SceneStageNLP, SceneStageTimes{
		ReadyAt: base, EnqueuedAt: base, StartedAt: ms(base, 5), CompletedAt: ms(base, 55),
	}, "", "", nil)
	body, err := run.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if !containsBytes(body, []byte(`"scene_timeline"`)) || !containsBytes(body, []byte(`"stage":"nlp"`)) {
		t.Fatalf("scene_timeline missing from report JSON: %s", body)
	}
}

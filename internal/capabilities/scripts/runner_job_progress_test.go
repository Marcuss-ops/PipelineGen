package scriptgeneration

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/renderqueue"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type recordingProgressSink struct {
	plain []int
	data  []int
	last  map[string]any
}

func (s *recordingProgressSink) SetProgress(_ context.Context, _ string, progress int, _ string) error {
	s.plain = append(s.plain, progress)
	return nil
}

func (s *recordingProgressSink) SetProgressData(_ context.Context, _ string, progress int, _ string, data map[string]any) error {
	s.data = append(s.data, progress)
	s.last = data
	return nil
}

type plainProgressSink struct {
	last int
}

func (s *plainProgressSink) SetProgress(_ context.Context, _ string, progress int, _ string) error {
	s.last = progress
	return nil
}

func TestWriteJobProgressPrefersStructuredSink(t *testing.T) {
	sink := &recordingProgressSink{}
	r := &Runner{jobProgressSink: sink, log: zap.NewNop()}
	r.writeJobProgress(context.Background(), "job-1", job.StageVoiceover, 52, "voiceover 3/10", map[string]any{
		"stage": "voiceover", "completed": 3, "total": 10,
	})
	if len(sink.data) != 1 || sink.data[0] != 52 {
		t.Fatalf("structured progress = %v, want one 52%% write", sink.data)
	}
	if sink.last["completed"] != 3 || sink.last["total"] != 10 {
		t.Fatalf("activity = %v, want completed/total", sink.last)
	}
	if len(sink.plain) != 0 {
		t.Fatalf("plain fallback used together with structured sink: %v", sink.plain)
	}
}

func TestWriteJobProgressFallsBackToPlainSink(t *testing.T) {
	sink := &plainProgressSink{}
	r := &Runner{jobProgressSink: sink, log: zap.NewNop()}
	r.writeJobProgress(context.Background(), "job-1", job.StageRender, 78, "render", map[string]any{"stage": "render"})
	if sink.last != 78 {
		t.Fatalf("plain progress = %d, want 78", sink.last)
	}
}

func TestJobStageProgressBandsAreMonotone(t *testing.T) {
	stages := []job.StageName{
		job.StageScript, job.StageTranslation, job.StageVoiceover,
		job.StageRender, job.StageUpload, job.StagePersistence,
	}
	lastCeiling := -1
	for _, stage := range stages {
		floor, ceiling := jobStageProgressBand(stage)
		if floor < lastCeiling {
			t.Fatalf("stage %s floor %d moved behind the previous ceiling %d", stage, floor, lastCeiling)
		}
		if ceiling < floor {
			t.Fatalf("stage %s band %d-%d is inverted", stage, floor, ceiling)
		}
		lastCeiling = ceiling
	}
}

func TestJobIDForRunResolvesBoundJob(t *testing.T) {
	r, _ := newProgressTestRunner(t)
	if got := r.jobIDForRun("run-1"); got != "job-1" {
		t.Fatalf("jobIDForRun(run-1) = %q, want job-1", got)
	}
	if got := r.jobIDForRun("missing"); got != "" {
		t.Fatalf("jobIDForRun(missing) = %q, want empty", got)
	}
}

// TestWriteJobProgressKeepsMonotonePercentAndRefreshesDetail pins both halves
// of the contract: a late TTS write cannot lower the aggregate percentage,
// but its stage detail still replaces the stale render detail.
func TestWriteJobProgressKeepsMonotonePercentAndRefreshesDetail(t *testing.T) {
	r, _ := newProgressTestRunner(t)
	sink := &recordingProgressSink{}
	r.jobProgressSink = sink
	r.log = zap.NewNop()

	r.writeJobProgress(context.Background(), "job-1", job.StageRender, 80, "render", map[string]any{"stage": "render"})
	r.writeJobProgress(context.Background(), "job-1", job.StageRender, 72, "map_render heartbeat pid=42 elapsed=15s", map[string]any{"stage": "map_render", "phase": "heartbeat", "pid": 42})

	if len(sink.data) != 2 || sink.data[0] != 80 || sink.data[1] != 80 {
		t.Fatalf("progress writes = %v, want two writes at monotone 80%%", sink.data)
	}
	if sink.last["stage"] != "map_render" || sink.last["phase"] != "heartbeat" || sink.last["pid"] != 42 {
		t.Fatalf("latest activity = %v, want map renderer heartbeat details", sink.last)
	}
}

func TestMapProgressProjectionOnlyIncludesExitCodeOnExit(t *testing.T) {
	r := NewRunner(newInMemRunRepository(), newStubTextGenerator(nil), newStubTranslator(), &entityTimelineVoiceoverGenerator{})
	r.SetJobStageSink(&recordingJobStageSink{})
	sink := &recordingProgressSink{}
	r.SetJobProgressSink(sink)
	r.bindJobStage("run-map-progress", "job-map-progress")
	ctx := r.withMapRenderProgress(context.Background(), "run-map-progress")

	renderqueue.ReportMapRenderProgress(ctx, renderqueue.MapRenderProgress{Phase: "start", PID: 42})
	if _, ok := sink.last["exit_code"]; ok {
		t.Fatalf("start event must not claim an exit code: %#v", sink.last)
	}
	renderqueue.ReportMapRenderProgress(ctx, renderqueue.MapRenderProgress{
		Phase: "exit", PID: 42, Result: "ok", ExitCode: 0,
	})
	if sink.last["result"] != "ok" || sink.last["exit_code"] != 0 {
		t.Fatalf("exit event = %#v, want result=ok and exit_code=0", sink.last)
	}
}

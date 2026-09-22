package adapters

import (
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// TestStageForProcessorStagesAreCanonical pins that the producer mapping never
// invents a stage outside the canonical workflow list: a processor that reports
// a stage the parent does not know would be progress nobody can order or read.
func TestStageForProcessorStagesAreCanonical(t *testing.T) {
	canonical := make(map[job.StageName]struct{}, len(job.CanonicalStageOrder()))
	for _, stage := range job.CanonicalStageOrder() {
		canonical[stage] = struct{}{}
	}
	for _, name := range CanonicalProcessorNames() {
		stage := stageForProcessor(name)
		if stage == "" {
			continue
		}
		if _, ok := canonical[stage]; !ok {
			t.Errorf("processor %q reports stage %q, which is not in job.CanonicalStageOrder()", name, stage)
		}
	}
}

// TestStageForProcessorHasOneProducerPerStage pins the single-producer rule:
// two processors mapped onto one stage would upsert the same observation and the
// later one would overwrite the earlier one's failure.
func TestStageForProcessorHasOneProducerPerStage(t *testing.T) {
	producers := make(map[job.StageName]ProcessorName)
	for _, name := range CanonicalProcessorNames() {
		stage := stageForProcessor(name)
		if stage == "" {
			continue
		}
		if other, dup := producers[stage]; dup {
			t.Errorf("stage %q is produced by both %q and %q", stage, other, name)
		}
		producers[stage] = name
	}
}

// TestStageForProcessorCoversTheWorkStages pins the stages the child pipeline
// actually performs and must therefore report. `render` is deliberately absent:
// the child item pipeline does not render video (the render lanes are
// clip.render and the durable Runner's localized render fan-out), so declaring
// it here would advertise progress with no producer.
func TestStageForProcessorCoversTheWorkStages(t *testing.T) {
	want := map[job.StageName]ProcessorName{
		job.StageClips:       ProcessorClipBindings,
		job.StageStock:       ProcessorStockBindings,
		job.StageTranslation: ProcessorTranslation,
		job.StageVoiceover:   ProcessorVoiceover,
		job.StageOverlay:     ProcessorVisualSlots,
		job.StageUpload:      ProcessorDocument,
		job.StagePersistence: ProcessorPersistence,
	}
	for stage, producer := range want {
		if got := stageForProcessor(producer); got != stage {
			t.Errorf("stageForProcessor(%q) = %q, want %q", producer, got, stage)
		}
	}
	// No processor may claim a stage outside this set: the remaining canonical
	// stages are script (the item itself) and render (owned by the render
	// lanes), neither of which a postprocessor performs.
	claimed := make(map[job.StageName]struct{}, len(want))
	for stage := range want {
		claimed[stage] = struct{}{}
	}
	for _, name := range CanonicalProcessorNames() {
		stage := stageForProcessor(name)
		if stage == "" {
			continue
		}
		if _, ok := claimed[stage]; !ok {
			t.Errorf("processor %q reports stage %q, which this test does not expect to be claimed", name, stage)
		}
	}
}

// TestRecordProcessorProgressReportsWorkStages pins end to end that the real
// producers land under their stage in the parent-facing map, so the parent's
// aggregate shows clips/stock/overlay instead of only script and persistence.
func TestRecordProcessorProgressReportsWorkStages(t *testing.T) {
	result := &PipelineResult{}
	cases := []struct {
		processor ProcessorName
		stage     job.StageName
	}{
		{ProcessorClipBindings, job.StageClips},
		{ProcessorStockBindings, job.StageStock},
		{ProcessorVisualSlots, job.StageOverlay},
	}
	for _, tc := range cases {
		recordProcessorProgress(result, tc.processor, nil, ProcessInput{EffectiveLanguage: "it"}, job.StageCompleted, "job-1", "")
		progress, ok := result.StageProgress[string(tc.stage)]
		if !ok {
			t.Fatalf("processor %q did not report stage %q", tc.processor, tc.stage)
		}
		if progress.Stage != tc.stage || progress.Total != 1 || progress.Completed != 1 {
			t.Errorf("stage %q progress = %+v, want one completed observation", tc.stage, progress)
		}
	}
}

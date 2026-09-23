package videocreate

// ── Progress projection (the §20 contract) ────────────────────────────
//
// Every stage reports its band so the remote Calendar can render a
// continuous bar without knowing anything about the internals:
//
//	SCRIPTING       5-20    MEDIA_SEARCH    20-30
//	MEDIA_ACQUIRE   30-45   VOICEOVER       45-55 (incl. audio master
//	and overlay plan)       RENDERING       55-85
//	ASSEMBLING      85-92   AUDIO_FINALIZE  92-96
//	VERIFYING       96-99   FINALIZING      99-100
//
// The band table lives with the step ladder (model.go::WorkflowSteps),
// so a step can never be added without its progress band.

// reportProgress emits (percent, message) through the run's progress
// channel. It is nil-safe: a test run without broker tools still
// exercises every stage.
func (r *Run) reportProgress(percent int, message string) {
	if r.Progress == nil {
		return
	}
	r.Progress(percent, message)
}

// stageStarted reports the step's band-start with the canonical
// operator-facing message.
func (r *Run) stageStarted(spec StepSpec) {
	r.reportProgress(spec.BandStart, string(spec.Stage)+": "+spec.Title)
}

// stageSucceeded reports the step's band-end.
func (r *Run) stageSucceeded(spec StepSpec) {
	r.reportProgress(spec.BandEnd, string(spec.Stage)+": done")
}

// stageSkipped reports an optional step that the payload made
// unnecessary. It still moves the bar (a silent gap reads as a hang).
func (r *Run) stageSkipped(spec StepSpec) {
	r.reportProgress(spec.BandEnd, string(spec.Stage)+": skipped")
}

// stageFailed reports a step failure before the job turns FAILED.
func (r *Run) stageFailed(spec StepSpec, reason string) {
	r.reportProgress(spec.BandStart, string(spec.Stage)+": FAILED: "+reason)
}

// ProgressBand returns the (start, end) band of a step key (used by
// tests and by the remote progress projection).
func ProgressBand(stepKey string) (int, int, bool) {
	spec, ok := StepByKey(stepKey)
	if !ok {
		return 0, 0, false
	}
	return spec.BandStart, spec.BandEnd, true
}

// CurrentStageProgress maps a current stage to the band mid-point so a
// resumed job can re-publish a truthful bar immediately after restart.
func CurrentStageProgress(state WorkflowState) int {
	for _, spec := range WorkflowSteps {
		rec := state.Stages[spec.StepKey]
		if rec == nil || (rec.Status != StageSucceeded && rec.Status != StageSkipped) {
			return spec.BandStart
		}
	}
	return 100
}

// Package voiceover — Stage 0 preparation and terminal failure helpers
// (BON-08: Execute decomposition).
//
// prepareSegment owns the input/destination gate and the initial result
// envelope. failSegment owns the shared terminal-failure envelope so
// every stage reports the same code, message, progress and metric.
package voiceover

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
)

// prepareSegment validates the command and builds the result envelope.
// A nil command returns (nil, error); a rejected destination returns the
// failed envelope plus its typed PipelineError.
func (u *ProcessSegmentUseCase) prepareSegment(cmd *ProcessSegmentCommand) (*VoiceoverItemResult, error) {
	if cmd == nil {
		return nil, fmt.Errorf("ProcessSegmentUseCase.Execute: nil input")
	}
	// The destination always carries the LOCAL output folder. The Drive
	// FolderID is required only for delivery runs: a publisher-less
	// (local-only) run has nothing to upload.
	if cmd.Dest == nil || (u.deps.Publisher != nil && cmd.Dest.FolderID == "") {
		observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
		out := &VoiceoverItemResult{
			Language:  cmd.Language,
			Status:    StatusFailed,
			ErrorCode: string(FailureMissingFolder),
			Error:     "missing_folder_id: voiceover destination has no FolderID for upload",
		}
		setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
		return out, newPipelineErrorCode(StageDestinationResolve, false, FailureMissingFolder, fmt.Errorf("%s", out.Error))
	}
	return &VoiceoverItemResult{
		Language: cmd.Language,
		Voice:    cmd.Voice,
		Filename: cmd.Filename,
		ID:       cmd.ID,
		Status:   StatusFailed,
	}, nil
}

// failSegment marks a terminal stage failure and returns the typed error
// the caller must propagate. Every call represents the end of this run,
// so the failed metric is incremented once here.
func failSegment(out *VoiceoverItemResult, cmd *ProcessSegmentCommand, stage Stage, retryable bool, code FailureCode, cause error) *PipelineError {
	observability.VoiceoverJobsTotal.WithLabelValues("failed").Inc()
	out.ErrorCode = string(code)
	out.Error = fmt.Sprintf("%s: %v", code, cause)
	setFinalStageProgress(out, string(cmd.Language), cmd.JobID)
	return newPipelineErrorCode(stage, retryable, code, cause)
}

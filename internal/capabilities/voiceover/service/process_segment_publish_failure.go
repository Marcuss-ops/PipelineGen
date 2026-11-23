// Package voiceover — Stage 3 failure classification
// (BON-08: Execute decomposition).
//
// publishStage returns typed PipelineErrors for its own failures and
// raw adapter errors for the Publisher. classifyPublishFailure maps both
// shapes to the canonical result envelope so Execute does not carry the
// branch cascade inline. The mapping is pure and table-testable.
package voiceover

import (
	"context"
	"errors"
	"fmt"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/observability"
	"go.uber.org/zap"
)

// publishFailureDecision is the canonical Stage 3 failure envelope.
// Pipeline is set when the error is already typed; the caller must
// propagate it unchanged. Otherwise the caller builds a PipelineError
// from Stage, Retryable and FailureCode.
type publishFailureDecision struct {
	Pipeline           *PipelineError
	Code               string
	Message            string
	Stage              Stage
	Retryable          bool
	FailureCode        FailureCode
	DriveUploadFailure bool
}

// publishAndClassify runs Stage 3 and folds any failure into the result
// envelope. It returns the publish output on success and the typed
// PipelineError on failure; the caller owns the terminal metric and
// progress update.
func (u *ProcessSegmentUseCase) publishAndClassify(
	ctx context.Context,
	cmd *ProcessSegmentCommand,
	out *VoiceoverItemResult,
	ttsOut *TTSOutput,
	post *AudioPostOutput,
	log *zap.Logger,
) (*publishStageResult, error) {
	var pub *publishStageResult
	err := kernobs.MeasureStage(ctx, canonicalStagePublish, func(stageCtx context.Context) error {
		var pubErr error
		pub, pubErr = u.publishStage(stageCtx, cmd, out, ttsOut, post, log)
		return pubErr
	})
	if err == nil {
		return pub, nil
	}

	decision := classifyPublishFailure(err)
	if decision.DriveUploadFailure {
		observability.DriveUploadFailuresTotal.Inc()
	}
	out.ErrorCode = decision.Code
	out.Error = decision.Message
	if decision.Pipeline != nil {
		return nil, err
	}
	return nil, newPipelineErrorCode(decision.Stage, decision.Retryable, decision.FailureCode, err)
}

// classifyPublishFailure maps a publish-stage error to the result code,
// operator message and typed-error fields. A destination-parent
// mismatch is permanent; every other raw publisher failure is treated
// as a retryable upload failure.
func classifyPublishFailure(err error) publishFailureDecision {
	var pipelineErr *PipelineError
	if errors.As(err, &pipelineErr) {
		return publishFailureDecision{
			Pipeline: pipelineErr,
			Code:     string(pipelineErr.FailureCode()),
			Message:  pipelineErr.Error(),
		}
	}
	if errors.Is(err, delivery.ErrDestinationParentMismatch) {
		return publishFailureDecision{
			Code:               VoiceoverDestinationMismatchCode,
			Message:            fmt.Sprintf("%s: %v", VoiceoverDestinationMismatchCode, err),
			Stage:              StageUpload,
			Retryable:          false,
			FailureCode:        FailureDestinationMismatch,
			DriveUploadFailure: true,
		}
	}
	return publishFailureDecision{
		Code:               string(FailureUpload),
		Message:            fmt.Sprintf("%s: %v", FailureUpload, err),
		Stage:              StageUpload,
		Retryable:          true,
		FailureCode:        FailureUpload,
		DriveUploadFailure: true,
	}
}

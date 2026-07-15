// Package texttracks — jobs.go: canonical broker-facing handler for
// `asset.text.materialize` jobs.
//
// PR-PY-CLIPS-CORRETTE-TRADOTTE Fase 3 (July 2026).
package texttracks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"go.uber.org/zap"
)

type MaterializeJobPayload struct {
	AssetID         string   `json:"asset_id"`
	SourceLanguage  string   `json:"source_language"`
	SourceTextHash  string   `json:"source_text_hash"`
	TargetLanguages []string `json:"target_languages,omitempty"`
	TextKinds       []string `json:"text_kinds"`
}

type MaterializeJobHandler struct {
	materializer *Materializer
	log          *zap.Logger
}

func NewMaterializeJobHandler(
	materializer *Materializer,
	log *zap.Logger,
) *MaterializeJobHandler {
	if materializer == nil {
		panic("texttracks.NewMaterializeJobHandler: materializer is nil")
	}
	if log == nil {
		panic("texttracks.NewMaterializeJobHandler: log is nil")
	}
	return &MaterializeJobHandler{materializer: materializer, log: log}
}

func (h *MaterializeJobHandler) Register(jobsSvc *appjobs.Service) error {
	if jobsSvc == nil {
		return errors.New("texttracks.MaterializeJobHandler.Register: jobsSvc is nil")
	}
	return jobsSvc.RegisterHandler(job.TypeAssetTextMaterialize, appjobs.HandlerFunc(h.HandleJob))
}

func (h *MaterializeJobHandler) HandleJob(
	ctx context.Context,
	j *appjobs.Job,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	pf := appjobs.SafeProgressFn(tools)
	pf(0, "starting asset.text.materialize")
	defer pf(100, "asset.text.materialize done")

	var cmd MaterializeJobPayload
	if err := json.Unmarshal(j.Payload, &cmd); err != nil {
		return nil, fmt.Errorf("texttracks.materialize: payload decode: %w", err)
	}
	h.log.Info("texttracks.materialize.job.start",
		zap.String("job_id", j.ID),
		zap.String("asset_id", cmd.AssetID),
		zap.String("source_language", cmd.SourceLanguage),
		zap.Int("kind_count", len(cmd.TextKinds)),
		zap.Int("target_lang_count", len(cmd.TargetLanguages)),
	)

	if err := h.validatePayload(&cmd); err != nil {
		return nil, h.classifyError(fmt.Errorf("texttracks.materialize: payload invalid: %w", err))
	}

	reports := make(map[string]*MaterializationReport, len(cmd.TextKinds))
	totalDuration := time.Duration(0)

	progressPer := 100 / (len(cmd.TextKinds) + 1)
	pf(progressPer, fmt.Sprintf("fan-out to %d kind(s)", len(cmd.TextKinds)))

	for i, kindStr := range cmd.TextKinds {
		kind := asset.TextTrackKind(kindStr)
		if !isKnownTextTrackKind(kind) {
			return nil, h.classifyError(&ErrInvalidMaterializeRequest{
				Field:  "text_kinds",
				Reason: fmt.Sprintf("unknown text_kind %q at index %d", kindStr, i),
			})
		}

		// Thread the payload-level target_languages override
		// into the per-call Materialize invocation. Empty
		// means "use the materializer's configured set".
		rep, err := h.materializer.Materialize(
			ctx, cmd.AssetID, cmd.SourceLanguage, cmd.SourceTextHash, kind, cmd.TargetLanguages,
		)
		if err != nil {
			classified := h.classifyError(err)
			if rep != nil {
				reports[kindStr] = rep
			}
			return map[string]any{
				"asset_id":    cmd.AssetID,
				"reports":     reports,
				"failed_kind": kindStr,
			}, classified
		}
		reports[kindStr] = rep
		totalDuration += rep.Duration

		pf(progressPer*(i+2), fmt.Sprintf("materialized %d/%d kinds", i+1, len(cmd.TextKinds)))
	}

	result := map[string]any{
		"asset_id":               cmd.AssetID,
		"source_language":        cmd.SourceLanguage,
		"kind_count":             len(cmd.TextKinds),
		"reports":                reports,
		"total_duration_ms":      totalDuration.Milliseconds(),
		"languages_materialized": aggregateLanguages(reports, true),
		"languages_skipped":      aggregateLanguages(reports, false),
		"languages_failed":       aggregateFailedLanguages(reports),
	}

	h.log.Info("texttracks.materialize.job.done",
		zap.String("job_id", j.ID),
		zap.String("asset_id", cmd.AssetID),
		zap.Int("kind_count", len(cmd.TextKinds)),
		zap.Int("languages_materialized", len(result["languages_materialized"].([]string))),
		zap.Int("languages_skipped", len(result["languages_skipped"].([]string))),
		zap.Int("languages_failed", len(result["languages_failed"].(map[string]string))),
		zap.Int64("total_duration_ms", result["total_duration_ms"].(int64)),
	)
	return result, nil
}

func (h *MaterializeJobHandler) validatePayload(cmd *MaterializeJobPayload) error {
	if cmd.AssetID == "" {
		return &ErrInvalidMaterializeRequest{Field: "asset_id", Reason: "asset_id is required"}
	}
	if cmd.SourceLanguage == "" {
		return &ErrInvalidMaterializeRequest{Field: "source_language", Reason: "source_language is required"}
	}
	if cmd.SourceTextHash == "" {
		return &ErrInvalidMaterializeRequest{
			Field:  "source_text_hash",
			Reason: "source_text_hash is required (caller pre-computes SHA-256 of the source text)",
		}
	}
	if len(cmd.TextKinds) == 0 {
		return &ErrInvalidMaterializeRequest{
			Field:  "text_kinds",
			Reason: "text_kinds must contain at least one kind",
		}
	}
	return nil
}

// classifyError maps typed sentinels to TERMINAL vs RETRYABLE.
//
// TERMINAL: ErrInvalidMaterializeRequest, ErrNoSourceTrack,
// ErrTrackNotReady, ErrUnsupportedLanguage, ErrTranslationFailed
// (deterministic translation failure — retrying with the same
// input does NOT help).
//
// RETRYABLE (broker default policy): repository errors, outbox
// emission errors. A transient translation failure (HTTP 5xx,
// timeout) does NOT surface as ErrTranslationFailed — the
// underlying error is returned unwrapped, and the broker's
// default retry policy decides.
func (h *MaterializeJobHandler) classifyError(err error) error {
	switch {
	case errors.Is(err, &ErrInvalidMaterializeRequest{}):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, &ErrNoSourceTrack{}):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, &ErrTrackNotReady{}):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, &ErrUnsupportedLanguage{}):
		return fmt.Errorf("terminal: %w", err)
	case errors.Is(err, &ErrTranslationFailed{}):
		return fmt.Errorf("terminal: %w", err)
	default:
		return err
	}
}

func isKnownTextTrackKind(k asset.TextTrackKind) bool {
	switch k {
	case asset.TextTrackTranscript,
		asset.TextTrackDescription,
		asset.TextTrackSummary,
		asset.TextTrackTitle,
		asset.TextTrackKeywords:
		return true
	}
	return false
}

func aggregateLanguages(reports map[string]*MaterializationReport, materialized bool) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, rep := range reports {
		if rep == nil {
			continue
		}
		var list []string
		if materialized {
			list = append(list, rep.CreatedLanguages...)
			list = append(list, rep.RetranslatedLanguages...)
		} else {
			list = append(list, rep.SkippedLanguages...)
		}
		for _, lang := range list {
			if !seen[lang] {
				seen[lang] = true
				out = append(out, lang)
			}
		}
	}
	return out
}

func aggregateFailedLanguages(reports map[string]*MaterializationReport) map[string]string {
	out := map[string]string{}
	for _, rep := range reports {
		if rep == nil {
			continue
		}
		for lang, msg := range rep.FailedLanguages {
			out[lang] = msg
		}
	}
	return out
}

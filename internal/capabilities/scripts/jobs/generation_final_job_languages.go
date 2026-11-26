package jobs

import (
	"context"
	"fmt"
	"strings"
	"time"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	processor "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/adapters/processor"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// executeFinalJobLanguageRuns turns one multi-language final_job request into
// ordered, independent durable runs. Each child has a singleton translation
// target and TTS language; the shared project/media cache still allows
// identity assets (for example a Mike Tyson portrait) to be reused.
func (h *GenerateJobHandler) executeFinalJobLanguageRuns(
	ctx context.Context,
	j *job.Job,
	parent *scriptgen.GenerationRun,
	tools *appjobs.JobTools,
) (map[string]any, error) {
	if h == nil || h.durableRunner == nil || h.runRepo == nil || parent == nil || j == nil {
		return nil, fmt.Errorf("multilingual final job: durable runner, run repository, parent run, and job are required")
	}
	languages := uniqueTargetLanguages(parent.Request.Languages)
	if len(languages) < 2 {
		return nil, fmt.Errorf("multilingual final job: expected at least two distinct target languages")
	}
	progress := appjobs.SafeProgressFn(tools)
	event := appjobs.SafeEventFn(tools)
	results := make([]scriptgen.LanguageDeliveryResult, 0, len(languages))
	artifacts := make([]job.Artifact, 0, len(languages)*3)

	for index, language := range languages {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("multilingual final job cancelled before %s: %w", language, err)
		}
		childID := finalJobLanguageRunID(parent.ID, language)
		request := finalJobLanguageRequest(parent.Request, language)
		child, err := h.getOrCreateLanguageRun(ctx, childID, request)
		if err != nil {
			return h.failLanguageParent(ctx, parent.ID, language, err)
		}

		if child.Status == scriptgen.RunStatusFailed && child.NextRetryAt == nil && child.AttemptCount >= scriptgen.MaxRetries {
			return h.failLanguageParent(ctx, parent.ID, language,
				fmt.Errorf("language run %s exhausted its retry budget: %s", childID, child.ErrorMessage))
		}
		if child.Status != scriptgen.RunStatusCompleted {
			event("stage_progress", "Language video started", map[string]any{
				"stage": string(scriptgen.StageGeneratingSceneText), "language": language,
				"status": string(job.StageRunning), "run_id": childID, "index": index + 1,
				"total": len(languages),
			})
			progress(int(float64(index)/float64(len(languages))*95), "generating video for "+language)
			execution := scriptgen.ExecutionContext{
				RootJobID: childID, JobID: j.ID, ProjectID: request.Project,
				VideoID: request.OutputName, CorrelationID: j.CorrelationID,
			}
			h.durableRunner.ExecuteWithContext(ctx, childID, request, execution)
			child, err = h.readLanguageRunOutcome(ctx, childID)
			if err != nil {
				return h.failLanguageParent(ctx, parent.ID, language, err)
			}
		}
		if child == nil || child.Status != scriptgen.RunStatusCompleted || child.Result == nil {
			if child != nil && (child.NextRetryAt != nil || child.Status == scriptgen.RunStatusRunning) {
				return nil, incompleteRunError(child)
			}
			err := fmt.Errorf("language %s run %s did not complete (status=%s, stage=%s, error=%s)",
				language, childID, child.Status, child.CurrentStage, child.ErrorMessage)
			return h.failLanguageParent(ctx, parent.ID, language, err)
		}

		remote := child.Result.RemoteFinalJob
		if remote == nil || !strings.EqualFold(strings.TrimSpace(remote.Status), "SUCCEEDED") {
			err := fmt.Errorf("language %s run completed without a successful final video receipt", language)
			return h.failLanguageParent(ctx, parent.ID, language, err)
		}
		domainResult := scriptgen.DurableResultToDomain(child.Result)
		languageArtifacts, persistErr := processor.PersistGeneratedArtifacts(ctx, j.ID+"-"+language, domainResult, h.log)
		if persistErr != nil {
			return h.failLanguageParent(ctx, parent.ID, language, fmt.Errorf("persist language artifacts: %w", persistErr))
		}
		artifacts = append(artifacts, languageArtifacts...)
		results = append(results, scriptgen.LanguageDeliveryResult{
			Language: language, RunID: childID, Status: string(child.Status),
			ScriptText: child.Result.Output.Text, RemoteJobID: remote.JobID,
			ArtifactURL: remote.ArtifactURL, ArtifactSHA: remote.SHA256,
		})
		partial := &scriptgen.GenerateResult{LanguageDeliveries: results}
		if err := h.runRepo.SavePartialResult(ctx, parent.ID, partial); err != nil {
			return h.failLanguageParent(ctx, parent.ID, language, fmt.Errorf("save language progress: %w", err))
		}
		event("stage_progress", "Language video completed", map[string]any{
			"stage": string(scriptgen.StageCompleted), "language": language,
			"status": string(job.StageCompleted), "run_id": childID,
		})
	}

	manifest := buildManifestFromArtifacts(j.ID, artifacts)
	if err := manifest.Validate(); err != nil {
		return h.failLanguageParent(ctx, parent.ID, "", fmt.Errorf("combined artifact manifest: %w", err))
	}
	if err := h.runRepo.UpdateStage(ctx, parent.ID, scriptgen.RunStatusCompleted, scriptgen.StageCompleted); err != nil {
		return nil, fmt.Errorf("multilingual final job: mark parent run complete: %w", err)
	}
	progress(100, "all language videos completed")
	return map[string]any{
		"run_id": parent.ID, "parent_state": "completed", "languages": results,
		job.ManifestKey: manifest,
	}, nil
}

func uniqueTargetLanguages(in []scriptgen.Language) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, value := range in {
		language := strings.ToLower(strings.TrimSpace(string(value)))
		if language == "" {
			continue
		}
		if _, ok := seen[language]; ok {
			continue
		}
		seen[language] = struct{}{}
		out = append(out, language)
	}
	return out
}

func finalJobLanguageRunID(parentID, language string) string {
	return strings.TrimSpace(parentID) + "-language-" + strings.ToLower(strings.TrimSpace(language))
}

func finalJobLanguageRequest(parent scriptgen.GenerateRequest, language string) scriptgen.GenerateRequest {
	child := parent
	child.Languages = []scriptgen.Language{scriptgen.Language(language)}
	child.VoiceoverLanguages = []scriptgen.Language{scriptgen.Language(language)}
	child.FinalJob = true
	// The parent delivery reads each child's remote receipt, so a language
	// child is the one case that must not be downgraded to the local join.
	child.FinalJobRemote = true
	// A language child is a fresh video run, but the input source is already
	// supplied and entity extraction is explicitly forced by MediaPlan. Do not
	// let the envelope-wide force-refresh bit defeat shared identity-asset cache
	// reads between siblings.
	child.ForceRefresh = false
	child.Source.ForceRefresh = false
	child.ScriptParams.ForceRefresh = false
	child.MediaPlan.ForceRefreshAssets = false
	child.MediaPlan.ForceRefreshBindings = false
	child.MediaPlan.ForceRefreshExtraction = true
	// Reuse identity-level media assets between sibling runs while each run
	// still rebuilds its own translated overlays, timeline, TTS, and final MP4.
	child.MediaPlan.Cache.Read = true
	child.MediaPlan.Cache.Write = true
	child.IdempotencyKey = strings.TrimSpace(parent.IdempotencyKey) + ":language:" + language
	if strings.TrimSpace(child.OutputName) != "" {
		child.OutputName += "-" + language
	}
	baseFolder := strings.TrimSpace(child.Render.DriveSubfolderName)
	if baseFolder == "" {
		baseFolder = strings.TrimSpace(child.OutputName)
	}
	if baseFolder != "" {
		child.Render.DriveSubfolderName = baseFolder + "-" + language
	}
	return child
}

func (h *GenerateJobHandler) getOrCreateLanguageRun(ctx context.Context, id string, request scriptgen.GenerateRequest) (*scriptgen.GenerationRun, error) {
	run, err := h.runRepo.Get(ctx, id)
	if err != nil || run != nil {
		return run, err
	}
	run = &scriptgen.GenerationRun{
		ID: id, Request: request, Status: scriptgen.RunStatusPending,
		CurrentStage: scriptgen.StageNormalizing, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := h.runRepo.Create(ctx, run); err != nil {
		// A lease replay can race the first insert. Read the winner before
		// surfacing the insert error; this keeps the language run idempotent.
		if existing, getErr := h.runRepo.Get(ctx, id); getErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return run, nil
}

func (h *GenerateJobHandler) readLanguageRunOutcome(ctx context.Context, id string) (*scriptgen.GenerationRun, error) {
	run, err := h.runRepo.Get(ctx, id)
	if err == nil {
		return run, nil
	}
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return h.runRepo.Get(detached, id)
}

func (h *GenerateJobHandler) failLanguageParent(ctx context.Context, parentID, language string, cause error) (map[string]any, error) {
	if h.runRepo != nil {
		_ = h.runRepo.FailRun(ctx, scriptgen.FailRunInput{
			RunID: parentID, FailedStage: scriptgen.StageFailed,
			ErrorCode:    "MULTILINGUAL_FINAL_JOB_FAILED",
			ErrorMessage: fmt.Sprintf("language %s: %v", language, cause),
		})
	}
	return nil, cause
}

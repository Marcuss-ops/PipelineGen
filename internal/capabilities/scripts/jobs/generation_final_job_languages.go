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
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
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
	// The first completed child owns the one master script generation. Every
	// later language reuses its scene text and translates that exact source,
	// keeping scene identities, phrase anchors and image bindings stable.
	languages = sourceLanguageFirst(languages, parent.Request.SourceLanguage)
	var masterResult *scriptgen.GenerateResult

	for index, language := range languages {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("multilingual final job cancelled before %s: %w", language, err)
		}
		childID := finalJobLanguageRunID(parent.ID, language)
		request := finalJobLanguageRequest(parent.Request, language)
		if masterResult != nil {
			if err := seedFinalJobLanguageRequest(&request, masterResult); err != nil {
				return h.failLanguageParent(ctx, parent.ID, language, err)
			}
		}
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
		if masterResult == nil {
			masterResult = child.Result
			if _, err := masterSceneSourceText(masterResult, parent.Request); err != nil {
				return h.failLanguageParent(ctx, parent.ID, language, err)
			}
		}

		remote := child.Result.RemoteFinalJob
		if remote == nil || !strings.EqualFold(strings.TrimSpace(remote.Status), "SUCCEEDED") {
			err := fmt.Errorf("language %s run completed without a successful final video receipt", language)
			return h.failLanguageParent(ctx, parent.ID, language, err)
		}
		domainResult := scriptgen.DurableResultToDomainForLanguage(child.Result, language)
		if strings.TrimSpace(child.Result.Output.Text) != "" && strings.TrimSpace(domainResult.Output.Text) == "" {
			return h.failLanguageParent(ctx, parent.ID, language,
				fmt.Errorf("language %s run %s has no complete scene-text projection", language, childID))
		}
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

func sourceLanguageFirst(languages []string, source scriptgen.Language) []string {
	want := strings.ToLower(strings.TrimSpace(string(source)))
	if want == "" {
		return languages
	}
	for index, language := range languages {
		if strings.EqualFold(strings.TrimSpace(language), want) {
			if index == 0 {
				return languages
			}
			out := append([]string(nil), languages...)
			copy(out[1:index+1], out[0:index])
			out[0] = language
			return out
		}
	}
	return languages
}

func masterSceneSourceText(result *scriptgen.GenerateResult, parent scriptgen.GenerateRequest) (map[string]string, error) {
	if result == nil {
		return nil, fmt.Errorf("multilingual final job: master script result is missing")
	}
	sourceLanguage := strings.ToLower(strings.TrimSpace(string(parent.SourceLanguage)))
	if sourceLanguage == "" {
		return nil, fmt.Errorf("multilingual final job: source language is required to lock the master script")
	}
	byID := make(map[string]string, len(result.Scenes))
	for _, scene := range result.Scenes {
		if text := strings.TrimSpace(scene.Text[scriptgen.Language(sourceLanguage)]); text != "" {
			byID[strings.TrimSpace(scene.ID)] = text
		}
	}
	segments := parent.ScriptParams.Segments
	if len(segments) == 0 {
		return nil, fmt.Errorf("multilingual final job: explicit script segments are required to reuse master scene text")
	}
	texts := make(map[string]string, len(segments))
	for index, segment := range segments {
		id := strings.TrimSpace(segment.ID)
		text := byID[id]
		if text == "" && index < len(result.Scenes) && (id == "" || strings.TrimSpace(result.Scenes[index].ID) == id) {
			text = strings.TrimSpace(result.Scenes[index].Text[scriptgen.Language(sourceLanguage)])
		}
		if text == "" {
			return nil, fmt.Errorf("multilingual final job: master script has no %s text for segment %q", sourceLanguage, id)
		}
		if id == "" {
			return nil, fmt.Errorf("multilingual final job: segment %d has no stable ID for master-text reuse", index)
		}
		texts[id] = text
	}
	return texts, nil
}

func seedFinalJobLanguageRequest(request *scriptgen.GenerateRequest, master *scriptgen.GenerateResult) error {
	if request == nil {
		return fmt.Errorf("multilingual final job: target request is missing")
	}
	texts, err := masterSceneSourceText(master, *request)
	if err != nil {
		return err
	}
	segments := append([]scriptpkg.ScriptSegment(nil), request.ScriptParams.Segments...)
	orderedText := make([]string, 0, len(segments))
	for index := range segments {
		id := strings.TrimSpace(segments[index].ID)
		text := strings.TrimSpace(texts[id])
		if text == "" {
			return fmt.Errorf("multilingual final job: master script has no source text for segment %q", id)
		}
		segments[index].SourceText = text
		orderedText = append(orderedText, text)
	}
	request.ScriptParams.Segments = segments
	request.ScriptParams.SourceTextVerbatim = true
	request.Source.SourceText = strings.Join(orderedText, "\n\n")
	// The source-language run already produced this extraction. Reuse its
	// deterministic cache entries and the same explicit overlay image pins.
	request.MediaPlan.ForceRefreshExtraction = false
	return nil
}

func finalJobLanguageRunID(parentID, language string) string {
	return strings.TrimSpace(parentID) + "-language-" + strings.ToLower(strings.TrimSpace(language))
}

func finalJobLanguageRequest(parent scriptgen.GenerateRequest, language string) scriptgen.GenerateRequest {
	child := parent
	if strings.EqualFold(strings.TrimSpace(language), strings.TrimSpace(string(parent.SourceLanguage))) {
		// The source-language delivery is the master generation itself; asking
		// the translator to translate en -> en is rejected by its contract.
		child.Languages = nil
		child.TranslationProvider = ""
		child.TranslationModel = ""
	} else {
		child.Languages = []scriptgen.Language{scriptgen.Language(language)}
	}
	child.VoiceoverLanguages = []scriptgen.Language{scriptgen.Language(language)}
	// The parent delivery reads each child's remote receipt, so a language
	// child is always a final job: final_job=true is the one flag that keeps
	// the complete video Master-owned and never downgrades to the local join.
	child.FinalJob = true
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

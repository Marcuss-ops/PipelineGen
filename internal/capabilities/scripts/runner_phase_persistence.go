package scriptgeneration

import (
	"context"
	"fmt"

	capabilityentities "github.com/Marcuss-ops/PipelineGen/internal/capabilities/entities"
	capabilityoverlay "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

// persistScript is the capability-owned persistence decision. Storage details
// stay behind ScriptPersistence; this phase only enforces the request flag,
// positive-ID contract, and checkpoint ordering.
func (r *Runner) persistScript(ctx context.Context, runID string, req GenerateRequest, exec ExecutionContext, resumeIdx int, result *GenerateResult) bool {
	step, startErr := r.startExecutionStep(ctx, exec, "PERSISTENCE", "storage")
	if startErr != nil {
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, startErr)
		return false
	}

	// Idempotent restart: a script already persisted in a prior attempt (even
	// one that crashed before the run reached COMPLETED) must never be written
	// again — the canonical row is reused, so a resume writes 0 duplicate DB
	// rows regardless of which stage it restarts from.
	alreadyPersisted := result != nil && result.ScriptID > 0
	if !req.SaveToDB || stageSkipped(resumeIdx, StagePublishingDocuments) || alreadyPersisted {
		if err := r.skipExecutionStep(ctx, exec, step); err != nil {
			r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
			return false
		}
		return true
	}
	if r.scriptPersistence == nil {
		cause := fmt.Errorf("script persistence requested but canonical persistence port is not configured")
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
		return false
	}

	scriptID, err := r.scriptPersistence.Persist(ctx, ScriptPersistenceInput{
		RunID: runID, Request: req, Result: result,
	})
	if err != nil {
		cause := fmt.Errorf("persist script: %w", err)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
		return false
	}
	if scriptID <= 0 {
		cause := fmt.Errorf("persist script returned invalid script_id=%d", scriptID)
		r.failExecutionStepBestEffort(ctx, exec, step, cause)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, cause)
		return false
	}
	result.ScriptID = scriptID
	r.checkpoint(ctx, runID, result)
	if err := r.completeExecutionStep(ctx, exec, step); err != nil {
		r.failExecutionStepBestEffort(ctx, exec, step, err)
		r.failRunWithRetry(ctx, runID, StagePublishingDocuments, err)
		return false
	}
	return true
}

// --- audio-compile diagnostics of the runner phase ---

// logLocationOverlayPlan preserves producer-boundary diagnostics for both the
// serial and overlapping paths without compiling the immutable plan twice.
func (r *Runner) logLocationOverlayPlan(req GenerateRequest, result *GenerateResult) {
	// Keep the location path observable at the producer boundary. A place
	// may be extracted and geocoded successfully yet still disappear before
	// the worker sees its immutable render plan; recording the two counts
	// makes that boundary auditable from the run log.
	geocodedPlaces := 0
	for i := range result.Scenes {
		if ann := annotationsForLanguage(result.Scenes[i], req.SourceLanguage, result.SourceLanguage); ann != nil {
			for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
				if isPlaceEntityType(entity.Type) && entity.Geo != nil {
					geocodedPlaces++
				}
			}
		}
	}
	mapItems := 0
	if result.OverlayPlan != nil {
		for _, item := range result.OverlayPlan.Items {
			if item.Kind == "map" {
				mapItems++
			}
		}
	}
	spokenPlaceOccurrences := 0
	if result.EntityTimeline != nil {
		for _, scene := range result.EntityTimeline.Scenes {
			for _, entity := range scene.Entities {
				if isPlaceEntityType(entity.Type) {
					spokenPlaceOccurrences++
				}
			}
		}
	}
	matchedMapCandidates := 0
	validMapCandidates := 0
	placeMatchDebug := make([]string, 0, geocodedPlaces)
	if result.EntityTimeline != nil {
		for sceneIndex := range result.Scenes {
			scene := result.Scenes[sceneIndex]
			ann := annotationsForLanguage(scene, req.SourceLanguage, req.SourceLanguage)
			if ann == nil {
				continue
			}
			var timelineScene *capabilityentities.SceneEntityTimeline
			for i := range result.EntityTimeline.Scenes {
				if result.EntityTimeline.Scenes[i].SceneID == scene.ID {
					timelineScene = &result.EntityTimeline.Scenes[i]
					break
				}
			}
			for _, entity := range append(append([]scriptpkg.AnnotatedEntity(nil), ann.PrimaryEntities...), ann.SecondaryEntities...) {
				if !isPlaceEntityType(entity.Type) || entity.Geo == nil {
					continue
				}
				matched := false
				if timelineScene != nil {
					matched = occurrenceFor(timelineScene.Entities, entity) != nil
				}
				if matched {
					matchedMapCandidates++
					occurrence := occurrenceFor(timelineScene.Entities, entity)
					if _, ok := capabilityoverlay.NewMapCandidate(occurrence.EntityID, entity.CanonicalName,
						entity.Geo.Latitude, entity.Geo.Longitude, occurrence.AudioStartUS,
						occurrence.AudioEndUS-occurrence.AudioStartUS, entity.Confidence, entity.Geo.Scope); ok {
						validMapCandidates++
					}
				}
				placeMatchDebug = append(placeMatchDebug, fmt.Sprintf("%s:%s/%s=%t", scene.ID, entity.Type, entity.CanonicalName, matched))
			}
		}
	}
	r.log.Info("location overlay plan compiled",
		zap.Int("geocoded_places", geocodedPlaces),
		zap.Int("spoken_place_occurrences", spokenPlaceOccurrences),
		zap.Int("matched_map_candidates", matchedMapCandidates),
		zap.Int("valid_map_candidates", validMapCandidates),
		zap.Strings("place_match_debug", placeMatchDebug),
		zap.Int("map_items", mapItems),
		zap.Bool("map_plate_resolver_wired", r.mapPlates != nil && r.shouldGeocodeScriptLocations(req)),
	)
}

// failAudioCompileStep records both the execution-step failure and the
// run-level retryable failure. All audio validation/rendering branches use the
// same fail-closed boundary, so it lives with the phase that owns the audio
// compile step instead of in a file of its own.
func (r *Runner) failAudioCompileStep(ctx context.Context, runID string, exec ExecutionContext, step ExecutionStep, cause error) bool {
	r.failExecutionStepBestEffort(ctx, exec, step, cause)
	r.failRunWithRetry(ctx, runID, StageCompilingAudio, cause)
	return false
}

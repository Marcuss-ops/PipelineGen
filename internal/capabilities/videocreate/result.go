package videocreate

import (
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── Result assembly (the §18/§19 contract) ───────────────────────────
//
// The result is built ONLY from durable facts (verified + published
// identities + the child ledger) — never from in-memory optimism. It is
// returned only when the workflow is about to turn SUCCEEDED, and the
// typed contract (appjobs.VideoCreateResult) validates fail-closed
// before it ever reaches the caller.

// BuildVideoCreateResult assembles the canonical typed result.
func BuildVideoCreateResult(run *Run) (appjobs.VideoCreateResult, error) {
	if run == nil {
		return appjobs.VideoCreateResult{}, fmt.Errorf("%w: nil run", ErrWorkflowFailed)
	}
	verified, published := run.Facts.Verified, run.Facts.Published
	if verified == nil || published == nil {
		return appjobs.VideoCreateResult{}, fmt.Errorf("%w: result requires verified and published final video facts", ErrWorkflowFailed)
	}
	script, youtube, stock, voiceover, render, assembly := run.State.ChildLedger()
	result := appjobs.VideoCreateResult{
		VideoID:       published.AssetID,
		ScriptAssetID: run.Facts.ScriptAssetID,
		FinalVideo: appjobs.FinalVideoArtifact{
			AssetID:    published.AssetID,
			MediaURL:   published.MediaURL,
			DriveRef:   appjobs.DriveRef{DriveFileID: published.DriveFileID},
			SHA256:     verified.SHA256,
			SizeBytes:  verified.SizeBytes,
			DurationMS: verified.DurationMS,
		},
		ThumbnailContext: run.thumbnailContext(),
		Children: appjobs.VideoCreateChildren{
			Script:    script,
			YouTube:   youtube,
			Stock:     stock,
			Voiceover: voiceover,
			Render:    render,
			Assembly:  assembly,
		},
		DurationMS:      verified.DurationMS,
		CompletedStages: run.State.CompletedStepKeys(),
	}
	if err := result.Validate(); err != nil {
		return appjobs.VideoCreateResult{}, fmt.Errorf("%w: %v", ErrWorkflowFailed, err)
	}
	return result, nil
}

// thumbnailContext is the §19 option B: the workflow returns the DATA
// the cover needs and the caller side (InstaeditLogin on the 51)
// generates the thumbnail with its own infrastructure. cover/thumbnail
// is deliberately NOT a render-lane phase.
func (r *Run) thumbnailContext() *appjobs.ThumbnailContext {
	frames := []string{}
	if r.Facts.OverlayPlan != nil && len(r.Facts.OverlayPlan.FrameAssetIDs) > 0 {
		frames = append(frames, r.Facts.OverlayPlan.FrameAssetIDs...)
	}
	if len(frames) == 0 {
		for _, clip := range r.Facts.Acquired {
			if clip.Ref.AssetID != "" {
				frames = append(frames, clip.Ref.AssetID)
			}
		}
	}
	subjects := []string{}
	if r.Request.Topic != "" {
		subjects = append(subjects, r.Request.Topic)
	}
	return &appjobs.ThumbnailContext{
		Title:           r.Request.Topic,
		Subjects:        subjects,
		SuggestedPrompt: fmt.Sprintf("Cinematic cover image about %s, bold composition, high contrast", r.Request.Topic),
		FrameAssetIDs:   frames,
	}
}

// buildManifest is the §18 artifact-spine hand-off. The producer has
// ALREADY completed publication (the canonical delivery publisher ran
// in 11_publish), so the manifest entry carries the Remote* identities
// and an empty Path — exactly the "populated by producers that already
// completed publication" shape kernel/job/artifact_manifest.go
// documents. A required Path-based entry would make the Sender
// re-upload the file and create a second Drive copy.
func buildManifest(run *Run, result appjobs.VideoCreateResult) job.ArtifactManifest {
	entry := job.Artifact{
		ID:                run.Job.ID + ":" + string(job.ArtifactKindFinalVideo),
		Kind:              job.ArtifactKindFinalVideo,
		Path:              "",
		Filename:          "final_video.mp4",
		MIMEType:          "video/mp4",
		SizeBytes:         result.FinalVideo.SizeBytes,
		SHA256:            result.FinalVideo.SHA256,
		Required:          false,
		RemoteFileID:      result.FinalVideo.DriveFileID,
		RemoteWebViewLink: result.FinalVideo.MediaURL,
		ArtifactMetadata: map[string]any{
			"asset_id":    result.FinalVideo.AssetID,
			"duration_ms": result.FinalVideo.DurationMS,
			"video_id":    result.VideoID,
		},
	}
	if run.Facts.Published != nil && run.Facts.Published.DownloadURL != "" {
		entry.RemoteDownloadLink = run.Facts.Published.DownloadURL
	}
	return job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    run.Job.CorrelationID,
		JobID:         run.Job.ID,
		Artifacts:     []job.Artifact{entry},
	}
}

// handlerResult flattens the typed result into the canonical handler
// result map. The flattening is deliberate: the 51 must be able to read
// `job.result.final_video.media_url` from the broker projection without
// decoding a nested envelope. The canonical __artifact_manifest rides
// along so the artifact spine sees the final video.
func handlerResult(run *Run, result appjobs.VideoCreateResult) (job.Result, error) {
	flat := map[string]any{}
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("%w: encode result: %v", ErrWorkflowFailed, err)
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return nil, fmt.Errorf("%w: project result: %v", ErrWorkflowFailed, err)
	}
	manifest := buildManifest(run, result)
	manifestMap := map[string]any{}
	mraw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("%w: encode artifact manifest: %v", ErrWorkflowFailed, err)
	}
	if err := json.Unmarshal(mraw, &manifestMap); err != nil {
		return nil, fmt.Errorf("%w: project artifact manifest: %v", ErrWorkflowFailed, err)
	}
	flat[job.ManifestKey] = manifestMap
	return job.Result(flat), nil
}

package videocreate

import (
	"context"
	"fmt"
)

// ── Recovery: facts rehydration after a restart (§7/§11) ──────────────
//
// The step-store rows say WHERE to resume; this rebuilds WHAT the
// finished stages produced. The rule is strict:
//
//   - durable facts (asset ids, hashes, Drive identities, scenes,
//     selections, assembled/published/verified identities) come from
//     the step outputs — they survive every restart by construction;
//   - LOCAL MATERIALIZATIONS (the files the media plane works on) are
//     re-read from the terminal children's results. They are execution
//     details (§11): the workflow state never carries them as truth.
//
// A replayed run that finds every step completed rehydrates the full
// result WITHOUT executing anything — that is what makes "Replay →
// nessun duplicato" true at the result level too.

// RehydrateFacts rebuilds the run's transient facts from the durable
// step outputs + the child-job ledger.
func RehydrateFacts(ctx context.Context, run *Run) error {
	if run == nil {
		return fmt.Errorf("%w: nil run", ErrStateCorrupt)
	}
	if rec := run.State.StageRecordFor("01_script"); rec != nil {
		run.Facts.ScriptAssetID = rec.Output.ScriptAssetID
		run.Facts.Scenes = rec.Output.Scenes
		run.Facts.TextSegments = rec.Output.TextSegments
		run.Facts.AudioPlanJSON = rec.Output.AudioPlan
		if rec.Output.AudioMaster != nil {
			run.Facts.FinalAudio = rec.Output.AudioMaster
		}
	}
	if rec := run.State.StageRecordFor("02_media_search"); rec != nil {
		run.Facts.Candidates = rec.Output.Selection
	}
	if err := rehydrateAcquired(ctx, run); err != nil {
		return err
	}
	if err := rehydrateVoiceover(ctx, run); err != nil {
		return err
	}
	if rec := run.State.StageRecordFor("05_audio_master"); rec != nil && rec.Output.AudioMaster != nil {
		run.Facts.FinalAudio = rec.Output.AudioMaster
	}
	if rec := run.State.StageRecordFor("06_overlay_plan"); rec != nil {
		run.Facts.OverlayPlan = rec.Output.OverlayPlan
	}
	if err := rehydrateRendered(ctx, run); err != nil {
		return err
	}
	if rec := run.State.StageRecordFor("08_assemble"); rec != nil {
		run.Facts.Assembled = rec.Output.Assembled
	}
	if rec := run.State.StageRecordFor("09_audio_mux"); rec != nil && !rec.Output.Skipped && rec.Status == StageSucceeded {
		// The mux output is deterministic inside the persistent
		// per-job workspace (run.workPath), so the identity is derived,
		// not stored.
		run.Facts.Muxed = &MuxedVideo{Path: run.workPath("final_video.mp4")}
	}
	if rec := run.State.StageRecordFor("10_verify"); rec != nil {
		run.Facts.Verified = rec.Output.Verified
	}
	if rec := run.State.StageRecordFor("11_publish"); rec != nil {
		run.Facts.Published = rec.Output.Published
	}
	return nil
}

// rehydrateAcquired rebuilds the acquired-clip facts. The pairing
// contract (ChildJobs[i] produced Artifacts[i]) restores the durable
// identity; the child result restores the local materialization.
func rehydrateAcquired(ctx context.Context, run *Run) error {
	rec := run.State.StageRecordFor("03_media_acquire")
	if rec == nil {
		return nil
	}
	for i, childID := range rec.Jobs {
		ref := StageArtifactRef{}
		if i < len(rec.Output.Artifacts) {
			ref = rec.Output.Artifacts[i]
		}
		localPath := ""
		if childID != "" {
			child, err := run.Deps.Children.WaitTerminal(ctx, childID)
			if err != nil {
				return fmt.Errorf("%w: rehydrate acquire child %s: %v", ErrStateCorrupt, childID, err)
			}
			var res AcquireChildResult
			if err := childResult(child, &res); err != nil {
				return fmt.Errorf("%w: rehydrate acquire child %s result: %v", ErrStateCorrupt, childID, err)
			}
			localPath = res.LocalPath
		}
		run.Facts.Acquired = append(run.Facts.Acquired, AcquiredClip{
			Ref:        ref,
			LocalPath:  localPath,
			Source:     ref.Source,
			SceneIndex: i + 1,
		})
	}
	return nil
}

// rehydrateVoiceover rebuilds the voiceover facts from its child result.
func rehydrateVoiceover(ctx context.Context, run *Run) error {
	rec := run.State.StageRecordFor("04_voiceover")
	if rec == nil || len(rec.Jobs) == 0 {
		return nil
	}
	ref := StageArtifactRef{}
	if len(rec.Output.Artifacts) > 0 {
		ref = rec.Output.Artifacts[0]
	}
	fact := &VoiceoverFact{Ref: ref}
	if rec.Jobs[0] != "" && rec.Status != StageSkipped {
		child, err := run.Deps.Children.WaitTerminal(ctx, rec.Jobs[0])
		if err != nil {
			return fmt.Errorf("%w: rehydrate voiceover child: %v", ErrStateCorrupt, err)
		}
		var res VoiceoverChildResult
		if err := childResult(child, &res); err != nil {
			return fmt.Errorf("%w: rehydrate voiceover child result: %v", ErrStateCorrupt, err)
		}
		fact.LocalPath = res.LocalPath
		fact.SampleRate = res.SampleRate
		fact.Channels = res.Channels
		fact.Codec = res.Codec
	}
	run.Facts.Voiceover = fact
	return nil
}

// rehydrateRendered rebuilds the copy-certified render segments (needed
// when 08_assemble has not run yet: the assembler contract requires the
// certification facts, not just the identities).
func rehydrateRendered(ctx context.Context, run *Run) error {
	rec := run.State.StageRecordFor("07_render")
	if rec == nil {
		return nil
	}
	for i, childID := range rec.Jobs {
		sceneID := ""
		if i < len(run.Facts.Scenes) {
			sceneID = run.Facts.Scenes[i]
		}
		if childID == "" {
			continue
		}
		child, err := run.Deps.Children.WaitTerminal(ctx, childID)
		if err != nil {
			return fmt.Errorf("%w: rehydrate render child %s: %v", ErrStateCorrupt, childID, err)
		}
		var res RenderChildResult
		if err := childResult(child, &res); err != nil {
			return fmt.Errorf("%w: rehydrate render child %s result: %v", ErrStateCorrupt, childID, err)
		}
		run.Facts.Rendered = append(run.Facts.Rendered, RenderedClip{
			Segment: AssembleSegment{
				AssetID:         res.AssetID,
				SHA256:          res.ContentSHA,
				DurationMS:      res.DurationMS,
				CopyCertified:   res.CopyCertified,
				ContractID:      res.ContractID,
				StreamSignature: res.StreamSignature,
			},
			LocalPath: res.LocalPath,
			SceneID:   sceneID,
		})
	}
	return nil
}

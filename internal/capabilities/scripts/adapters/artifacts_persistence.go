// Package scripts/adapters — artifacts_persistence.go is the
// canonical home for script.generate artifact persistence ops
// (godlike/06 SSOT: one adapter per side-effect). The handler used
// to embed os.MkdirAll / os.WriteFile / os.TempDir / ComputeSHA256
// directly inside the broker-dispatch handler. PR-GODOBJ-4
// KILL-K1 moves ALL of those ops to this adapter.
//
// KILL-K1 contract (per user spec, July 2026):
//   - handler (`internal/capabilities/scripts/jobs/generation_handler.go`)
//     does NOT touch filesystem. It calls
//     PersistGeneratedArtifacts(ctx, jobID, *GenerationResult)
//     and uses the returned []job.Artifact to build the
//     typed *job.ArtifactManifest in generation_manifest.go.
//   - The adapter does all FS ops: os.TempDir, os.MkdirAll,
//     os.WriteFile, file stat, sha256 hashing.
//   - On error, the adapter returns a typed error mapping to the
//     canonical godlike/07 typed-error contract (errors.New + %w).
//
// godlike/06 SSOT surface: this file is the SINGLE canonical home
// for the §8.4 spec multi-artifact shape on the script.generate
// job type. Pre-PR-GODOBJ-4 the same shape lived inline in the
// handler's buildAndInjectManifest; the migration here is the
// godlike/07-told-by-godlike/06 split: persistence lives in the
// adapter, manifest construction lives in the handler.
//
// godlike/07 honest-limitation: this file exceeds the 66-LoC
// transitional cap (~150 LoC) because the §8.4 multi-artifact
// shape comprises 3 inline writing paths (script.json / scenes.json /
// workspace-mkdir / optional final audio) + SHA256 + manifest
// assembly. Forward-pointer linked_issue (zero-baseline rule):
// PR-GODOBJ-4c-PERSIST-ADAPTER-SLIM extracts per-kind writers into
// per-kind helper functions (≤30 LoC each). Deadline 2026-08-15.
//
// JSON-shape invariant: §8.4 spec post-Sprint-1.0 lists exactly
// 2 kinds in the manifest — script-json (REQUIRED), scenes (OPTIONAL
// when generated) — plus the optional certified final_audio.m4a.
// Per-scene voiceovers are INTENTIONALLY NOT emitted as manifest
// artifacts (P0 finalize optimization, Aug 2026): the TTS pipeline
// already publishes each scene file to Drive during generation
// (voiceover.VoiceoverItemExecutor "TTS → publish → finalize") and
// stores the DriveLink in the scene binding; the Google-Doc, scenes.json
// and script.json consumers all read THAT link, and audio/render use
// local paths. The finalize re-publication was a second upload of the
// same bytes with no downstream consumer (Qdrant classifies audio as
// REGISTERED-only). The files stay on disk (job workspace + voiceover
// text-hash cache) for audio compile and cross-run reuse. Document
// artefacts (document-pdf, document-markdown) were RETIRED in
// Sprint 1.0; the canonical downstream document.generate job owns
// Google-Doc creation. Pre-§8.4 kinds (script_text, metadata,
// entities, image) are REMOVED here.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// PersistGeneratedArtifacts writes the canonical §8.4 multi-artifact
// sidecar for a script.generate run. The handler invokes this BEFORE
// buildManifestFromArtifacts. The adapter is the SINGLE canonical
// home for filesystem ops in this pipeline (PR-GODOBJ-4 KILL K1).
//
// Signature:
//
//	ctx context.Context       — propagation context for triage
//	jobID string              — used in artifact IDs (jobID + ":" + kind)
//	result *GenerationResult   — the typed aggregate result
//
// Returns []job.Artifact (the typed pre-computed artifact
// slice) and an error. The handler then wraps this slice into a
// *job.ArtifactManifest via buildManifestFromArtifacts.
//
// Workspace convention matches the worker's Workspace.Prepare:
//
//	/tmp/pipelinegen/jobs/<jobID>/output/
func PersistGeneratedArtifacts(
	ctx context.Context,
	jobID string,
	result *domainScript.GenerationResult,
	log *zap.Logger,
) ([]job.Artifact, error) {
	if log == nil {
		log = zap.NewNop()
	}
	if result == nil {
		return nil, nil
	}

	fs := filesystem.NewOS()
	outDir := workspaceOutputDir(jobID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("artifacts_persistence: mkdir %s: %w", outDir, err)
	}

	artifacts := make([]job.Artifact, 0, 3 /* script, scenes, optional final audio */)

	// ── 1. script-json (REQUIRED) ──────────────────────────────────────
	scriptJSONPath := filepath.Join(outDir, "script.json")
	// script.json is a public artifact. Keep local paths available on the
	// in-memory result for the local artifact writer, but never expose them
	// in the serialized contract.
	publicResult := *result
	publicResult.Output.SpecScene = stripVoiceoverLocalPaths(result.Output.SpecScene)
	if publicResult.FinalAudio != nil {
		finalAudio := *publicResult.FinalAudio
		finalAudio.Path = ""
		publicResult.FinalAudio = &finalAudio
	}
	scriptData, err := json.MarshalIndent(&publicResult, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("artifacts_persistence: marshal script.json: %w", err)
	}
	if writeErr := os.WriteFile(scriptJSONPath, scriptData, 0o644); writeErr != nil {
		return nil, fmt.Errorf("artifacts_persistence: write script.json: %w", writeErr)
	}
	sha, shaErr := job.ComputeSHA256(fs, scriptJSONPath)
	if shaErr != nil {
		return nil, fmt.Errorf("artifacts_persistence: sha256 script.json: %w", shaErr)
	}
	artifacts = append(artifacts, job.Artifact{
		ID:        jobID + ":script_json",
		Kind:      job.ArtifactKindScriptJSON,
		Path:      scriptJSONPath,
		Filename:  "script.json",
		MIMEType:  "application/json",
		SizeBytes: int64(len(scriptData)),
		SHA256:    sha,
		Required:  true,
	})

	// ── 2. scenes (OPTIONAL when generated) ────────────────────────────
	if len(result.Output.SpecScene.Scenes) > 0 {
		scenesJSONPath := filepath.Join(outDir, "scenes.json")
		scenesData, marErr := json.MarshalIndent(stripVoiceoverLocalPaths(result.Output.SpecScene), "", "  ")
		if marErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: marshal scenes.json: %w", marErr)
		}
		if writeErr := os.WriteFile(scenesJSONPath, scenesData, 0o644); writeErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: write scenes.json: %w", writeErr)
		}
		scenesSHA, scenesSHAErr := job.ComputeSHA256(fs, scenesJSONPath)
		if scenesSHAErr != nil {
			return nil, fmt.Errorf("artifacts_persistence: sha256 scenes.json: %w", scenesSHAErr)
		}
		artifacts = append(artifacts, job.Artifact{
			ID:        jobID + ":scenes",
			Kind:      job.ArtifactKindScenes,
			Path:      scenesJSONPath,
			Filename:  "scenes.json",
			MIMEType:  "application/json",
			SizeBytes: int64(len(scenesData)),
			SHA256:    scenesSHA,
			Required:  false,
		})
	}

	// ── 5. per-scene voiceovers — INTENTIONALLY NOT emitted ────────────
	// (P0 finalize optimization, Aug 2026 — O(N) → O(1) Drive uploads.)
	//
	// The TTS pipeline (RunVoiceoverSceneFanout → VoiceoverItemExecutor
	// "TTS → publish → finalize") already publishes each scene voiceover
	// to Drive during generation and hydrates the voiceovers row +
	// media_assets projection + the scene binding DriveLink. All
	// downstream consumers read THAT publication:
	//   - Google Docs  → scene.Bindings.Voiceover.Link (TTS link)
	//   - scenes.json / script.json → bindings (TTS links)
	//   - audio compile / render → local paths + final_audio.m4a
	//   - Qdrant index → audio is REGISTERED-only (skipped)
	//
	// Re-listing the same local files here made post_writer_finalize
	// re-upload identical bytes (~2.5s × N sequential Drive I/O ≈ the
	// 50s finalize bottleneck on a 20-clip job) with no downstream
	// consumer. The files remain on disk (job workspace + voiceover
	// text-hash cache) for the audio compile and cross-run reuse.

	if result.AudioMode == "COMBINED_TIMELINE" {
		if result.FinalAudio == nil || !result.FinalAudio.CopyEligible || !result.FinalAudio.FinalMix || strings.TrimSpace(result.FinalAudio.Path) == "" {
			return nil, fmt.Errorf("artifacts_persistence: combined audio is not certified")
		}
		if result.FinalAudio.Codec != "aac" || !strings.EqualFold(result.FinalAudio.Profile, "LC") || result.FinalAudio.SampleRate != 48000 || result.FinalAudio.Channels != 2 || result.FinalAudio.ChannelLayout != "stereo" || result.FinalAudio.DurationMS <= 0 || result.FinalAudio.Bitrate <= 0 || result.FinalAudio.AudioPlanSHA256 == "" || result.FinalAudio.AudioPlanVersion == "" || result.FinalAudio.AudioContractVersion == "" {
			return nil, fmt.Errorf("artifacts_persistence: final audio violates canonical copy contract")
		}
		info, err := os.Stat(result.FinalAudio.Path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			return nil, fmt.Errorf("artifacts_persistence: certified final audio is unavailable")
		}
		if result.FinalAudio.SizeBytes > 0 && result.FinalAudio.SizeBytes != info.Size() {
			return nil, fmt.Errorf("artifacts_persistence: final audio size mismatch")
		}
		sourceSHA, err := job.ComputeSHA256(fs, result.FinalAudio.Path)
		if err != nil {
			return nil, fmt.Errorf("artifacts_persistence: sha256 source final audio: %w", err)
		}
		if result.FinalAudio.FinalAudioSHA256 == "" || result.FinalAudio.FinalAudioSHA256 != sourceSHA {
			return nil, fmt.Errorf("artifacts_persistence: final audio hash mismatch")
		}
		source, err := os.ReadFile(result.FinalAudio.Path)
		if err != nil {
			return nil, fmt.Errorf("artifacts_persistence: open final audio: %w", err)
		}
		finalPath := filepath.Join(outDir, "final_audio.m4a")
		if err := os.WriteFile(finalPath, source, 0o644); err != nil {
			return nil, fmt.Errorf("artifacts_persistence: copy final audio: %w", err)
		}
		info, err = os.Stat(finalPath)
		if err != nil {
			return nil, fmt.Errorf("artifacts_persistence: stat final audio: %w", err)
		}
		sha, err := job.ComputeSHA256(fs, finalPath)
		if err != nil {
			return nil, fmt.Errorf("artifacts_persistence: sha256 final audio: %w", err)
		}
		result.FinalAudio.Path = finalPath
		result.FinalAudio.SizeBytes = info.Size()
		result.FinalAudio.FinalAudioSHA256 = sha
		artifacts = append(artifacts, job.Artifact{
			ID: result.FinalAudio.AssetID, Kind: job.ArtifactKindFinalAudio,
			Path: finalPath, Filename: "final_audio.m4a", MIMEType: "audio/mp4",
			SizeBytes: info.Size(), SHA256: sha, Required: true,
			ArtifactMetadata: map[string]any{
				"audio_asset_id":         result.FinalAudio.AssetID,
				"audio_contract_version": result.FinalAudio.AudioContractVersion,
				"audio_plan_version":     result.FinalAudio.AudioPlanVersion,
				"audio_plan_sha256":      result.FinalAudio.AudioPlanSHA256,
				"final_audio_sha256":     result.FinalAudio.FinalAudioSHA256,
				"audio_strategy":         "FINAL_AUDIO_COPY",
				"codec":                  result.FinalAudio.Codec, "profile": result.FinalAudio.Profile,
				"sample_rate": result.FinalAudio.SampleRate, "channels": result.FinalAudio.Channels,
				"channel_layout": result.FinalAudio.ChannelLayout,
				"bitrate":        result.FinalAudio.Bitrate, "size_bytes": result.FinalAudio.SizeBytes,
				"duration_ms": result.FinalAudio.DurationMS, "start_pts": result.FinalAudio.StartPTS,
				"final_mix":     result.FinalAudio.FinalMix,
				"copy_eligible": result.FinalAudio.CopyEligible,
			},
		})
	}

	return artifacts, nil
}

func stripVoiceoverLocalPaths(in domainScript.SpecSceneOutput) domainScript.SpecSceneOutput {
	out := in
	if len(in.Scenes) == 0 {
		return out
	}
	out.Scenes = make([]domainScript.SpecScene, len(in.Scenes))
	for i, scene := range in.Scenes {
		out.Scenes[i] = scene
		if scene.Bindings.Voiceover != nil {
			binding := *scene.Bindings.Voiceover
			binding.LocalPath = ""
			out.Scenes[i].Bindings.Voiceover = &binding
		}
	}
	return out
}

// workspaceOutputDir returns the job-workspace output directory.
// Convention (matches worker.Workspace.Prepare):
//
//	/tmp/pipelinegen/jobs/<jobID>/output/
func workspaceOutputDir(jobID string) string {
	return filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID, "output")
}

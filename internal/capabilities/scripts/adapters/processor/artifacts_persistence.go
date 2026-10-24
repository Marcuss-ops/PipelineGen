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
// shape comprises 3 inline writing paths (script.json /
// workspace-mkdir / optional final audio) + SHA256 + manifest
// assembly. Forward-pointer linked_issue (zero-baseline rule):
// PR-GODOBJ-4c-PERSIST-ADAPTER-SLIM extracts per-kind writers into
// per-kind helper functions (≤30 LoC each). Deadline 2026-08-15.
//
// JSON-shape invariant: the manifest lists exactly 2 kinds —
// script-json (REQUIRED) plus the optional certified final_audio.m4a.
// The former OPTIONAL scenes sidecar is NOT emitted any more
// (consumer-audit cut 2026-10-24): scenes.json was a strict subset of
// script.json (its `output.scenes` subtree) with ZERO machine readers
// anywhere in the tree — the final job resolves the script_json artifact,
// and no Go/Python/RenderingGen code ever opened scenes.json. Its only
// consumer class was humans browsing the delivery folder, who can read
// the same scenes at script.json's `output.scenes`; keeping it cost one
// extra hash + one extra sequential Drive upload per job in the finalize
// bottleneck.
// Per-scene voiceovers are INTENTIONALLY NOT emitted as manifest
// artifacts (P0 finalize optimization, Aug 2026): the TTS pipeline
// already publishes each scene file to Drive during generation
// (voiceover.VoiceoverItemExecutor "TTS → publish → finalize") and
// stores the DriveLink in the scene binding; the Google-Doc and
// script.json consumers all read THAT link, and audio/render use
// local paths. The finalize re-publication was a second upload of the
// same bytes with no downstream consumer (Qdrant classifies audio as
// REGISTERED-only). The files stay on disk (job workspace + voiceover
// text-hash cache) for audio compile and cross-run reuse. Document
// artefacts (document-pdf, document-markdown) were RETIRED in
// Sprint 1.0; the canonical downstream document.generate job owns
// Google-Doc creation. Pre-§8.4 kinds (script_text, metadata,
// entities, image) are REMOVED here.
package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	if result == nil {
		return nil, nil
	}

	fs := filesystem.NewOS()
	outDir := workspaceOutputDir(jobID)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("artifacts_persistence: mkdir %s: %w", outDir, err)
	}

	// P0 destination-bug fix (Sept 2026): the persisted sidecars MUST carry
	// the language they were generated in. Without it the pre-finalization
	// publisher falls back to a hard-coded default ("it") and an `en` run's
	// script.json / final_audio.m4a land in
	// <docs root>/<job>/it — one wrong-language folder per run. The language
	// is a routing HINT (never a folder id): the Drive resolver stays the only
	// owner of folder topology.
	language := strings.TrimSpace(result.Language)

	artifacts := make([]job.Artifact, 0, 2 /* script, optional final audio */)

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
		ID:               jobID + ":script_json",
		Kind:             job.ArtifactKindScriptJSON,
		Path:             scriptJSONPath,
		Filename:         "script.json",
		MIMEType:         "application/json",
		SizeBytes:        int64(len(scriptData)),
		SHA256:           sha,
		Required:         true,
		DriveLanguage:    language,
		ArtifactMetadata: languageRoutingMetadata(language),
	})

	// ── 2. scenes — INTENTIONALLY NOT emitted (consumer-audit cut
	// 2026-10-24) ───────────────────────────────────────────────────
	//
	// scenes.json duplicated script.json's `output.scenes` subtree
	// verbatim (same stripVoiceoverLocalPaths projection) and had zero
	// machine readers: the final job resolves only the script_json
	// artifact, and nothing in the tree opens scenes.json. Humans
	// reviewing the delivery folder read the same scenes inside
	// script.json. Emitting it cost one extra hash plus one extra
	// sequential Drive upload per job inside the measured finalize
	// bottleneck (~2.5s per artifact), with no consumer.

	// ── 5. per-scene voiceovers — INTENTIONALLY NOT emitted ────────────
	// (P0 finalize optimization, Aug 2026 — O(N) → O(1) Drive uploads.)
	//
	// The TTS pipeline (RunVoiceoverSceneFanout → VoiceoverItemExecutor
	// "TTS → publish → finalize") already publishes each scene voiceover
	// to Drive during generation and hydrates the voiceovers row +
	// media_assets projection + the scene binding DriveLink. All
	// downstream consumers read THAT publication:
	//   - Google Docs  → scene.Bindings.Voiceover.Link (TTS link)
	//   - script.json  → bindings (TTS links)
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
		sourceSize := info.Size()
		finalPath := filepath.Join(outDir, "final_audio.m4a")
		// Stream the certified master instead of materializing it in RAM. The
		// source digest is already verified against FinalAudioSHA256 above, and
		// the copy is byte-identical by construction; re-hashing the same bytes
		// here only added a full read plus a second full-size buffer.
		if err := copyCertifiedAudio(result.FinalAudio.Path, finalPath); err != nil {
			return nil, fmt.Errorf("artifacts_persistence: copy final audio: %w", err)
		}
		info, err = os.Stat(finalPath)
		if err != nil {
			return nil, fmt.Errorf("artifacts_persistence: stat final audio: %w", err)
		}
		if info.Size() != sourceSize {
			return nil, fmt.Errorf("artifacts_persistence: copied final audio size mismatch")
		}
		result.FinalAudio.Path = finalPath
		result.FinalAudio.SizeBytes = info.Size()
		result.FinalAudio.FinalAudioSHA256 = sourceSHA
		// The certified master is an audio artifact and routes to the
		// voiceover destination, which (like script) is project+language
		// scoped: it must carry the same language hint as the sidecars or it
		// re-introduces the wrong-language fallback for the audio tree.
		finalAudioMetadata := map[string]any{
			"audio_asset_id":         result.FinalAudio.AssetID,
			"drive_file_id":          result.FinalAudio.DriveFileID,
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
		}
		for key, value := range languageRoutingMetadata(language) {
			finalAudioMetadata[key] = value
		}
		artifacts = append(artifacts, job.Artifact{
			ID: result.FinalAudio.AssetID, Kind: job.ArtifactKindFinalAudio,
			Path: finalPath, Filename: "final_audio.m4a", MIMEType: "audio/mp4",
			SizeBytes: info.Size(), SHA256: sourceSHA, Required: true,
			DriveLanguage:    language,
			ArtifactMetadata: finalAudioMetadata,
		})
	}

	return artifacts, nil
}

// copyCertifiedAudio materializes a certified final master in the artifact
// output directory without reading it whole into memory. os.Link is the
// zero-copy path when source and destination share a filesystem; the fallback
// streams the bytes. The caller has already verified the source digest, so the
// destination does not need a second full read to re-prove the same bytes.
func copyCertifiedAudio(sourcePath, destPath string) error {
	if err := os.Remove(destPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(sourcePath, destPath); err == nil {
		return nil
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() { _ = source.Close() }()
	destination, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	closeErr := destination.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// languageRoutingMetadata returns the artifact-metadata half of the language
// routing hint (the Artifact.DriveLanguage field is the other half). It is
// empty for an unknown language: the canonical publisher derives the
// destination from DriveLanguage / the `language` metadata key and MUST NOT be
// handed a fabricated default (the pre-fix hard-coded "it" is why an `en` run
// published into the Italian tree).
func languageRoutingMetadata(language string) map[string]any {
	if strings.TrimSpace(language) == "" {
		return nil
	}
	return map[string]any{"language": language}
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

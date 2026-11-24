// Package artifacts owns script.generate artifact persistence. It is the
// canonical home for the §8.4 multi-artifact shape: script.json plus the
// optional certified final audio and final video. Filesystem I/O belongs here,
// never in the broker-dispatch handler.
//
// JSON-shape invariant: the manifest lists exactly the script JSON plus the
// optional certified final_audio and final_video. The retired scenes sidecar
// and per-scene voiceover re-publication remain intentionally absent.
package artifacts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// PersistGeneratedArtifacts writes the canonical §8.4 multi-artifact sidecar
// for a script.generate run. The handler invokes this before building the
// manifest. The adapter is the SINGLE canonical home for filesystem ops in this
// pipeline (PR-GODOBJ-4 KILL K1).
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

	// The persisted sidecars MUST carry the language they were generated in.
	// It is a routing HINT (never a folder id): the Drive resolver stays the
	// only owner of folder topology.
	language := strings.TrimSpace(result.Language)
	artifacts := make([]job.Artifact, 0, 3)

	scriptArtifact, err := persistScriptJSONArtifact(fs, outDir, jobID, result, language)
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, scriptArtifact)

	if result.AudioMode == "COMBINED_TIMELINE" {
		audioArtifact, err := persistFinalAudioArtifact(outDir, result, language)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, audioArtifact)
	}

	// The local lane's joined video is the job's deliverable when no remote
	// master exists. A remote master wins when both exist.
	if result.RemoteFinalVideo == nil && result.LocalFinalVideo != nil {
		videoArtifact, err := persistLocalFinalVideoArtifact(jobID, result, language)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, videoArtifact)
	}

	if result.RemoteFinalVideo != nil {
		videoArtifact, present, err := persistRemoteFinalVideoArtifact(jobID, result, language)
		if err != nil {
			return nil, err
		}
		if present {
			artifacts = append(artifacts, videoArtifact)
		}
	}

	return artifacts, nil
}

// persistScriptJSONArtifact writes the public script.json projection. Local
// paths remain on the in-memory result for the local artifact writer but never
// appear in the serialized contract.
func persistScriptJSONArtifact(fs *filesystem.OS, outDir, jobID string, result *domainScript.GenerationResult, language string) (job.Artifact, error) {
	scriptJSONPath := filepath.Join(outDir, "script.json")
	publicResult := *result
	publicResult.Output.SpecScene = stripVoiceoverLocalPaths(result.Output.SpecScene)
	if publicResult.FinalAudio != nil {
		finalAudio := *publicResult.FinalAudio
		finalAudio.Path = ""
		publicResult.FinalAudio = &finalAudio
	}
	if publicResult.RemoteFinalVideo != nil {
		remote := *publicResult.RemoteFinalVideo
		remote.Path = ""
		publicResult.RemoteFinalVideo = &remote
	}
	scriptData, err := json.MarshalIndent(&publicResult, "", "  ")
	if err != nil {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: marshal script.json: %w", err)
	}
	if writeErr := os.WriteFile(scriptJSONPath, scriptData, 0o644); writeErr != nil {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: write script.json: %w", writeErr)
	}
	sha, shaErr := job.ComputeSHA256(fs, scriptJSONPath)
	if shaErr != nil {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: sha256 script.json: %w", shaErr)
	}
	return job.Artifact{
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
	}, nil
}

// validateCertifiedFinalAudio enforces the canonical copy contract: only a
// certified, byte-verified final mix may be registered as final_audio.
func validateCertifiedFinalAudio(result *domainScript.GenerationResult, fs *filesystem.OS) (os.FileInfo, string, error) {
	audio := result.FinalAudio
	if audio == nil || !audio.CopyEligible || !audio.FinalMix || strings.TrimSpace(audio.Path) == "" {
		return nil, "", fmt.Errorf("artifacts_persistence: combined audio is not certified")
	}
	if audio.Codec != "aac" || !strings.EqualFold(audio.Profile, "LC") || audio.SampleRate != 48000 || audio.Channels != 2 || audio.ChannelLayout != "stereo" || audio.DurationMS <= 0 || audio.Bitrate <= 0 || audio.AudioPlanSHA256 == "" || audio.AudioPlanVersion == "" || audio.AudioContractVersion == "" {
		return nil, "", fmt.Errorf("artifacts_persistence: final audio violates canonical copy contract")
	}
	info, err := os.Stat(audio.Path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return nil, "", fmt.Errorf("artifacts_persistence: certified final audio is unavailable")
	}
	if audio.SizeBytes > 0 && audio.SizeBytes != info.Size() {
		return nil, "", fmt.Errorf("artifacts_persistence: final audio size mismatch")
	}
	sourceSHA, err := job.ComputeSHA256(fs, audio.Path)
	if err != nil {
		return nil, "", fmt.Errorf("artifacts_persistence: sha256 source final audio: %w", err)
	}
	if audio.FinalAudioSHA256 == "" || audio.FinalAudioSHA256 != sourceSHA {
		return nil, "", fmt.Errorf("artifacts_persistence: final audio hash mismatch")
	}
	return info, sourceSHA, nil
}

func persistFinalAudioArtifact(outDir string, result *domainScript.GenerationResult, language string) (job.Artifact, error) {
	fs := filesystem.NewOS()
	sourceInfo, sourceSHA, err := validateCertifiedFinalAudio(result, fs)
	if err != nil {
		return job.Artifact{}, err
	}
	sourceSize := sourceInfo.Size()
	finalPath := filepath.Join(outDir, "final_audio.m4a")
	// Stream the certified master instead of materializing it in RAM. The
	// source digest is already verified above, and the copy is byte-identical
	// by construction.
	if err := copyCertifiedAudio(result.FinalAudio.Path, finalPath); err != nil {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: copy final audio: %w", err)
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: stat final audio: %w", err)
	}
	if info.Size() != sourceSize {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: copied final audio size mismatch")
	}
	result.FinalAudio.Path = finalPath
	result.FinalAudio.SizeBytes = info.Size()
	result.FinalAudio.FinalAudioSHA256 = sourceSHA
	return job.Artifact{
		ID: result.FinalAudio.AssetID, Kind: job.ArtifactKindFinalAudio,
		Path: finalPath, Filename: "final_audio.m4a", MIMEType: "audio/mp4",
		SizeBytes: info.Size(), SHA256: sourceSHA, Required: true,
		DriveLanguage:    language,
		ArtifactMetadata: finalAudioMetadata(result, language),
	}, nil
}

func finalAudioMetadata(result *domainScript.GenerationResult, language string) map[string]any {
	audio := result.FinalAudio
	metadata := map[string]any{
		"audio_asset_id":         audio.AssetID,
		"drive_file_id":          audio.DriveFileID,
		"audio_contract_version": audio.AudioContractVersion,
		"audio_plan_version":     audio.AudioPlanVersion,
		"audio_plan_sha256":      audio.AudioPlanSHA256,
		"final_audio_sha256":     audio.FinalAudioSHA256,
		"audio_strategy":         "FINAL_AUDIO_COPY",
		"codec":                  audio.Codec, "profile": audio.Profile,
		"sample_rate": audio.SampleRate, "channels": audio.Channels,
		"channel_layout": audio.ChannelLayout,
		"bitrate":        audio.Bitrate, "size_bytes": audio.SizeBytes,
		"duration_ms": audio.DurationMS, "start_pts": audio.StartPTS,
		"final_mix":     audio.FinalMix,
		"copy_eligible": audio.CopyEligible,
	}
	for key, value := range languageRoutingMetadata(language) {
		metadata[key] = value
	}
	return metadata
}

// persistLocalFinalVideoArtifact registers the job's own joined video exactly
// like a remote master, with deferred Drive publication.
func persistLocalFinalVideoArtifact(jobID string, result *domainScript.GenerationResult, language string) (job.Artifact, error) {
	local := result.LocalFinalVideo
	videoPath := strings.TrimSpace(local.Path)
	if videoPath == "" {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: local final video has no path")
	}
	info, err := os.Stat(videoPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: local final video is unavailable at %s", videoPath)
	}
	if local.SizeBytes > 0 && local.SizeBytes != info.Size() {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: local final video size mismatch")
	}
	videoSHA, err := job.ComputeSHA256(filesystem.NewOS(), videoPath)
	if err != nil {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: sha256 local final video: %w", err)
	}
	if strings.TrimSpace(local.SHA256) != "" && !strings.EqualFold(strings.TrimSpace(local.SHA256), videoSHA) {
		return job.Artifact{}, fmt.Errorf("artifacts_persistence: local final video hash mismatch")
	}
	metadata := map[string]any{
		"source":                                 "overlay_join",
		"joined_items":                           local.JoinedItems,
		"plan_items":                             local.PlanItems,
		"width":                                  local.Width,
		"height":                                 local.Height,
		"fps":                                    local.FPS,
		"duration_us":                            local.DurationUS,
		finalization.DrivePublicationMetadataKey: finalization.DrivePublicationDeferred,
	}
	for key, value := range languageRoutingMetadata(language) {
		metadata[key] = value
	}
	return job.Artifact{
		ID: jobID + ":final_video", Kind: job.ArtifactKindFinalVideo,
		Path: videoPath, Filename: finalVideoFilename(result.Title),
		MIMEType: "video/mp4", SizeBytes: info.Size(), SHA256: videoSHA,
		Required: true, DriveLanguage: language, ArtifactMetadata: metadata,
	}, nil
}

// persistRemoteFinalVideoArtifact registers the downloaded composite master.
// An empty path with a downloadable URL is a hard failure; an empty opaque URL
// carries no artifact. The bool reports whether an artifact was produced.
func persistRemoteFinalVideoArtifact(jobID string, result *domainScript.GenerationResult, language string) (job.Artifact, bool, error) {
	remote := result.RemoteFinalVideo
	artifactPath := strings.TrimSpace(remote.Path)
	if artifactPath == "" {
		if isHTTPArtifactURL(remote.ArtifactURL) {
			return job.Artifact{}, false, fmt.Errorf("artifacts_persistence: remote final video %s was not downloaded; configure VELOX_ARTIFACT_TOKEN or VELOX_MASTER_ADMIN_TOKEN", remote.JobID)
		}
		return job.Artifact{}, false, nil
	}
	info, err := os.Stat(artifactPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
		return job.Artifact{}, false, fmt.Errorf("artifacts_persistence: remote final video is unavailable at %s", artifactPath)
	}
	if remote.SizeBytes > 0 && remote.SizeBytes != info.Size() {
		return job.Artifact{}, false, fmt.Errorf("artifacts_persistence: remote final video size mismatch")
	}
	videoSHA, err := job.ComputeSHA256(filesystem.NewOS(), artifactPath)
	if err != nil {
		return job.Artifact{}, false, fmt.Errorf("artifacts_persistence: sha256 remote final video: %w", err)
	}
	if want := strings.TrimSpace(remote.SHA256); want != "" && !strings.EqualFold(want, videoSHA) {
		return job.Artifact{}, false, fmt.Errorf("artifacts_persistence: remote final video hash mismatch")
	}
	metadata := map[string]any{
		"remote_job_id":                          remote.JobID,
		"artifact_url":                           remote.ArtifactURL,
		finalization.DrivePublicationMetadataKey: finalization.DrivePublicationDeferred,
	}
	// Pin the exact job folder resolved by the render plan. Without it a video
	// artifact falls back to the generic youtube_clip tree.
	if folderID := strings.TrimSpace(remote.DriveFolderID); folderID != "" {
		metadata["resolved_folder_id"] = folderID
		metadata["drive_folder_id"] = folderID
	}
	return job.Artifact{
		ID: jobID + ":final_video", Kind: job.ArtifactKindFinalVideo,
		Path: artifactPath, Filename: finalVideoFilename(result.Title),
		MIMEType: "video/mp4", SizeBytes: info.Size(), SHA256: videoSHA,
		Required: true, DriveLanguage: language, ArtifactMetadata: metadata,
	}, true, nil
}

// copyCertifiedAudio materializes a certified final master in the artifact
// output directory without reading it whole into memory. os.Link is the
// zero-copy path when source and destination share a filesystem; the fallback
// streams the bytes.
func copyCertifiedAudio(sourcePath, destPath string) error {
	sourceInfo, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if destinationInfo, statErr := os.Stat(destPath); statErr == nil && os.SameFile(sourceInfo, destinationInfo) {
		// Resume may already point at the canonical output path; never unlink
		// the certified source just to recreate the same directory entry.
		return nil
	}
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
// routing hint. It is empty for an unknown language: the canonical publisher
// must never receive a fabricated default.
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
func workspaceOutputDir(jobID string) string {
	return filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID, "output")
}

// isHTTPArtifactURL reports whether the Master receipt points at bytes the
// Producer must download. Drive URLs are already published; empty or opaque
// URLs carry no downloadable artifact.
func isHTTPArtifactURL(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "http://")
}

// finalVideoFilename gives the composite a human-readable Drive name while
// keeping it filesystem-safe. It falls back to output_name, then title.
func finalVideoFilename(names ...string) string {
	name := ""
	for _, candidate := range names {
		if strings.TrimSpace(candidate) != "" {
			name = strings.TrimSpace(candidate)
			break
		}
	}
	if name == "" {
		name = "final_video"
	}
	name = strings.NewReplacer(
		"/", "-", "\\", "-", ":", "-", "*", "-", "?", "-",
		"\"", "-", "<", "-", ">", "-", "|", "-",
	).Replace(name)
	if !strings.HasSuffix(strings.ToLower(name), ".mp4") {
		name += ".mp4"
	}
	return name
}

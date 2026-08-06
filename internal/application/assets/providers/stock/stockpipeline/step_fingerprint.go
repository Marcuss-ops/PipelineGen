package stockpipeline

import (
	"encoding/json"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

const checkpointFingerprintVersion = "stock-step-fingerprint-v2"

type checkpointFingerprintPayload struct {
	Version        string                         `json:"version"`
	JobID          string                         `json:"job_id"`
	StepKey        string                         `json:"step_key"`
	PolicyVersion  string                         `json:"policy_version"`
	URLs           checkpointFingerprintURLs      `json:"urls"`
	Timestamps     []checkpointFingerprintWindow  `json:"timestamps"`
	Configuration  checkpointFingerprintConfig    `json:"configuration"`
	Durations      checkpointFingerprintDurations `json:"durations"`
	PreviousOutput json.RawMessage                `json:"previous_output"`
}

type checkpointFingerprintURLs struct {
	Direct []string `json:"direct"`
	Drive  []string `json:"drive"`
	Search []string `json:"search"`
}

type checkpointFingerprintWindow struct {
	URL      string  `json:"url"`
	StartSec float64 `json:"start_sec"`
	EndSec   float64 `json:"end_sec"`
}

type checkpointFingerprintConfig struct {
	OrchestratorMaxConcurrentJobs  int                  `json:"orchestrator_max_concurrent_jobs"`
	TotalMinutes                   int                  `json:"total_minutes"`
	TargetTotalDurationSeconds     int                  `json:"target_total_duration_seconds"`
	TargetDurationPerSourceSeconds int                  `json:"target_duration_per_source_seconds"`
	ClipsPerSource                 int                  `json:"clips_per_source"`
	ClipDurationSeconds            int                  `json:"clip_duration_seconds"`
	DownloadMode                   string               `json:"download_mode"`
	MaxVideos                      int                  `json:"max_videos"`
	NoAudio                        bool                 `json:"no_audio"`
	NoEffects                      bool                 `json:"no_effects"`
	NoTransitions                  bool                 `json:"no_transitions"`
	Subfolder                      string               `json:"subfolder"`
	FolderName                     string               `json:"folder_name"`
	DriveFolderID                  string               `json:"drive_folder_id"`
	FolderID                       string               `json:"folder_id"`
	DriveFolderResolved            bool                 `json:"drive_folder_resolved"`
	Persist                        bool                 `json:"persist"`
	Metadata                       *ChunkMetadataInput  `json:"metadata,omitempty"`
	Clips                          []checkpointClipSpec `json:"clips"`
}

type checkpointClipSpec struct {
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url,omitempty"`
	StartSec    float64  `json:"start_sec"`
	EndSec      float64  `json:"end_sec"`
	Round       int      `json:"round,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Category    string   `json:"category,omitempty"`
	Slug        string   `json:"slug,omitempty"`
	ParentSlug  string   `json:"parent_slug,omitempty"`
}

type checkpointFingerprintDurations struct {
	ChunkDurationInputSeconds int `json:"chunk_duration_input_seconds"`
	ClipDurationInputSeconds  int `json:"clip_duration_input_seconds"`
	SecondsPerSegment         int `json:"seconds_per_segment"`
	ChunkDurationConfigSec    int `json:"chunk_duration_config_seconds"`
	ClipDurationConfigSec     int `json:"clip_duration_config_seconds"`
}

// stepInputFingerprint returns a SHA-256 fingerprint of the complete
// deterministic identity of one checkpoint attempt. It deliberately excludes
// wall-clock values and broker lease expiry, while including domain timestamps
// (clip windows) and the prior step's deterministic output.
//
// legacyStepInputFingerprint identifies checkpoints written before the
// content-addressed v2 contract. It remains available only at the explicit
// migration seam in RunResilient and in legacy-resume fixtures.
func legacyStepInputFingerprint(jobID, stepName string) string {
	return jobID + "|" + stepName
}

func stepInputFingerprint(jobID, stepName string, cfg OrchestratorConfig, input *RunInput, previous *RunState) string {
	if input == nil {
		input = &RunInput{}
	}
	if cfg.JobId == "" {
		cfg.JobId = jobID
	}
	if cfg.PolicyVersion == "" {
		cfg.PolicyVersion = input.PolicyVersion
	}
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}

	payload := checkpointFingerprintPayload{
		Version:       checkpointFingerprintVersion,
		JobID:         jobID,
		StepKey:       stepName,
		PolicyVersion: cfg.PolicyVersion,
		URLs: checkpointFingerprintURLs{
			Direct: canonicalStrings(input.DirectURLs),
			Drive:  canonicalStrings(input.DriveURLs),
			Search: canonicalStrings(input.SearchQueries),
		},
		Timestamps: fingerprintWindows(input, previous),
		Configuration: checkpointFingerprintConfig{
			OrchestratorMaxConcurrentJobs:  cfg.MaxConcurrentJobs,
			TotalMinutes:                   input.TotalMinutes,
			TargetTotalDurationSeconds:     input.TargetTotalDurationSeconds,
			TargetDurationPerSourceSeconds: input.TargetDurationPerSourceSeconds,
			ClipsPerSource:                 input.ClipsPerSource,
			ClipDurationSeconds:            input.ClipDurationSeconds,
			DownloadMode:                   input.DownloadMode,
			MaxVideos:                      input.MaxVideos,
			NoAudio:                        input.NoAudio,
			NoEffects:                      input.NoEffects,
			NoTransitions:                  input.NoTransitions,
			Subfolder:                      input.Subfolder,
			FolderName:                     input.FolderName,
			DriveFolderID:                  input.DriveFolderID,
			FolderID:                       input.FolderID,
			DriveFolderResolved:            input.DriveFolderResolved,
			Persist:                        input.Persist,
			Metadata:                       input.Metadata,
			Clips:                          fingerprintClips(input.Clips),
		},
		Durations: checkpointFingerprintDurations{
			ChunkDurationInputSeconds: input.ChunkDuration,
			ClipDurationInputSeconds:  input.ClipDuration,
			SecondsPerSegment:         input.SecondsPerSegment,
			ChunkDurationConfigSec:    cfg.ChunkDurationSec,
			ClipDurationConfigSec:     cfg.ClipDurationSec,
		},
		PreviousOutput: deterministicPreviousOutputOrMarker(previous),
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		// Keep the fallback domain-separated and error-specific. This path
		// is defensive (the payload is composed of JSON-safe DTOs), but
		// different serialization failures must never collapse to one
		// indistinguishable previous-output marker.
		return sha256String(checkpointFingerprintVersion + "|payload-serialization-error|" + jobID + "|" + stepName + "|" + err.Error())
	}
	return sha256String(string(raw))
}

func deterministicPreviousOutputOrMarker(state *RunState) json.RawMessage {
	output, err := deterministicPreviousOutput(state)
	if err != nil {
		marker, marshalErr := json.Marshal(struct {
			SerializationError bool   `json:"serialization_error"`
			Error              string `json:"error"`
		}{SerializationError: true, Error: err.Error()})
		if marshalErr != nil {
			return json.RawMessage(`{"serialization_error":true}`)
		}
		return marker
	}
	return output
}

func canonicalStrings(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}
	return append([]string(nil), values...)
}

func fingerprintClips(clips []ClipSpec) []checkpointClipSpec {
	if len(clips) == 0 {
		return []checkpointClipSpec{}
	}
	out := make([]checkpointClipSpec, len(clips))
	for i, clip := range clips {
		out[i] = checkpointClipSpec{
			Title: clip.Title, Description: clip.Description, URL: clip.URL,
			StartSec: clip.StartSec, EndSec: clip.EndSec, Round: clip.Round,
			Tags: canonicalStrings(clip.Tags), Category: clip.Category,
			Slug: clip.Slug, ParentSlug: clip.ParentSlug,
		}
	}
	return out
}

func fingerprintWindows(input *RunInput, previous *RunState) []checkpointFingerprintWindow {
	windows := make([]checkpointFingerprintWindow, 0)
	if input != nil {
		for _, clip := range input.Clips {
			windows = append(windows, checkpointFingerprintWindow{URL: clip.URL, StartSec: clip.StartSec, EndSec: clip.EndSec})
		}
	}
	if previous != nil {
		for _, plan := range previous.Plan {
			windows = append(windows, checkpointFingerprintWindow{URL: plan.SourceID, StartSec: plan.StartSec, EndSec: plan.EndSec})
		}
	}
	return windows
}

type deterministicStagedAsset struct {
	SourceID    string  `json:"source_id"`
	Bytes       int64   `json:"bytes"`
	DurationSec float64 `json:"duration_sec"`
}

type deterministicChunkState struct {
	Index          int      `json:"index"`
	ArtifactID     string   `json:"artifact_id"`
	SourceURL      string   `json:"source_url"`
	SourceProvider string   `json:"source_provider"`
	SourceVideoID  string   `json:"source_video_id"`
	StartSec       float64  `json:"start_sec"`
	EndSec         float64  `json:"end_sec"`
	SHA256         string   `json:"sha256"`
	SizeBytes      int64    `json:"size_bytes"`
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	Round          int      `json:"round"`
	Tags           []string `json:"tags"`
	Category       string   `json:"category"`
	Slug           string   `json:"slug"`
}

type deterministicMetadataState struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type deterministicManifestArtifact struct {
	ID               string         `json:"id"`
	Kind             string         `json:"kind"`
	Filename         string         `json:"filename"`
	MIMEType         string         `json:"mime_type"`
	SizeBytes        int64          `json:"size_bytes"`
	SHA256           string         `json:"sha256"`
	Required         bool           `json:"required"`
	ArtifactMetadata map[string]any `json:"artifact_metadata,omitempty"`
}

type deterministicManifest struct {
	SchemaVersion string                          `json:"schema_version"`
	WorkflowID    string                          `json:"workflow_id"`
	JobID         string                          `json:"job_id"`
	Artifacts     []deterministicManifestArtifact `json:"artifacts"`
}

// deterministicPreviousOutput excludes execution-time timestamps, local
// filesystem paths, and remote links. It retains the logical output contract
// (source windows, hashes, sizes, artifact roles, and metadata) so the next
// step changes identity when the previous step's meaningful output changes.
func deterministicPreviousOutput(state *RunState) (json.RawMessage, error) {
	if state == nil {
		return json.RawMessage(`null`), nil
	}
	published := make([]deterministicChunkState, 0, len(state.Published))
	for _, chunk := range state.Published {
		published = append(published, deterministicChunkState{
			Index: chunk.Index, ArtifactID: chunk.ArtifactID, SourceURL: chunk.SourceURL,
			SourceProvider: chunk.SourceProvider, SourceVideoID: chunk.SourceVideoID,
			StartSec: chunk.StartSec, EndSec: chunk.EndSec, SHA256: chunk.SHA256,
			SizeBytes: chunk.SizeBytes, Title: chunk.Title, Description: chunk.Description,
			Round: chunk.Round, Tags: canonicalStrings(chunk.Tags), Category: chunk.Category,
			Slug: chunk.Slug,
		})
	}
	staged := make([]deterministicStagedAsset, 0, len(state.StagedAssets))
	for _, asset := range state.StagedAssets {
		if asset != nil {
			staged = append(staged, deterministicStagedAsset{SourceID: asset.SourceID, Bytes: asset.Bytes, DurationSec: asset.DurationSec})
		}
	}
	manifest := deterministicManifest{}
	if state.Manifest != nil {
		manifest.SchemaVersion, manifest.WorkflowID, manifest.JobID = state.Manifest.SchemaVersion, state.Manifest.WorkflowID, state.Manifest.JobID
		manifest.Artifacts = make([]deterministicManifestArtifact, 0, len(state.Manifest.Artifacts))
		for _, artifact := range state.Manifest.Artifacts {
			manifest.Artifacts = append(manifest.Artifacts, deterministicManifestArtifact{
				ID: artifact.ID, Kind: artifact.Kind, Filename: artifact.Filename,
				MIMEType: artifact.MIMEType, SizeBytes: artifact.SizeBytes, SHA256: artifact.SHA256,
				Required: artifact.Required, ArtifactMetadata: artifact.ArtifactMetadata,
			})
		}
	}
	projection := struct {
		Plan              []ClipPlan                 `json:"plan"`
		StagedAssets      []deterministicStagedAsset `json:"staged_assets"`
		CutPaths          []string                   `json:"cut_paths"`
		ComposedPaths     []string                   `json:"composed_paths"`
		Published         []deterministicChunkState  `json:"published"`
		MetadataPublished deterministicMetadataState `json:"metadata_published"`
		Manifest          deterministicManifest      `json:"manifest"`
		FinalStatus       job.Status                 `json:"final_status"`
		Counts            RunCounts                  `json:"counts"`
		SourceErrors      map[string]string          `json:"source_errors,omitempty"`
	}{
		Plan: state.Plan, StagedAssets: staged,
		CutPaths: state.CutPaths, ComposedPaths: state.ComposedPaths,
		Published:         published,
		MetadataPublished: deterministicMetadataState{SHA256: state.MetadataPublished.SHA256, SizeBytes: state.MetadataPublished.SizeBytes},
		Manifest:          manifest, FinalStatus: state.FinalStatus,
		Counts: state.Counts, SourceErrors: state.SourceErrors,
	}

	raw, err := json.Marshal(projection)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

// cloneRunState makes a complete deep copy for the next checkpoint's
// previous-output context. This is intentionally separate from
// deterministicPreviousOutput: the latter omits execution-only fields for
// hashing, while resume must preserve every field required by later steps.
func cloneRunState(state *RunState) (*RunState, error) {
	if state == nil {
		return nil, nil
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var clone RunState
	if err := json.Unmarshal(raw, &clone); err != nil {
		return nil, err
	}
	return &clone, nil
}

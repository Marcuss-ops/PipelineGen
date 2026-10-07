package stockpipeline

import (
	"encoding/json"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

type checkpointFingerprintPayload struct {
	SchemaVersion      string          `json:"schema_version"`
	JobID              string          `json:"job_id"`
	StepKey            string          `json:"step_key"`
	PolicyVersion      string          `json:"policy_version"`
	CanonicalInput     json.RawMessage `json:"canonical_input"`
	PreviousResultHash string          `json:"previous_result_hash"`
}

type checkpointFingerprintInput struct {
	URLs          checkpointFingerprintURLs      `json:"urls"`
	Timestamps    []checkpointFingerprintWindow  `json:"timestamps"`
	Configuration checkpointFingerprintConfig    `json:"configuration"`
	Durations     checkpointFingerprintDurations `json:"durations"`
}

const (
	checkpointFingerprintVersion          = "stock-step-fingerprint-v3"
	checkpointFingerprintV2Version        = "stock-step-fingerprint-v2"
	errCheckpointFingerprintSerialization = "checkpoint fingerprint payload serialization failed"
)

type legacyCheckpointFingerprintPayload struct {
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

func canonicalCheckpointFingerprintInput(cfg OrchestratorConfig, input *RunInput) (json.RawMessage, error) {
	runInput := input
	if runInput == nil {
		runInput = &RunInput{}
	}
	canonical := checkpointFingerprintInput{
		URLs: checkpointFingerprintURLs{
			Direct: canonicalStrings(runInput.DirectURLs),
			Drive:  canonicalStrings(runInput.DriveURLs),
			Search: canonicalStrings(runInput.SearchQueries),
		},
		Timestamps: fingerprintWindows(runInput),
		Configuration: checkpointFingerprintConfig{
			OrchestratorMaxConcurrentJobs:  cfg.MaxConcurrentJobs,
			TotalMinutes:                   runInput.TotalMinutes,
			TargetTotalDurationSeconds:     runInput.TargetTotalDurationSeconds,
			TargetDurationPerSourceSeconds: runInput.TargetDurationPerSourceSeconds,
			ClipsPerSource:                 runInput.ClipsPerSource,
			ClipDurationSeconds:            runInput.ClipDurationSeconds,
			DownloadMode:                   runInput.DownloadMode,
			MaxVideos:                      runInput.MaxVideos,
			NoAudio:                        runInput.NoAudio,
			NoEffects:                      runInput.NoEffects,
			NoTransitions:                  runInput.NoTransitions,
			Subfolder:                      runInput.Subfolder,
			FolderName:                     runInput.FolderName,
			DriveFolderID:                  runInput.DriveFolderID,
			FolderID:                       runInput.FolderID,
			DriveFolderResolved:            runInput.DriveFolderResolved,
			Persist:                        runInput.Persist,
			Metadata:                       runInput.Metadata,
			Clips:                          fingerprintClips(runInput.Clips),
		},
		Durations: checkpointFingerprintDurations{
			ChunkDurationInputSeconds: runInput.ChunkDuration,
			ClipDurationInputSeconds:  runInput.ClipDuration,
			SecondsPerSegment:         runInput.SecondsPerSegment,
			ChunkDurationConfigSec:    cfg.ChunkDurationSec,
			ClipDurationConfigSec:     cfg.ClipDurationSec,
		},
	}
	return json.Marshal(canonical)
}

func deterministicPreviousOutputHash(state *RunState) string {
	return sha256String(string(deterministicPreviousOutputOrMarker(state)))
}

func buildCheckpointFingerprintPayload(jobID, stepName string, cfg OrchestratorConfig, input *RunInput, previous *RunState) (checkpointFingerprintPayload, error) {
	canonicalInput, err := canonicalCheckpointFingerprintInput(cfg, input)
	if err != nil {
		return checkpointFingerprintPayload{}, err
	}
	return checkpointFingerprintPayload{
		SchemaVersion:      checkpointFingerprintVersion,
		JobID:              jobID,
		StepKey:            stepName,
		PolicyVersion:      cfg.PolicyVersion,
		CanonicalInput:     canonicalInput,
		PreviousResultHash: deterministicPreviousOutputHash(previous),
	}, nil
}

func marshalCheckpointFingerprintPayload(payload checkpointFingerprintPayload) ([]byte, error) {
	return json.Marshal(payload)
}

func fingerprintSerializationFallback(jobID, stepName string, err error) string {
	return sha256String(checkpointFingerprintVersion + "|" + errCheckpointFingerprintSerialization + "|" + jobID + "|" + stepName + "|" + err.Error())
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
// content-addressed fingerprint contract. It remains available only at the
// explicit migration seam in RunResilient and in legacy-resume fixtures.
func legacyStepInputFingerprint(jobID, stepName string) string {
	return jobID + "|" + stepName
}

func legacyV2StepInputFingerprint(jobID, stepName string, cfg OrchestratorConfig, input *RunInput, previous *RunState) string {
	runInput := input
	if runInput == nil {
		runInput = &RunInput{}
	}
	if cfg.JobId == "" {
		cfg.JobId = jobID
	}
	if cfg.PolicyVersion == "" {
		cfg.PolicyVersion = runInput.PolicyVersion
	}
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}
	payload := legacyCheckpointFingerprintPayload{
		Version: checkpointFingerprintV2Version, JobID: jobID, StepKey: stepName,
		PolicyVersion: cfg.PolicyVersion,
		URLs: checkpointFingerprintURLs{
			Direct: canonicalStrings(runInput.DirectURLs), Drive: canonicalStrings(runInput.DriveURLs), Search: canonicalStrings(runInput.SearchQueries),
		},
		Timestamps: fingerprintWindowsLegacy(runInput, previous),
		Configuration: checkpointFingerprintConfig{
			OrchestratorMaxConcurrentJobs: cfg.MaxConcurrentJobs, TotalMinutes: runInput.TotalMinutes,
			TargetTotalDurationSeconds: runInput.TargetTotalDurationSeconds, TargetDurationPerSourceSeconds: runInput.TargetDurationPerSourceSeconds,
			ClipsPerSource: runInput.ClipsPerSource, ClipDurationSeconds: runInput.ClipDurationSeconds,
			DownloadMode: runInput.DownloadMode, MaxVideos: runInput.MaxVideos, NoAudio: runInput.NoAudio, NoEffects: runInput.NoEffects,
			NoTransitions: runInput.NoTransitions, Subfolder: runInput.Subfolder, FolderName: runInput.FolderName,
			DriveFolderID: runInput.DriveFolderID, FolderID: runInput.FolderID, DriveFolderResolved: runInput.DriveFolderResolved,
			Persist: runInput.Persist, Metadata: runInput.Metadata, Clips: fingerprintClips(runInput.Clips),
		},
		Durations: checkpointFingerprintDurations{
			ChunkDurationInputSeconds: runInput.ChunkDuration, ClipDurationInputSeconds: runInput.ClipDuration,
			SecondsPerSegment: runInput.SecondsPerSegment, ChunkDurationConfigSec: cfg.ChunkDurationSec, ClipDurationConfigSec: cfg.ClipDurationSec,
		},
		PreviousOutput: deterministicPreviousOutputOrMarker(previous),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return sha256String(checkpointFingerprintV2Version + "|payload-serialization-error|" + jobID + "|" + stepName + "|" + err.Error())
	}
	return sha256String(string(raw))
}

func stepInputFingerprint(jobID, stepName string, cfg OrchestratorConfig, input *RunInput, previous *RunState) string {
	runInput := input
	if runInput == nil {
		runInput = &RunInput{}
	}
	if cfg.JobId == "" {
		cfg.JobId = jobID
	}
	if cfg.PolicyVersion == "" {
		cfg.PolicyVersion = runInput.PolicyVersion
	}
	if cfg.MaxConcurrentJobs <= 0 {
		cfg.MaxConcurrentJobs = DefaultMaxConcurrentJobs
	}

	payload, err := buildCheckpointFingerprintPayload(jobID, stepName, cfg, runInput, previous)
	if err != nil {
		return fingerprintSerializationFallback(jobID, stepName, err)
	}
	raw, err := marshalCheckpointFingerprintPayload(payload)
	if err != nil {
		return fingerprintSerializationFallback(jobID, stepName, err)
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

func fingerprintWindowsLegacy(input *RunInput, previous *RunState) []checkpointFingerprintWindow {
	windows := fingerprintWindows(input)
	if previous != nil {
		for _, plan := range previous.Plan {
			windows = append(windows, checkpointFingerprintWindow{URL: plan.SourceID, StartSec: plan.StartSec, EndSec: plan.EndSec})
		}
	}
	return windows
}

func fingerprintWindows(input *RunInput) []checkpointFingerprintWindow {
	windows := make([]checkpointFingerprintWindow, 0)
	if input != nil {
		for _, clip := range input.Clips {
			windows = append(windows, checkpointFingerprintWindow{URL: clip.URL, StartSec: clip.StartSec, EndSec: clip.EndSec})
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
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Filename  string `json:"filename"`
	MIMEType  string `json:"mime_type"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Required  bool   `json:"required"`
}

type deterministicManifest struct {
	SchemaVersion string                          `json:"schema_version"`
	WorkflowID    string                          `json:"workflow_id"`
	JobID         string                          `json:"job_id"`
	Artifacts     []deterministicManifestArtifact `json:"artifacts"`
}

// deterministicPreviousOutput excludes execution-time timestamps, local
// filesystem paths, temporary cut/compose paths, and remote links. It retains
// the logical output contract
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
				Required: artifact.Required,
			})
		}
	}
	projection := struct {
		Plan              []ClipPlan                 `json:"plan"`
		StagedAssets      []deterministicStagedAsset `json:"staged_assets"`
		Published         []deterministicChunkState  `json:"published"`
		MetadataPublished deterministicMetadataState `json:"metadata_published"`
		Manifest          deterministicManifest      `json:"manifest"`
		FinalStatus       job.Status                 `json:"final_status"`
		Counts            RunCounts                  `json:"counts"`
		SourceErrors      map[string]string          `json:"source_errors,omitempty"`
	}{
		Plan: state.Plan, StagedAssets: staged,
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

// ── Legacy checkpoint eligibility + resume fingerprint ──────────────
// legacyCheckpointEligible limits the original jobID|stepKey fingerprint
// migration to requests whose effective input is entirely zero-value. A v0 row
// carries no input hash at all, so the only safe answer to "could this row be
// the same work?" is to require that the CURRENT run carries none of the
// inputs a modern fingerprint would have covered: any explicit target, budget,
// output override, destination, lease or operator metadata marks a job the
// legacy build cannot have produced, and the v0 row must NOT be trusted.
//
// Without this gate, an old checkpoint with no input identity could incorrectly
// skip work for a new request. The decision is expressed as named dimensions
// (below) so each one is independently reviewable and tested; do NOT delete the
// legacy branch until persisted checkpoints and jobs that depend on it are
// proven absent.
func legacyCheckpointEligible(cfg OrchestratorConfig, input *RunInput) bool {
	if input == nil {
		return false
	}
	return legacyPolicySaltAbsent(cfg, input) &&
		legacyLeaseAbsent(cfg.Lease) &&
		legacyConfigDurationsAbsent(cfg) &&
		legacyExplicitTargetsAbsent(input) &&
		legacyBudgetOverridesAbsent(input) &&
		legacyRenderOverridesAbsent(input) &&
		legacyDestinationOverridesAbsent(input) &&
		legacyLeaseAbsent(input.FinalizationLease)
}

// legacyPolicySaltAbsent: a legacy-shaped run carries no fingerprint salt on
// either the resolved config or the request.
func legacyPolicySaltAbsent(cfg OrchestratorConfig, input *RunInput) bool {
	return cfg.PolicyVersion == "" && input.PolicyVersion == ""
}

// legacyLeaseAbsent: a legacy-shaped run is not broker-leased. Shared by the
// orchestrator lease (cfg.Lease) and the finalization lease
// (input.FinalizationLease) — both are the same finalization.Lease shape, and
// either one carrying an id, an attempt or an expiry marks a modern job.
func legacyLeaseAbsent(lease finalization.Lease) bool {
	return lease.LeaseID == "" && lease.JobID == "" && lease.WorkerID == "" && lease.Attempt == 0 && lease.ExpiresAt.IsZero()
}

// legacyConfigDurationsAbsent: the resolved config carries no duration budget.
func legacyConfigDurationsAbsent(cfg OrchestratorConfig) bool {
	return cfg.ChunkDurationSec == 0 && cfg.ClipDurationSec == 0
}

// legacyExplicitTargetsAbsent: the request names no source explicitly (no
// search query, direct URL, Drive URL or clip spec).
func legacyExplicitTargetsAbsent(input *RunInput) bool {
	return len(input.SearchQueries) == 0 && len(input.DirectURLs) == 0 && len(input.DriveURLs) == 0 && len(input.Clips) == 0
}

// legacyBudgetOverridesAbsent: the request carries no duration, budget,
// download-mode or max-video tuning of its own.
func legacyBudgetOverridesAbsent(input *RunInput) bool {
	return input.TotalMinutes == 0 && input.TargetTotalDurationSeconds == 0 && input.TargetDurationPerSourceSeconds == 0 &&
		input.ClipsPerSource == 0 && input.ClipDurationSeconds == 0 && input.DownloadMode == "" && input.MaxVideos == 0 &&
		input.ChunkDuration == 0 && input.ClipDuration == 0 && input.SecondsPerSegment == 0
}

// legacyRenderOverridesAbsent: the request does not override the rendering
// pipeline (audio/effects/transitions) or the output folder naming.
func legacyRenderOverridesAbsent(input *RunInput) bool {
	return !input.NoAudio && !input.NoEffects && !input.NoTransitions && input.Subfolder == "" && input.FolderName == ""
}

// legacyDestinationOverridesAbsent: the request carries no destination
// resolution, operator metadata or persist flag.
func legacyDestinationOverridesAbsent(input *RunInput) bool {
	return input.DriveFolderID == "" && input.FolderID == "" && !input.DriveFolderResolved && input.Metadata == nil && !input.Persist
}

// resumeCheckpointFingerprint returns a stored legacy fingerprint only when
// its compatibility contract matches the current request. All other cases
// retain the current fingerprint so the step executes as a new attempt.
func resumeCheckpointFingerprint(jobID, stepName string, cfg OrchestratorConfig, input *RunInput, previous *RunState, row steps.StepState, hasRow bool) string {
	fingerprint := stepInputFingerprint(jobID, stepName, cfg, input, previous)
	if !hasRow {
		return fingerprint
	}
	switch {
	case row.Fingerprint == legacyStepInputFingerprint(jobID, stepName) && legacyCheckpointEligible(cfg, input):
		return row.Fingerprint
	case row.Fingerprint == legacyV2StepInputFingerprint(jobID, stepName, cfg, input, previous):
		return row.Fingerprint
	default:
		return fingerprint
	}
}

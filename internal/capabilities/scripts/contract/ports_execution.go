package contract

import (
	"context"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
)

// LocalizedRenderResult is the certified outcome of one localized render
// fan-out: the produced video artifact identity (asset id, sha256, Drive
// link, duration) for the (scene, language) unit. It is projected verbatim
// from the localization service's certified artifact — the run records the
// produced MP4 instead of discarding it.
type LocalizedRenderResult struct {
	SceneID    string   `json:"scene_id"`
	SceneIndex int      `json:"scene_index,omitempty"`
	Language   Language `json:"language"`
	ClipID     string   `json:"clip_id"`
	// RenderJobID is the render-plan identity of the job that produced these
	// bytes (localization's plan revision: `<clip>:<language>:overlay-v5:`
	// + the plan fingerprint prefix — `:` separators because the revision IS
	// the RenderingGen queue job id, which must be one valid filename
	// component). It is projected here so the run result
	// NAMES its render job: before this field existed a collector could only
	// recover the id by scraping `plan_revision` out of the master log, which
	// worked on a fresh run and returned "no queue record resolved" on a run
	// that reused cached renders.
	RenderJobID string `json:"render_job_id,omitempty"`
	AssetID     string `json:"asset_id"`
	SHA256      string `json:"sha256"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	// DriveFolderID is the RESOLVED destination folder the artifact was
	// published into (<job>/<language> under the clips root). It is projected so
	// the destination of a render is readable on the run result instead of
	// only being inferable from the log — and so a gate can prove that two
	// languages of the same clip did NOT land in one shared folder. Empty on
	// the pre-upload (RENDERED) stage, which has no destination yet.
	DriveFolderID string `json:"drive_folder_id,omitempty"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	// SizeBytes / StorageKey / ArtifactURL are the certified durable identity
	// of the rendered MP4. They travel together so a final-job handoff can
	// reference the bytes in the shared object store without a Producer-local
	// os.Stat, re-hash or re-upload. LocalPath remains a runtime-only hint for
	// consumers that genuinely need the file (Drive publication, muxing).
	SizeBytes   int64  `json:"size_bytes,omitempty"`
	StorageKey  string `json:"storage_key,omitempty"`
	ArtifactURL string `json:"artifact_url,omitempty"`
	LocalPath   string `json:"local_path,omitempty"`
	Status      string `json:"status"`
	Backend     string `json:"backend,omitempty"`
	// Reused / RenderSource state WHERE these bytes came from: "fresh_gpu" for
	// a render this run executed, "deterministic_render_cache" for a verified
	// reuse of an identical plan. Without them a dedup run (metrics: null, a
	// few seconds of Drive I/O per clip) is indistinguishable from a fresh
	// render that reported no timings — the exact question "where did the time
	// go?" the per-render report exists to answer.
	Reused       bool               `json:"reused,omitempty"`
	RenderSource string             `json:"render_source,omitempty"`
	Metrics      map[string]float64 `json:"metrics,omitempty"`
	// Boundary timestamps let the parent distinguish summed child work from
	// actual fan-out wall time when localized renders overlap. StartedAt is
	// ADMISSION (the render slot was granted), not the call's submission: the
	// wait before it is QueueWaitMS, so WallMS describes the unit's own work
	// (render + publication) and never the lane's queueing.
	StartedAt   time.Time `json:"started_at,omitempty"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	WallMS      int64     `json:"wall_ms,omitempty"`
	QueueWaitMS int64     `json:"queue_wait_ms,omitempty"`

	// CheckpointFingerprint is computed from the enqueue inputs that determine
	// these bytes. It is in-process only (never serialized), so the envelope
	// body stays byte-identical to the per-artifact checkpoint payload while
	// the writer still records a meaningful resume identity.
	CheckpointFingerprint string `json:"-"`
}

type LocalizedRenderFailure struct {
	SceneID   string   `json:"scene_id"`
	Language  Language `json:"language"`
	ClipID    string   `json:"clip_id,omitempty"`
	ErrorCode string   `json:"error_code"`
	Error     string   `json:"error"`
}

// AudioAssetSource resolves ONE background-music or sound-effect asset_id
// to its canonical ResolvedAudioAsset (verified local path + certified
// source duration). The concrete adapter (composition root) reads the
// canonical asset registry and materializes Drive sources into scratch;
// the capability never imports Drive, SQLite, or the filesystem mechanics.
// Fail-closed: an unknown asset_id or an asset with no usable local copy is
// a typed error, never a silent empty path.
type AudioAssetSource interface {
	ResolveAudioAsset(ctx context.Context, assetID string) (audio.ResolvedAudioAsset, error)
}

// ClipAudioAssetSource is the optional production resolver for original clip
// audio. It is deliberately separate from AudioAssetSource because BGM/SFX
// resolution and clip-source materialization have different contracts.
type ClipAudioAssetSource interface {
	ResolveClipAudioAsset(ctx context.Context, assetID string) (audio.ResolvedAudioAsset, error)
}

package localization

// artifact.go owns LocalizedClipArtifact — the certified per-language output
// of a localized render. It is the single shape the render boundary returns
// (never a bare path like "/tmp/es.mp4") and that the Drive uploader and the
// Docs writer consume.
//
// godlike/06 SSOT (one canonical owner per fact): the artifact is the ONE
// object that carries a localized clip's identity, provenance (plan
// fingerprint), bytes (sha256 / size / duration), codecs, Drive location, and
// lifecycle status. The Docs writer receives this object verbatim instead of
// inferring "which link belongs to which language" from ad-hoc strings.
//
// godlike/07 no-fake-availability: DriveFileID / DriveLink / LocalPath are
// omitempty — they are present ONLY once the corresponding step actually ran
// (upload / materialization). A not-yet-uploaded artifact carries Status
// below LocalizedClipUploaded and no fabricated Drive link.

// LocalizedClipArtifactVersion is the canonical version of the
// localized-clip artifact contract.
const LocalizedClipArtifactVersion = "localized-clip-artifact.v1"

// LocalizedClipStatus is the lifecycle of a localized clip artifact, from
// PENDING through the terminal UPLOADED (success) or FAILED states.
type LocalizedClipStatus string

const (
	// LocalizedClipPending is the initial state: the artifact does not exist
	// yet and no work has been recorded.
	LocalizedClipPending LocalizedClipStatus = "PENDING"
	// LocalizedClipReady means the localized inputs are resolved (transcript
	// + translated text tracks) and the plan can legally be built/enqueued.
	LocalizedClipReady LocalizedClipStatus = "READY"
	// LocalizedClipQueued means the plan is enqueued, waiting for a render
	// worker slot.
	LocalizedClipQueued LocalizedClipStatus = "QUEUED"
	// LocalizedClipRendering means the render boundary (Rust) is executing
	// the plan.
	LocalizedClipRendering LocalizedClipStatus = "RENDERING"
	// LocalizedClipRendered means the local bytes are produced and validated
	// against the output contract.
	LocalizedClipRendered LocalizedClipStatus = "RENDERED"
	// LocalizedClipUploaded is the terminal success state: the validated
	// bytes are uploaded to Drive and DriveFileID / DriveLink are set.
	LocalizedClipUploaded LocalizedClipStatus = "UPLOADED"
	// LocalizedClipFailed is the terminal failure state: the artifact will
	// never reach UPLOADED without a corrected re-run.
	LocalizedClipFailed LocalizedClipStatus = "FAILED"
)

// LocalizedClipArtifact is the canonical certified output of one localized
// render. Every field is a fact observed by the pipeline — the renderer
// reports codecs/duration/size from ffprobe, the uploader reports the Drive
// id/link, and the fingerprint links the artifact back to the exact
// LocalizedClipPlan that produced it.
type LocalizedClipArtifact struct {
	// Version pins the contract shape. Always LocalizedClipArtifactVersion.
	Version string `json:"version"`

	// ── Identity ─────────────────────────────────────────────────
	// JobID correlates the artifact to its enclosing Master job.
	JobID string `json:"job_id"`
	// SceneID is the editorial scene the clip belongs to (may be empty
	// for standalone clips).
	SceneID string `json:"scene_id"`
	// ClipID is the canonical clip identity that was localized.
	ClipID string `json:"clip_id"`

	// ── Language + provenance ────────────────────────────────────
	// Language is the BCP-47 language this artifact renders into (e.g.
	// "es"). It is the key Docs uses to associate a link to a language.
	Language string `json:"language"`
	// PlanFingerprint is the canonical LocalizedClipPlan.Fingerprint of the
	// plan that produced this artifact — provenance, not recomputed here.
	PlanFingerprint string `json:"plan_fingerprint"`

	// ── Asset identity ───────────────────────────────────────────
	// AssetID is the canonical derived-asset id assigned once the artifact
	// is committed to the asset registry.
	AssetID string `json:"asset_id"`

	// ── Bytes ────────────────────────────────────────────────────
	// LocalPath is the local filesystem path of the rendered file. Omitted
	// once the bytes are no longer staged locally (uploaded / Docs-only).
	LocalPath string `json:"local_path,omitempty"`
	// SHA256 is the content hash of the rendered bytes.
	SHA256 string `json:"sha256"`
	// SizeBytes is the rendered file size in bytes.
	SizeBytes int64 `json:"size_bytes"`
	// DurationMS is the rendered clip duration in milliseconds.
	DurationMS int64 `json:"duration_ms"`

	// ── Media facts (from ffprobe) ───────────────────────────────
	// VideoCodec is the video codec of the rendered stream (e.g. "h264").
	VideoCodec string `json:"video_codec"`
	// AudioCodec is the audio codec of the rendered stream (e.g. "aac").
	AudioCodec string `json:"audio_codec"`

	// ── Drive location ───────────────────────────────────────────
	// DriveFileID is the uploaded Drive file id. Omitted until upload.
	DriveFileID string `json:"drive_file_id,omitempty"`
	// DriveLink is the uploaded Drive web-view link. Omitted until upload.
	DriveLink string `json:"drive_link,omitempty"`

	// ── Lifecycle ────────────────────────────────────────────────
	// Status is the artifact lifecycle state (LocalizedClipPending …
	// LocalizedClipUploaded / LocalizedClipFailed).
	Status LocalizedClipStatus `json:"status"`
}

// artifact_checkpoint.go — durable per-artifact checkpoint contract.
//
// This is item 9's missing repository API: one row per completed artifact,
// not one growing snapshot per run. It is deliberately separate from
// RunRepository.SavePartialResult (the whole-result recovery envelope) and
// from the generic checkpoint.Store (the stage/unit completion authority).
//
// Ownership:
//
//	run_artifact_checkpoints  this file's port + SQLite adapter
//	                          → the artifact BODY needed to rebuild a result
//	job_checkpoints           capabilities/checkpoint.Store
//	                          → "has (job, stage, unit) completed?" + artifact
//	                            byte verification for resume
//	artifact_stages           publication saga (STAGED → PUBLISHED → SUCCEEDED)
//	workflow_payload_json     best-effort whole-result recovery envelope;
//	                          remains the safety net until the per-artifact
//	                          loaders are wired for every kind
//
// A row exists exactly when its artifact is READY. Invalidate deletes it, so
// there is no second status vocabulary to keep in sync.
package script

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	// ArtifactCheckpointSchemaVersion pins the payload contract accepted by
	// this API. A row from another version is not reused; it must be
	// rewritten by the current producer rather than parsed optimistically.
	ArtifactCheckpointSchemaVersion = 1

	// MaxArtifactCheckpointPayloadBytes bounds one artifact body. Large media
	// stays behind ArtifactURI + ArtifactSHA256; a checkpoint that grows past
	// this limit is a whole-result snapshot in disguise and is rejected.
	MaxArtifactCheckpointPayloadBytes = 8 << 20
)

var (
	// ErrInvalidArtifactCheckpoint is the single sentinel for a structurally
	// incomplete checkpoint. Adapters must return it (wrapped) before touching
	// the database.
	ErrInvalidArtifactCheckpoint = errors.New("scriptgeneration: invalid artifact checkpoint")

	// ErrArtifactCheckpointCorrupt marks a stored payload whose bytes no
	// longer match payload_sha256. A reader must never decode or reuse such a
	// row; the unit is recomputed.
	ErrArtifactCheckpointCorrupt = errors.New("scriptgeneration: artifact checkpoint payload digest mismatch")
)

// ArtifactCheckpointKind is the canonical artifact vocabulary. Kinds are
// opaque strings to the SQL column (adding one does not require a migration),
// but the API accepts only known values so a typo cannot create a checkpoint
// nothing can load.
type ArtifactCheckpointKind string

const (
	ArtifactCheckpointSceneText       ArtifactCheckpointKind = "scene_text"
	ArtifactCheckpointTranslation     ArtifactCheckpointKind = "translation"
	ArtifactCheckpointVoiceover       ArtifactCheckpointKind = "voiceover"
	ArtifactCheckpointOverlayPlan     ArtifactCheckpointKind = "overlay_plan"
	ArtifactCheckpointOverlayRender   ArtifactCheckpointKind = "overlay_render"
	ArtifactCheckpointLocalizedRender ArtifactCheckpointKind = "localized_render"
	ArtifactCheckpointFinalAudio      ArtifactCheckpointKind = "final_audio"
	ArtifactCheckpointFinalJobPlan    ArtifactCheckpointKind = "final_job_plan"
	ArtifactCheckpointFinalJobReceipt ArtifactCheckpointKind = "final_job_receipt"
)

// Valid reports whether the kind belongs to the current vocabulary.
func (k ArtifactCheckpointKind) Valid() bool {
	switch k {
	case ArtifactCheckpointSceneText,
		ArtifactCheckpointTranslation,
		ArtifactCheckpointVoiceover,
		ArtifactCheckpointOverlayPlan,
		ArtifactCheckpointOverlayRender,
		ArtifactCheckpointLocalizedRender,
		ArtifactCheckpointFinalAudio,
		ArtifactCheckpointFinalJobPlan,
		ArtifactCheckpointFinalJobReceipt:
		return true
	default:
		return false
	}
}

// ArtifactCheckpoint is one completed artifact of one run. Payload is the
// typed artifact body serialized by the producer; it is intentionally opaque
// to this contract so a new artifact kind does not change the repository API.
//
// PayloadSHA256 and CreatedAt/UpdatedAt are read-side values. A writer may
// leave them empty/zero: the adapter computes the digest and stamps the
// timestamps. A non-empty PayloadSHA256 must already match Payload.
type ArtifactCheckpoint struct {
	RunID              string
	Kind               ArtifactCheckpointKind
	Key                string
	SchemaVersion      int
	InputFingerprint   string
	ProcessorVersion   string
	ArtifactID         string
	ArtifactSHA256     string
	ArtifactURI        string
	ArtifactSizeBytes  int64
	ArtifactDurationUS int64
	Payload            json.RawMessage
	PayloadSHA256      string
	Attempt            int
	CompletedAt        time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CanonicalPayload is the exact byte sequence the repository stores and
// hashes. An empty payload is stored as the JSON object {}, never as SQL NULL
// or the JSON literal null.
func (c ArtifactCheckpoint) CanonicalPayload() []byte {
	if len(c.Payload) == 0 {
		return []byte("{}")
	}
	return c.Payload
}

// PayloadDigest is the SHA-256 of CanonicalPayload.
func (c ArtifactCheckpoint) PayloadDigest() string {
	return digest.SHA256Bytes(c.CanonicalPayload())
}

// Validate fails closed on a checkpoint that cannot identify its run/artifact,
// cannot be verified later, or has a body the repository must not store.
func (c ArtifactCheckpoint) Validate() error {
	if c.RunID == "" || strings.TrimSpace(c.RunID) != c.RunID {
		return fmt.Errorf("%w: run_id is required and must not be padded", ErrInvalidArtifactCheckpoint)
	}
	if !c.Kind.Valid() {
		return fmt.Errorf("%w: unknown artifact kind %q", ErrInvalidArtifactCheckpoint, c.Kind)
	}
	if c.Key == "" || strings.TrimSpace(c.Key) != c.Key {
		return fmt.Errorf("%w: artifact key is required and must not be padded", ErrInvalidArtifactCheckpoint)
	}
	if c.SchemaVersion != ArtifactCheckpointSchemaVersion {
		return fmt.Errorf("%w: schema_version is %d, want %d", ErrInvalidArtifactCheckpoint, c.SchemaVersion, ArtifactCheckpointSchemaVersion)
	}
	if c.InputFingerprint == "" || strings.TrimSpace(c.InputFingerprint) != c.InputFingerprint {
		return fmt.Errorf("%w: input_fingerprint is required and must not be padded", ErrInvalidArtifactCheckpoint)
	}
	if c.ProcessorVersion == "" || strings.TrimSpace(c.ProcessorVersion) != c.ProcessorVersion {
		return fmt.Errorf("%w: processor_version is required and must not be padded", ErrInvalidArtifactCheckpoint)
	}
	if c.ArtifactID != "" && strings.TrimSpace(c.ArtifactID) != c.ArtifactID {
		return fmt.Errorf("%w: artifact_id must not be padded", ErrInvalidArtifactCheckpoint)
	}
	if c.ArtifactSHA256 != "" && !digest.IsCanonicalSHA256(c.ArtifactSHA256) {
		return fmt.Errorf("%w: artifact_sha256 must be a canonical SHA-256", ErrInvalidArtifactCheckpoint)
	}
	if c.ArtifactURI != "" && strings.TrimSpace(c.ArtifactURI) != c.ArtifactURI {
		return fmt.Errorf("%w: artifact_uri must not be padded", ErrInvalidArtifactCheckpoint)
	}
	if c.ArtifactSizeBytes < 0 {
		return fmt.Errorf("%w: artifact_size_bytes must not be negative", ErrInvalidArtifactCheckpoint)
	}
	if c.ArtifactDurationUS < 0 {
		return fmt.Errorf("%w: artifact_duration_us must not be negative", ErrInvalidArtifactCheckpoint)
	}
	payload := c.CanonicalPayload()
	if !json.Valid(payload) {
		return fmt.Errorf("%w: payload is not valid JSON", ErrInvalidArtifactCheckpoint)
	}
	if len(payload) > MaxArtifactCheckpointPayloadBytes {
		return fmt.Errorf("%w: payload is %d bytes, limit is %d", ErrInvalidArtifactCheckpoint, len(payload), MaxArtifactCheckpointPayloadBytes)
	}
	if c.PayloadSHA256 != "" {
		if !digest.IsCanonicalSHA256(c.PayloadSHA256) || c.PayloadSHA256 != c.PayloadDigest() {
			return fmt.Errorf("%w: payload_sha256 does not match the payload bytes", ErrInvalidArtifactCheckpoint)
		}
	}
	if c.Attempt < 0 {
		return fmt.Errorf("%w: attempt must not be negative", ErrInvalidArtifactCheckpoint)
	}
	if c.CompletedAt.IsZero() {
		return fmt.Errorf("%w: completed_at is required", ErrInvalidArtifactCheckpoint)
	}
	return nil
}

// ReusableFor answers the pure part of the resume decision: a recorded
// artifact body may be reused only when the schema, the input fingerprint and
// the processor version all match. External byte verification stays with
// checkpoint.ArtifactVerifier; this method never claims bytes still exist.
func (c ArtifactCheckpoint) ReusableFor(inputFingerprint, processorVersion string) (bool, string) {
	if c.SchemaVersion != ArtifactCheckpointSchemaVersion {
		return false, "checkpoint schema version changed"
	}
	if c.InputFingerprint != inputFingerprint {
		return false, "input fingerprint changed"
	}
	if c.ProcessorVersion != processorVersion {
		return false, "processor version changed"
	}
	return true, ""
}

// ArtifactCheckpointKey joins the identity segments of one artifact with the
// canonical "/" separator. Segments containing "/" are rejected so a key can
// always be split back into its parts unambiguously.
func ArtifactCheckpointKey(parts ...string) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("%w: artifact key requires at least one segment", ErrInvalidArtifactCheckpoint)
	}
	for _, part := range parts {
		if part == "" || strings.TrimSpace(part) != part || strings.Contains(part, "/") {
			return "", fmt.Errorf("%w: invalid artifact key segment %q", ErrInvalidArtifactCheckpoint, part)
		}
	}
	return strings.Join(parts, "/"), nil
}

// ArtifactCheckpointRepository is the durable API for per-artifact
// checkpoints. Implementations must be safe for concurrent use.
//
// Contract:
//   - Save upserts on (run_id, artifact_kind, artifact_key). Re-saving the
//     same identity converges on one row; it never appends a second version.
//     The adapter validates, computes PayloadSHA256, and stamps
//     CreatedAt/UpdatedAt; it must not store an unvalidated checkpoint.
//   - Get returns (nil, nil) when no row exists. A stored row whose payload
//     digest does not verify is an error wrapping ErrArtifactCheckpointCorrupt,
//     never a nil miss.
//   - List returns every row of one run ordered by (artifact_kind,
//     artifact_key). It is the resume scan; callers filter in memory or use
//     Get for one artifact.
//   - Invalidate deletes one row. Deleting a missing row is a no-op so a
//     stale-checkpoint cleanup is idempotent.
type ArtifactCheckpointRepository interface {
	SaveArtifactCheckpoint(ctx context.Context, checkpoint ArtifactCheckpoint) error
	GetArtifactCheckpoint(ctx context.Context, runID string, kind ArtifactCheckpointKind, key string) (*ArtifactCheckpoint, error)
	ListArtifactCheckpoints(ctx context.Context, runID string) ([]ArtifactCheckpoint, error)
	InvalidateArtifactCheckpoint(ctx context.Context, runID string, kind ArtifactCheckpointKind, key string) error
}

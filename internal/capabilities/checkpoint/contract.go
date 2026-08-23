// Package checkpoint owns the durable per-unit checkpoint contract: the
// canonical record of "this job completed this work unit" that survives
// crashes and restarts. It is deliberately SEPARATE from the runner's
// best-effort workflow checkpoint (SavePartialResult): that one restores
// general workflow state, this one is the authority for resume decisions —
// each stage (research, script, audio, render_scene, assemble, publish)
// records one checkpoint per work unit (unit_id = "global" for whole-job
// stages, "scene_01" etc. for per-scene stages).
//
// Cache ≠ Checkpoint:
//
//	CACHE      → "does this result exist somewhere?" (job-agnostic, reusable)
//	CHECKPOINT → "has THIS job completed THIS unit?" (job-specific)
//
// The checkpoint carries the unit's input fingerprint so resume can verify
// that the recorded completion is still valid for the current inputs (see
// the CanResume logic in the resume step: fingerprint + artifact existence
// + artifact SHA256 + processor version must all match before SKIP).
//
// The store is intentionally single: one CheckpointStore for every stage,
// never per-stage stores (no ResearchCheckpointStore / AudioCheckpointStore
// / RenderCheckpointStore duplication).
package checkpoint

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Canonical stage vocabulary shared by every workflow stage. Stages are
// opaque strings to the store (any stage is accepted); these constants are
// the single canonical spelling used by the callers.
const (
	StageResearch        = "research"
	StageScript          = "script"
	StageClips           = "clips"
	StageAssetResolution = "asset_resolution"
	StageAudio           = "audio"
	StageRenderScene     = "render_scene"
	StageAssemble        = "assemble"
	StagePublish         = "publish"
)

// UnitGlobal is the unit id for whole-job stages (one unit per job); scene
// stages use the scene id as unit id.
const UnitGlobal = "global"

// StatusCompleted is the only durable completion state today. Rows are
// written by Complete and removed by Invalidate (an invalidated checkpoint
// is indistinguishable from a missing one for resume).
const StatusCompleted = "COMPLETED"

var (
	ErrInvalidCheckpoint = errors.New("invalid checkpoint")
	ErrNotWired          = errors.New("checkpoint store: not wired")
)

// Checkpoint is one durable completion record for a (job, stage, unit).
// ArtifactSHA256/ArtifactURI are empty for stages that produce no artifact
// (e.g. research); when set, they pin the exact artifact bytes the unit
// produced so resume can verify the artifact still exists and matches.
type Checkpoint struct {
	JobID            string
	Stage            string
	UnitID           string
	InputFingerprint string
	Status           string
	ArtifactSHA256   string
	ArtifactURI      string
	ProcessorVersion string
	CompletedAt      time.Time
}

// Validate fails closed on a structurally incomplete checkpoint: a
// checkpoint that cannot identify its (job, stage, unit) or its input
// fingerprint must never be persisted.
func (c Checkpoint) Validate() error {
	if strings.TrimSpace(c.JobID) == "" || strings.TrimSpace(c.Stage) == "" || strings.TrimSpace(c.UnitID) == "" {
		return fmt.Errorf("%w: job_id, stage and unit_id are required", ErrInvalidCheckpoint)
	}
	if strings.TrimSpace(c.InputFingerprint) == "" {
		return fmt.Errorf("%w: input_fingerprint is required", ErrInvalidCheckpoint)
	}
	if strings.TrimSpace(c.Status) == "" {
		return fmt.Errorf("%w: status is required", ErrInvalidCheckpoint)
	}
	if strings.TrimSpace(c.ProcessorVersion) == "" {
		return fmt.Errorf("%w: processor_version is required", ErrInvalidCheckpoint)
	}
	if c.ArtifactSHA256 != "" && !isSHA256(c.ArtifactSHA256) {
		return fmt.Errorf("%w: artifact_sha256 must be a valid SHA256", ErrInvalidCheckpoint)
	}
	if c.CompletedAt.IsZero() {
		return fmt.Errorf("%w: completed_at is required", ErrInvalidCheckpoint)
	}
	return nil
}

// Store is the durable checkpoint port. Get returns (nil, nil) when the
// unit has no checkpoint; Complete upserts the completion record (idempotent
// re-completion converges on the same row); Invalidate removes the record
// so the unit is treated as not completed.
type Store interface {
	Get(ctx context.Context, jobID, stage, unitID string) (*Checkpoint, error)
	Complete(ctx context.Context, checkpoint Checkpoint) error
	Invalidate(ctx context.Context, jobID, stage, unitID string) error
}

func isSHA256(value string) bool {
	if len(value) != digest.SHA256HexLength {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

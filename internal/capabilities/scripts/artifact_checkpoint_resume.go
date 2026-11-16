// artifact_checkpoint_resume.go — per-artifact loader for localized_render.
//
// The loader is deliberately separate from the dual-write. A deployment can
// record rows first (dual-write) and only later trust them for recovery
// (resume). The envelope remains the first authority: callers still pass
// LocalizedRenderResult values restored from workflow_payload_json, and this
// loader is consulted only when that in-memory envelope has no staged render.
package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	capcheckpoint "github.com/Marcuss-ops/PipelineGen/internal/capabilities/checkpoint"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

// SetArtifactCheckpointResume enables reading localized_render checkpoints.
// It is nil-safe and has no effect without a repository.
func (r *Runner) SetArtifactCheckpointResume(enabled bool) {
	if r != nil {
		r.artifactCheckpointResume = enabled
	}
}

// SetArtifactCheckpointVerifier wires the external verifier used for a
// checkpoint whose artifact was already published (UPLOADED). A nil verifier
// blocks reuse of such a row rather than trusting the recorded metadata.
func (r *Runner) SetArtifactCheckpointVerifier(verifier capcheckpoint.ArtifactVerifier) {
	if r != nil {
		r.artifactCheckpointVerifier = verifier
	}
}

// prepareLocalizedRenderInput attaches the input fingerprint to the sinks and,
// when resume is enabled, restores a reusable checkpoint into ResumeFrom. The
// envelope value already present on the input wins over the store.
func (r *Runner) prepareLocalizedRenderInput(ctx context.Context, in LocalizedRenderInput) (LocalizedRenderInput, error) {
	fingerprint := localizedRenderCheckpointFingerprint(in)
	in = attachLocalizedRenderCheckpointFingerprint(in, fingerprint)

	// A staged envelope result is the legacy recovery path and stays
	// authoritative. It still receives the fingerprint so the writer records
	// a v2 row after the upload.
	if in.ResumeFrom != nil {
		restored := *in.ResumeFrom
		restored.CheckpointFingerprint = fingerprint
		in.ResumeFrom = &restored
		return in, nil
	}
	if r == nil || !r.artifactCheckpointResume || r.artifactCheckpoints == nil {
		return in, nil
	}
	restored, err := r.loadLocalizedRenderCheckpoint(ctx, in, fingerprint)
	if err != nil {
		return in, err
	}
	if restored != nil {
		in.ResumeFrom = restored
	}
	return in, nil
}

func attachLocalizedRenderCheckpointFingerprint(in LocalizedRenderInput, fingerprint string) LocalizedRenderInput {
	if ready := in.OnRenderReady; ready != nil {
		in.OnRenderReady = func(rendered LocalizedRenderResult) error {
			rendered.CheckpointFingerprint = fingerprint
			return ready(rendered)
		}
	}
	if renderedSink := in.OnRendered; renderedSink != nil {
		in.OnRendered = func(rendered LocalizedRenderResult) error {
			rendered.CheckpointFingerprint = fingerprint
			return renderedSink(rendered)
		}
	}
	return in
}

// loadLocalizedRenderCheckpoint returns a reusable artifact body or nil when
// the unit must execute. Definitive staleness (changed inputs, corrupt body,
// missing artifact) invalidates the row; an unjudgeable artifact (verifier
// error or not wired) returns nil WITHOUT deleting the row.
func (r *Runner) loadLocalizedRenderCheckpoint(ctx context.Context, in LocalizedRenderInput, fingerprint string) (*LocalizedRenderResult, error) {
	key, err := ArtifactCheckpointKey(in.SceneID, in.ClipID, string(in.Language))
	if err != nil {
		return nil, fmt.Errorf("localized render resume: artifact checkpoint key: %w", err)
	}
	checkpoint, err := r.artifactCheckpoints.GetArtifactCheckpoint(ctx, in.RunID, ArtifactCheckpointLocalizedRender, key)
	if err != nil {
		if errors.Is(err, ErrArtifactCheckpointCorrupt) {
			r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "payload digest mismatch")
			return nil, nil
		}
		// A read outage falls back to the envelope/recompute path. The write
		// side is fail-closed; a read must not turn a transient DB error into
		// an unverified reuse.
		r.localizedRenderResumeLog().Warn("localized render checkpoint read failed; executing unit",
			zap.String("run_id", in.RunID), zap.String("scene_id", in.SceneID),
			zap.String("clip_id", in.ClipID), zap.String("language", string(in.Language)), zap.Error(err))
		return nil, nil
	}
	if checkpoint == nil {
		return nil, nil
	}
	if ok, reason := checkpoint.ReusableFor(fingerprint, localizedRenderCheckpointProcessorVersion); !ok {
		r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, reason)
		return nil, nil
	}
	var payload LocalizedRenderResult
	if err := json.Unmarshal(checkpoint.Payload, &payload); err != nil {
		r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "payload is not a localized render result")
		return nil, nil
	}
	if payload.SceneID != in.SceneID || payload.ClipID != in.ClipID || payload.Language != in.Language {
		r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "artifact identity does not match the requested unit")
		return nil, nil
	}
	if !digest.IsCanonicalSHA256(payload.SHA256) {
		r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "payload has no canonical artifact digest")
		return nil, nil
	}
	if checkpoint.ArtifactSHA256 != "" && !strings.EqualFold(checkpoint.ArtifactSHA256, payload.SHA256) {
		r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "checkpoint and payload digests differ")
		return nil, nil
	}

	switch strings.ToUpper(strings.TrimSpace(payload.Status)) {
	case "RENDERED":
		if !localizedRenderStagedBytesStillMatch(payload) {
			r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "staged artifact bytes are missing or changed")
			return nil, nil
		}
	case "UPLOADED":
		if strings.TrimSpace(payload.DriveFileID) == "" && strings.TrimSpace(payload.DriveLink) == "" {
			r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "uploaded artifact has no Drive identity")
			return nil, nil
		}
		if r.artifactCheckpointVerifier == nil {
			r.localizedRenderResumeLog().Warn("localized render checkpoint not reused (artifact verifier not configured)",
				zap.String("run_id", in.RunID), zap.String("scene_id", in.SceneID),
				zap.String("clip_id", in.ClipID), zap.String("language", string(in.Language)))
			return nil, nil
		}
		status, verifyErr := r.artifactCheckpointVerifier.VerifyArtifact(ctx, payload.SHA256, checkpoint.ArtifactURI)
		if verifyErr != nil {
			r.localizedRenderResumeLog().Warn("localized render checkpoint not reused (artifact verification failed)",
				zap.String("run_id", in.RunID), zap.String("scene_id", in.SceneID),
				zap.String("clip_id", in.ClipID), zap.String("language", string(in.Language)), zap.Error(verifyErr))
			return nil, nil
		}
		if !status.Exists || !status.SHA256Matches {
			r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "published artifact is missing or changed")
			return nil, nil
		}
	default:
		r.invalidateLocalizedRenderCheckpoint(ctx, in.RunID, key, "artifact status is not resumable")
		return nil, nil
	}

	payload.CheckpointFingerprint = fingerprint
	r.localizedRenderResumeLog().Info("localized render checkpoint reused",
		zap.String("run_id", in.RunID), zap.String("scene_id", in.SceneID),
		zap.String("clip_id", in.ClipID), zap.String("language", string(in.Language)),
		zap.String("status", payload.Status), zap.String("sha256", payload.SHA256))
	return &payload, nil
}

func localizedRenderStagedBytesStillMatch(rendered LocalizedRenderResult) bool {
	path := strings.TrimSpace(rendered.LocalPath)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return false
	}
	sum, _, err := digest.SHA256File(path)
	if err != nil {
		return false
	}
	return strings.EqualFold(sum, rendered.SHA256)
}

func (r *Runner) invalidateLocalizedRenderCheckpoint(ctx context.Context, runID, key, reason string) {
	if r == nil || r.artifactCheckpoints == nil {
		return
	}
	if err := r.artifactCheckpoints.InvalidateArtifactCheckpoint(ctx, runID, ArtifactCheckpointLocalizedRender, key); err != nil {
		r.localizedRenderResumeLog().Warn("localized render checkpoint invalidation failed",
			zap.String("run_id", runID), zap.String("key", key), zap.String("reason", reason), zap.Error(err))
		return
	}
	r.localizedRenderResumeLog().Info("localized render checkpoint invalidated",
		zap.String("run_id", runID), zap.String("key", key), zap.String("reason", reason))
}

func (r *Runner) localizedRenderResumeLog() *zap.Logger {
	if r != nil && r.log != nil {
		return r.log
	}
	return zap.NewNop()
}

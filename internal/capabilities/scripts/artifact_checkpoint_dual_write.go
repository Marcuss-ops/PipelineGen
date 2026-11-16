// artifact_checkpoint_dual_write.go — first rollout step for item 9.
//
// Only localized_render is dual-written. The artifact is already certified at
// this seam (recordLocalizedRenderReady has the rendered bytes; the later
// recordLocalizedRender has the published identity), so the checkpoint body
// is the same LocalizedRenderResult the whole-result envelope stores. Dual-write
// keeps the envelope as the recovery authority. Cutover then removes the
// per-unit envelope write: the checkpoint becomes the authority for the unit,
// while stage boundaries still persist the whole result.
package scriptgeneration

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	kernelscript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

const (
	// ArtifactCheckpointDualWriteEnv enables the localized_render dual-write.
	// Empty/0/false/off keeps the legacy envelope-only path. Any other value
	// is rejected at wiring time rather than silently enabling a typo.
	ArtifactCheckpointDualWriteEnv = "SCRIPT_ARTIFACT_CHECKPOINT_DUAL_WRITE"

	// ArtifactCheckpointResumeEnv enables the localized_render checkpoint
	// loader. It is separate from the dual-write so a deployment can record
	// rows first and only then trust them for resume.
	ArtifactCheckpointResumeEnv = "SCRIPT_ARTIFACT_CHECKPOINT_RESUME"

	// ArtifactCheckpointCutoverEnv removes the per-unit whole-result envelope
	// write for localized_render. It is the last rollout step: the per-unit
	// checkpoint must already be recorded AND trusted, and the published
	// artifact verifier must be wired. Stage boundaries keep writing the
	// envelope, so the result is never unrecoverable.
	ArtifactCheckpointCutoverEnv = "SCRIPT_ARTIFACT_CHECKPOINT_CUTOVER"

	// localizedRenderCheckpointProcessorVersion identifies the code that
	// produced the checkpoint body. v2 fingerprints the ENQUEUE inputs (the
	// v1 fingerprint incorrectly included RenderJobID, an output). Rows from
	// v1 are therefore not reusable and are fetched by the resumed unit.
	localizedRenderCheckpointProcessorVersion = "localized-render/v2"
)

// SetArtifactCheckpointRepository wires the optional per-artifact store. Nil
// keeps the rollout disabled even if the boolean flag is set.
func (r *Runner) SetArtifactCheckpointRepository(repo ArtifactCheckpointRepository) {
	if r != nil {
		r.artifactCheckpoints = repo
	}
}

// SetArtifactCheckpointDualWrite enables the localized_render dual-write. It
// is intentionally separate from the repository setter so a deployment can
// attach the store without writing rows until the rollout flag is turned on.
func (r *Runner) SetArtifactCheckpointDualWrite(enabled bool) {
	if r != nil {
		r.artifactCheckpointDualWrite = enabled
	}
}

// SetArtifactCheckpointCutover removes the per-unit envelope write for
// localized_render. It is a no-op unless dual-write, resume, the repository and
// the artifact verifier are all wired; a partial cutover would delete the only
// recovery copy without having a trusted replacement.
func (r *Runner) SetArtifactCheckpointCutover(enabled bool) {
	if r != nil {
		r.artifactCheckpointCutover = enabled
	}
}

// localizedRenderCutoverActive reports whether this unit may rely on its
// checkpoint instead of rewriting the whole-result envelope. The verifier is
// part of the gate: an UPLOADED row is reusable only after the media-SSOT
// check, so without it the cutover would silently fall back to a fresh render
// after a crash.
func (r *Runner) localizedRenderCutoverActive() bool {
	return r != nil &&
		r.artifactCheckpointCutover &&
		r.artifactCheckpointDualWrite &&
		r.artifactCheckpointResume &&
		r.artifactCheckpoints != nil &&
		r.artifactCheckpointVerifier != nil
}

// saveLocalizedRenderUnitEnvelope persists the whole-result envelope after a
// certified localized render. During cutover the per-unit checkpoint is the
// authority and this write is deliberately skipped; stage boundaries and the
// final completion path still call checkpoint/SavePartialResult, so the
// envelope catches up before the run advances.
func (r *Runner) saveLocalizedRenderUnitEnvelope(ctx context.Context, runID string, snapshot *GenerateResult) error {
	if r == nil || r.repo == nil || snapshot == nil {
		return nil
	}
	if r.localizedRenderCutoverActive() {
		r.log.Debug("localized render envelope write skipped (cutover)", zap.String("run_id", runID))
		return nil
	}
	return r.repo.SavePartialResult(ctx, runID, snapshot)
}

// saveLocalizedRenderArtifactCheckpoint writes the one kind enabled for the
// first rollout. It is a no-op unless both the store and the flag are wired.
// The write happens before the envelope save at each call site: a checkpoint
// failure fails the unit instead of leaving the per-artifact row silently
// missing behind a successful envelope.
func (r *Runner) saveLocalizedRenderArtifactCheckpoint(ctx context.Context, runID string, attempt int, rendered LocalizedRenderResult) error {
	if r == nil || !r.artifactCheckpointDualWrite || r.artifactCheckpoints == nil {
		return nil
	}
	key, err := ArtifactCheckpointKey(rendered.SceneID, rendered.ClipID, string(rendered.Language))
	if err != nil {
		return fmt.Errorf("localized render: artifact checkpoint key: %w", err)
	}
	inputFingerprint := strings.TrimSpace(rendered.CheckpointFingerprint)
	if inputFingerprint == "" {
		return fmt.Errorf("localized render: artifact checkpoint input fingerprint is missing")
	}
	payload, err := json.Marshal(rendered)
	if err != nil {
		return fmt.Errorf("localized render: artifact checkpoint payload: %w", err)
	}
	durationUS := rendered.DurationMS * 1000
	if durationUS < 0 {
		durationUS = 0
	}
	err = r.artifactCheckpoints.SaveArtifactCheckpoint(ctx, ArtifactCheckpoint{
		RunID:              runID,
		Kind:               ArtifactCheckpointLocalizedRender,
		Key:                key,
		SchemaVersion:      ArtifactCheckpointSchemaVersion,
		InputFingerprint:   inputFingerprint,
		ProcessorVersion:   localizedRenderCheckpointProcessorVersion,
		ArtifactID:         rendered.AssetID,
		ArtifactSHA256:     rendered.SHA256,
		ArtifactURI:        localizedRenderCheckpointURI(rendered),
		ArtifactSizeBytes:  rendered.SizeBytes,
		ArtifactDurationUS: durationUS,
		Payload:            payload,
		Attempt:            attempt,
		CompletedAt:        time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("localized render: artifact checkpoint save: %w", err)
	}
	return nil
}

// localizedRenderCheckpointFingerprint covers the enqueue inputs that
// determine the rendered bytes. It deliberately excludes every output
// (RenderJobID, AssetID, SHA, Drive ids, status, timestamps): those are
// publication state and change between the RENDERED and UPLOADED writes.
//
// The fingerprint must be computable BEFORE the render, when the loader asks
// "can this recorded artifact be reused for this unit?". That is why the
// render job id cannot participate.
func localizedRenderCheckpointFingerprint(in LocalizedRenderInput) string {
	voiceover := struct {
		ID       string  `json:"id"`
		Duration float64 `json:"duration,omitempty"`
	}{
		ID:       in.Voiceover.ID,
		Duration: in.Voiceover.Duration,
	}
	input := struct {
		SceneID        string   `json:"scene_id"`
		SceneIndex     int      `json:"scene_index"`
		Language       Language `json:"language"`
		Text           string   `json:"text"`
		SourceLanguage Language `json:"source_language"`
		SourceText     string   `json:"source_text"`
		ClipID         string   `json:"clip_id"`
		ClipAssetID    string   `json:"clip_asset_id"`
		ClipSHA256     string   `json:"clip_sha256"`
		ClipDurationMS int64    `json:"clip_duration_ms"`
		Voiceover      struct {
			ID       string  `json:"id"`
			Duration float64 `json:"duration,omitempty"`
		} `json:"voiceover"`
		Render   kernelscript.VideoRenderSpec `json:"render"`
		Overlays []LocalizedOverlayLineage    `json:"overlays,omitempty"`
	}{
		SceneID:        in.SceneID,
		SceneIndex:     in.SceneIndex,
		Language:       in.Language,
		Text:           in.Text,
		SourceLanguage: in.SourceLanguage,
		SourceText:     in.SourceText,
		ClipID:         in.ClipID,
		ClipAssetID:    in.ClipAssetID,
		ClipSHA256:     in.ClipSHA256,
		ClipDurationMS: in.ClipDurationMS,
		Voiceover:      voiceover,
		Render:         in.Render,
		Overlays:       in.Overlays,
	}
	body, err := json.Marshal(input)
	if err != nil {
		// The shape contains only value types (the render spec's only map is
		// JSON-serializable), so Marshal cannot fail in practice. Keep a
		// deterministic fallback rather than panicking in a render path.
		return digest.SHA256Bytes([]byte(in.SceneID + "\x00" + in.ClipID + "\x00" + string(in.Language) + "\x00" + in.Text + "\x00" + in.SourceText))
	}
	return digest.SHA256Bytes(body)
}

// localizedRenderCheckpointURI prefers the durable object-store URL, then the
// Drive URL, then the producer-local path (the only identity available at the
// RENDERED stage). The SHA is the byte authority in every case.
func localizedRenderCheckpointURI(rendered LocalizedRenderResult) string {
	for _, candidate := range []string{rendered.ArtifactURL, rendered.DriveLink, rendered.LocalPath} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

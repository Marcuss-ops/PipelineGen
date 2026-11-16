// artifact_checkpoint_translation.go — second kind on the per-artifact
// checkpoint contract: one translated scene×language body, written as soon as
// the translator certifies it.
//
// Unlike localized_render, the artifact IS the checkpoint body (the translated
// text), so there is no media verifier to call. The repository's payload digest
// plus the artifact digest bind the row to the exact text; the input
// fingerprint binds it to the source text, source language and target language.
package scriptgeneration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"go.uber.org/zap"
)

const translationCheckpointProcessorVersion = "translation/v1"

type translationCheckpointPayload struct {
	SceneID  string   `json:"scene_id"`
	Language Language `json:"language"`
	Text     string   `json:"text"`
}

// translationCutoverActive removes the debounced per-unit envelope write. It
// does not need the media verifier: the translated text is self-verifying via
// its payload/artifact digest, unlike an external media object.
func (r *Runner) translationCutoverActive() bool {
	return r != nil &&
		r.artifactCheckpointCutover &&
		r.artifactCheckpointDualWrite &&
		r.artifactCheckpointResume &&
		r.artifactCheckpoints != nil
}

func translationCheckpointFingerprint(sourceLang, targetLang Language, sourceText string) string {
	input := struct {
		SourceLanguage Language `json:"source_language"`
		TargetLanguage Language `json:"target_language"`
		SourceText     string   `json:"source_text"`
	}{
		SourceLanguage: sourceLang,
		TargetLanguage: targetLang,
		SourceText:     sourceText,
	}
	body, err := json.Marshal(input)
	if err != nil {
		return digest.SHA256Bytes([]byte(string(sourceLang) + "\x00" + string(targetLang) + "\x00" + sourceText))
	}
	return digest.SHA256Bytes(body)
}

// saveTranslationArtifactCheckpoint records the certified translated text. A
// failure is returned to the worker and fails the unit (the envelope must not
// claim progress the artifact store does not have).
func (r *Runner) saveTranslationArtifactCheckpoint(ctx context.Context, runID string, attempt int, sceneID string, sourceLang, targetLang Language, sourceText, translated string) error {
	if r == nil || !r.artifactCheckpointDualWrite || r.artifactCheckpoints == nil {
		return nil
	}
	if strings.TrimSpace(translated) == "" {
		return nil
	}
	key, err := ArtifactCheckpointKey(sceneID, string(targetLang))
	if err != nil {
		return fmt.Errorf("translation: artifact checkpoint key: %w", err)
	}
	payload, err := json.Marshal(translationCheckpointPayload{SceneID: sceneID, Language: targetLang, Text: translated})
	if err != nil {
		return fmt.Errorf("translation: artifact checkpoint payload: %w", err)
	}
	textDigest := digest.SHA256Bytes([]byte(translated))
	if err := r.artifactCheckpoints.SaveArtifactCheckpoint(ctx, ArtifactCheckpoint{
		RunID:             runID,
		Kind:              ArtifactCheckpointTranslation,
		Key:               key,
		SchemaVersion:     ArtifactCheckpointSchemaVersion,
		InputFingerprint:  translationCheckpointFingerprint(sourceLang, targetLang, sourceText),
		ProcessorVersion:  translationCheckpointProcessorVersion,
		ArtifactSHA256:    textDigest,
		ArtifactSizeBytes: int64(len(translated)),
		Payload:           payload,
		Attempt:           attempt,
		CompletedAt:       time.Now().UTC(),
	}); err != nil {
		return fmt.Errorf("translation: artifact checkpoint save: %w", err)
	}
	return nil
}

// loadTranslationCheckpoint restores one translated unit. The repository has
// already verified the payload digest; this function additionally checks the
// artifact digest against the text and refuses a changed input.
func (r *Runner) loadTranslationCheckpoint(ctx context.Context, runID, sceneID string, sourceLang, targetLang Language, sourceText string) (string, bool) {
	if r == nil || !r.artifactCheckpointResume || r.artifactCheckpoints == nil {
		return "", false
	}
	key, err := ArtifactCheckpointKey(sceneID, string(targetLang))
	if err != nil {
		return "", false
	}
	checkpoint, err := r.artifactCheckpoints.GetArtifactCheckpoint(ctx, runID, ArtifactCheckpointTranslation, key)
	if err != nil {
		if errors.Is(err, ErrArtifactCheckpointCorrupt) {
			r.invalidateTranslationCheckpoint(ctx, runID, key, "payload digest mismatch")
			return "", false
		}
		r.log.Warn("translation checkpoint read failed; translating unit",
			zap.String("run_id", runID), zap.String("scene_id", sceneID),
			zap.String("language", string(targetLang)), zap.Error(err))
		return "", false
	}
	if checkpoint == nil {
		return "", false
	}
	fingerprint := translationCheckpointFingerprint(sourceLang, targetLang, sourceText)
	if ok, reason := checkpoint.ReusableFor(fingerprint, translationCheckpointProcessorVersion); !ok {
		r.invalidateTranslationCheckpoint(ctx, runID, key, reason)
		return "", false
	}
	var payload translationCheckpointPayload
	if err := json.Unmarshal(checkpoint.Payload, &payload); err != nil || strings.TrimSpace(payload.Text) == "" {
		r.invalidateTranslationCheckpoint(ctx, runID, key, "payload is not a translated text")
		return "", false
	}
	if payload.SceneID != sceneID || payload.Language != targetLang {
		r.invalidateTranslationCheckpoint(ctx, runID, key, "artifact identity does not match the requested unit")
		return "", false
	}
	if !digest.IsCanonicalSHA256(checkpoint.ArtifactSHA256) ||
		!strings.EqualFold(checkpoint.ArtifactSHA256, digest.SHA256Bytes([]byte(payload.Text))) {
		r.invalidateTranslationCheckpoint(ctx, runID, key, "artifact digest does not match the payload text")
		return "", false
	}
	r.log.Info("translation checkpoint reused",
		zap.String("run_id", runID), zap.String("scene_id", sceneID),
		zap.String("language", string(targetLang)))
	return payload.Text, true
}

func (r *Runner) invalidateTranslationCheckpoint(ctx context.Context, runID, key, reason string) {
	if r == nil || r.artifactCheckpoints == nil {
		return
	}
	if err := r.artifactCheckpoints.InvalidateArtifactCheckpoint(ctx, runID, ArtifactCheckpointTranslation, key); err != nil {
		r.log.Warn("translation checkpoint invalidation failed",
			zap.String("run_id", runID), zap.String("key", key), zap.String("reason", reason), zap.Error(err))
		return
	}
	r.log.Info("translation checkpoint invalidated",
		zap.String("run_id", runID), zap.String("key", key), zap.String("reason", reason))
}

// saveTranslationUnitEnvelope keeps the legacy debounced whole-result write
// unless cutover is active. The post-fan-out flush remains a stage boundary and
// is never skipped.
func (r *Runner) saveTranslationUnitEnvelope(ctx context.Context, runID string, snapshot *GenerateResult) error {
	if r == nil || r.repo == nil || snapshot == nil {
		return nil
	}
	if r.translationCutoverActive() {
		r.log.Debug("translation envelope write skipped (cutover)", zap.String("run_id", runID))
		return nil
	}
	return r.checkpoint(ctx, runID, snapshot)
}

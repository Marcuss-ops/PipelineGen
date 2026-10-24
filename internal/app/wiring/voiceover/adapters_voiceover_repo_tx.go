package voiceover

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service/persistence"
	"go.uber.org/zap"
)

func (a *UseCaseRepoAdapter) InsertTx(ctx context.Context, tx *sql.Tx, rec *persistence.VoiceoverRecord) error {
	if rec == nil {
		return fmt.Errorf("UseCaseRepoAdapter.InsertTx: nil record")
	}
	return a.repo.InsertTx(ctx, tx, a.toInfraRecord(rec))
}

func (a *UseCaseRepoAdapter) DeleteByIDTx(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return fmt.Errorf("UseCaseRepoAdapter.DeleteByIDTx: empty id")
	}
	return a.repo.DeleteByIDTx(ctx, tx, id)
}

// FindByIdempotencyKeyTx runs the FASE 3 idempotency gate (July 2026)
// INSIDE the caller-owned tx. Scans voiceovers for an existing row
// with the same idempotency_key. Returns (matchedID, nil) when a
// match is found (idempotency gate fires); returns ("", sql.ErrNoRows)
// when no match exists (first-time run).
//
// Empty idempotencyKey short-circuits to ("", sql.ErrNoRows, nil) —
// the gate is intentionally skipped for pre-FASE-3 callers.
func (a *UseCaseRepoAdapter) FindByIdempotencyKeyTx(
	ctx context.Context,
	tx *sql.Tx,
	idempotencyKey string,
) (string, error) {
	if idempotencyKey == "" {
		return "", sql.ErrNoRows
	}
	if tx == nil {
		return "", fmt.Errorf("UseCaseRepoAdapter.FindByIdempotencyKeyTx: nil tx")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var matchedID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM voiceovers WHERE idempotency_key = ? LIMIT 1`,
		idempotencyKey,
	).Scan(&matchedID)
	if err == sql.ErrNoRows {
		return "", sql.ErrNoRows
	}
	if err != nil {
		return "", fmt.Errorf("FindByIdempotencyKeyTx: scan: %w", err)
	}
	return matchedID, nil
}

func (a *UseCaseRepoAdapter) PreReadByID(ctx context.Context, id string) (*persistence.VoiceoverRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("UseCaseRepoAdapter.PreReadByID: empty id")
	}
	r, err := a.repo.PreReadByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("UseCaseRepoAdapter.PreReadByID: %w", err)
	}
	if r == nil {
		return nil, nil
	}
	return a.fromInfraRecord(r), nil
}

// timeutil import is used here for FormatRFC3339 fallback in
// InsertTx; the canonical timeutil location avoids bringing in
// time.UTC().Format() boilerplate per Adapter struct.

// ── timing hydration for cache hits (moved from adapters_voiceover_repo.go) ──

// timingArtifactSource names the durable location of the canonical timing
// artifact recorded in the voiceovers metadata: the LOCAL certified copy
// first (the video pipeline persists timing.json next to the audio and never
// uploads it — waste-cut 2026-10-24), then the legacy Drive link. Empty when
// neither exists.
func timingArtifactSource(meta map[string]any) string {
	if path, _ := meta["timing_json_path"].(string); strings.TrimSpace(path) != "" {
		return strings.TrimSpace(path)
	}
	if link, _ := meta["timing_json_link"].(string); strings.TrimSpace(link) != "" {
		return strings.TrimSpace(link)
	}
	return ""
}

// loadTimingArtifact hydrates and validates the canonical word-level timing
// artifact for a cache hit. Local certified copy first (no network); legacy
// rows kept the artifact on Drive behind timing_json_link and still hydrate
// through the Drive reader. Validation failures return (nil, nil) so the
// caller treats the row as a MISS; I/O/decode failures return the error.
func (a *VoiceoverCacheAdapter) loadTimingArtifact(ctx context.Context, rec *persistence.VoiceoverRecord, meta map[string]any) (*audio.SpeechTimingArtifact, error) {
	if a == nil || rec == nil {
		return nil, nil
	}
	if localPath, _ := meta["timing_json_path"].(string); strings.TrimSpace(localPath) != "" {
		data, err := os.ReadFile(strings.TrimSpace(localPath))
		if err != nil {
			return nil, fmt.Errorf("read local timing artifact %s: %w", localPath, err)
		}
		return a.decodeTimingArtifact(rec, meta, data)
	}
	if a.timingReader == nil {
		return nil, nil
	}
	link, _ := meta["timing_json_link"].(string)
	fileID, err := driveFileIDFromLink(link)
	if err != nil {
		return nil, err
	}
	reader, _, err := a.timingReader.DownloadFile(ctx, fileID)
	if err != nil {
		return nil, fmt.Errorf("download timing artifact %s: %w", fileID, err)
	}
	defer func() { _ = reader.Close() }()
	var artifact audio.SpeechTimingArtifact
	if err := json.NewDecoder(io.LimitReader(reader, 8<<20)).Decode(&artifact); err != nil {
		return nil, fmt.Errorf("decode timing artifact %s: %w", fileID, err)
	}
	return a.validateTimingArtifact(rec, meta, &artifact)
}

// decodeTimingArtifact decodes a locally persisted timing artifact and runs
// the same identity/hash validation as the remote hydration path.
func (a *VoiceoverCacheAdapter) decodeTimingArtifact(rec *persistence.VoiceoverRecord, meta map[string]any, data []byte) (*audio.SpeechTimingArtifact, error) {
	var artifact audio.SpeechTimingArtifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		return nil, fmt.Errorf("decode timing artifact %s: %w", rec.ID, err)
	}
	return a.validateTimingArtifact(rec, meta, &artifact)
}

// validateTimingArtifact pins the cached artifact to the exact row identity:
// text hash, language, voice, final-audio digest, duration and word count.
// Any mismatch is a MISS (nil, nil) — never a fabricated reuse.
func (a *VoiceoverCacheAdapter) validateTimingArtifact(rec *persistence.VoiceoverRecord, meta map[string]any, artifact *audio.SpeechTimingArtifact) (*audio.SpeechTimingArtifact, error) {
	if err := artifact.Validate(); err != nil {
		a.log.Warn("voiceover cache: cached timing artifact failed validation", zap.String("id", rec.ID), zap.Error(err))
		return nil, nil
	}
	if artifact.TextSHA256 != strings.TrimSpace(rec.TextHash) ||
		artifact.Language != strings.TrimSpace(rec.Language) || artifact.Voice != strings.TrimSpace(rec.Voice) {
		a.log.Warn("voiceover cache: cached timing artifact identity mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	if audioSHA, _ := meta["audio_sha256"].(string); audioSHA == "" || artifact.AudioSHA256 != audioSHA {
		a.log.Warn("voiceover cache: cached timing artifact audio hash mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	if durationUS, ok := meta["timing_duration_us"].(float64); !ok || int64(durationUS) != artifact.DurationUS {
		a.log.Warn("voiceover cache: cached timing artifact duration mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	if wordCount, ok := meta["timing_word_count"].(float64); !ok || int(wordCount) != len(artifact.Words) {
		a.log.Warn("voiceover cache: cached timing artifact word-count mismatch", zap.String("id", rec.ID))
		return nil, nil
	}
	return artifact, nil
}

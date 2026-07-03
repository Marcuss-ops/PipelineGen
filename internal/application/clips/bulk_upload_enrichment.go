// Package clips (bulk_upload_enrichment) — Step "enrich" of the
// per-clip pipeline: stage the transcript for the indexer's
// /index_transcript endpoint + invoke ClipIndexer.IndexClip on the
// local registry.
//
// P1.7 (July 2026): extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split.
//
// Both legs are best-effort:
//   - Transcript staging: a write error logs a warning;
//     staging is non-fatal because the indexer can re-stage
//     from data/media on demand.
//   - IndexClip: a clip-indexer error logs a warning; pre-split
//     behaviour did NOT bump the failed counter for indexer
//     failures (only publish / hash / dispatcher errors counted).
//
// Caller-side responsibilities:
//   - bump indexed.Add(1) when enrichClip returns true
//   - call only when payload.SkipEmbeddings is false
//
// No new abstractions — top-level helper function with single
// bool return. The pushed (Qdrant-push) counter is declared at
// HandleJob scope but never incremented (latent unused counter
// from pre-split behaviour; preserved verbatim — promoting it
// counts failures is a separate hardening wave).
package clips

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// enrichClip stages the candidate's transcript (when present) for
// the indexer's /index_transcript endpoint, then invokes
// ClipIndexer.IndexClip on the local registry.
//
// The clipID parameter MUST be the canonical id computed by the
// orchestrator (processOneClip) from the real file hash — the same
// id that gets persisted to media_assets by the registration
// section. Pre-split the Asset.ID value (post buildBulkClipID) was
// what IndexClip received; P1.7 propagation through a clipID
// argument preserves that contract verbatim.
//
// Returns true when the indexer successfully indexed the clip;
// false otherwise. A nil indexer is treated as "not enabled" and
// returns false (matches pre-split behaviour where
// `if w.indexer != nil && w.indexer.IsEnabled()` gates the call).
func enrichClip(
	ctx context.Context,
	cfg ClipConfigPort,
	indexer ClipIndexerPort,
	cand clipCandidate,
	clipID string,
	log *zap.Logger,
) bool {
	if cand.Transcript != "" {
		stageTranscript(cfg, cand, log)
	}
	if indexer != nil && indexer.IsEnabled() {
		if err := indexer.IndexClip(ctx, clipID); err != nil {
			if log != nil {
				log.Warn("indexer failed (non-fatal)",
					zap.String("clip_id", clipID),
					zap.String("path", cand.LocalPath),
					zap.Error(err))
			}
			return false
		}
		return true
	}
	return false
}

// stageTranscript writes the candidate's transcript into the
// configured YoutubeClips stage root under a slug-safe subbucket.
// Matches pre-split behaviour: missing YoutubeClipsPath falls back
// to dataDir + "youtube-clips"; sub-bucket is sanitised to
// [a-zA-Z0-9_-] only; "_root" is the sentinel for subdir==""
// or ".".
//
// Non-fatal: a mkdir or WriteFile failure logs a warning; the
// indexer will re-stage from data/media on demand when it
// encounters the missing file.
func stageTranscript(
	cfg ClipConfigPort,
	cand clipCandidate,
	log *zap.Logger,
) {
	stageRoot := cfg.YoutubeClipsPath()
	if stageRoot == "" {
		stageRoot = filepath.Join(cfg.DataDir(), "youtube-clips")
	}
	baseNoExt := strings.TrimSuffix(filepath.Base(cand.LocalPath), filepath.Ext(cand.LocalPath))
	subBucket := strings.TrimSpace(cand.Subdir)
	if subBucket == "" || subBucket == "." {
		subBucket = "_root"
	}
	subBucket = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '-' || r == '_' {
			return r
		}
		return '_'
	}, subBucket)
	stageDir := filepath.Join(stageRoot, subBucket)
	_ = os.MkdirAll(stageDir, 0o755)
	stagePath := filepath.Join(stageDir, baseNoExt+".txt")
	if err := os.WriteFile(stagePath, []byte(cand.Transcript), 0o644); err != nil {
		if log != nil {
			log.Warn("transcript staging failed (non-fatal)",
				zap.String("path", stagePath),
				zap.Error(err))
		}
	}
}

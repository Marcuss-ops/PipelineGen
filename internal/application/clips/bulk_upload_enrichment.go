// Package clips (bulk_upload_enrichment) — Step "enrich" of the
// per-clip pipeline: stage the transcript for the indexer's
// /index_transcript endpoint.
//
// P1.7 (July 2026): extracted from
// internal/application/clips/bulk_upload_worker.go as part of the
// 7-file worker-pipeline split.
//
// Wave 2 (Asset commit + Qdrant, July 2026): direct IndexClip calls
// have been removed. The canonical asset.index.requested outbox event
// is already emitted by registerClip via the canonical dispatcher;
// the IndexingHandler consumer will trigger embedding generation and
// Qdrant upsert asynchronously. This helper now only performs
// transcript staging.
//
// Caller-side responsibilities:
//   - call only when payload.SkipEmbeddings is false
//
// No new abstractions — top-level helper function with single
// bool return. The pushed (Qdrant-push) counter is declared at
// HandleJob scope but never incremented (latent unused counter
// from pre-split behaviour; preserved verbatim — promoting it
// counts failures is a separate hardening wave).
package clips

import (
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// enrichClip stages the candidate's transcript (when present) for
// the indexer's /index_transcript endpoint.
//
// Wave 2 (Asset commit + Qdrant, July 2026): direct IndexClip calls
// have been removed. The canonical asset.index.requested outbox event
// is already emitted by registerClip via the canonical dispatcher;
// the IndexingHandler consumer will trigger embedding generation and
// Qdrant upsert asynchronously. This helper now only performs
// transcript staging.
func enrichClip(
	cfg ClipConfigPort,
	cand clipCandidate,
	log *zap.Logger,
) {
	if cand.Transcript != "" {
		stageTranscript(cfg, cand, log)
	}
	// Wave 2: indexing is owned by the canonical outbox consumer
	// (IndexingHandler → clipindexer.IndexClip). The dispatcher
	// emitted the asset.index.requested event during registerClip.
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

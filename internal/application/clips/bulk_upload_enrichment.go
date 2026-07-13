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
//   - always invoked (PR-13 retired the SkipEmbeddings gate)
//
// No new abstractions — top-level helper function. The outbox
// consumer owns indexing / Qdrant asynchronously; the worker
// only stages transcripts.
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
// is already emitted by registerClip via the canonical dispatcher.
func enrichClip(
	cfg ClipConfigPort,
	cand clipCandidate,
	log *zap.Logger,
) {
	if cand.Transcript != "" {
		stageTranscript(cfg, cand, log)
	}
}

// stageTranscript writes the candidate's transcript into the
// configured YoutubeClips stage root under a slug-safe subbucket.
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

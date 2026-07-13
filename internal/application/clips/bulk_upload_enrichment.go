// Package clips (bulk_upload_enrichment) — Step "enrich" of the per-clip
// pipeline: stage the transcript for the canonical IndexingHandler consumer
// (outbox owns indexing/Qdrant asynchronously).
package clips

import (
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
)

// enrichClip stages the candidate's transcript for the indexer when present.
func enrichClip(
	cfg ClipConfigPort,
	cand clipCandidate,
	log *zap.Logger,
) {
	if cand.Transcript != "" {
		stageTranscript(cfg, cand, log)
	}
}

// stageTranscript writes the candidate's transcript to a slug-safe
// subbucket under cfg.YoutubeClipsPath() (default DataDir/youtube-clips).
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

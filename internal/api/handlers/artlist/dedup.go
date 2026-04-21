package artlistpipeline

import (
	"velox/go-master/internal/artlistdb"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

// DedupChecker handles clip deduplication to avoid redundant downloads.
type DedupChecker struct {
	db *artlistdb.ArtlistDB
}

// NewDedupChecker creates a new deduplication checker.
func NewDedupChecker(db *artlistdb.ArtlistDB) *DedupChecker {
	return &DedupChecker{db: db}
}

// CheckExisting checks if a clip is already downloaded (by ID or URL).
// Returns the existing clip if found, or nil.
func (d *DedupChecker) CheckExisting(clipID, url string) *artlistdb.ArtlistClip {
	existingClip, found := d.db.IsClipAlreadyDownloaded(clipID, url)
	if found {
		logger.Info("Clip already downloaded, reusing",
			zap.String("clip_id", existingClip.ID),
			zap.String("drive_url", existingClip.DriveURL))
		return &existingClip
	}
	return nil
}

// CheckSimilarTags checks for clips with similar tags (min 2 matching tags).
// Returns a similar clip if found, or nil.
func (d *DedupChecker) CheckSimilarTags(tags []string) *artlistdb.ArtlistClip {
	if len(tags) == 0 {
		return nil
	}

	similarClips, err := d.db.FindDownloadedClipsWithSimilarTags(tags, 2)
	if err == nil && len(similarClips) > 0 {
		logger.Info("Similar clip already downloaded, reusing",
			zap.String("clip_id", similarClips[0].ID),
			zap.Int("similar_count", len(similarClips)))
		return &similarClips[0]
	}
	return nil
}

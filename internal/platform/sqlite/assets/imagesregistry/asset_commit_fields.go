package imagesregistry

import (
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
)

type assetCommitFields struct {
	now, requestedAt time.Time
	nowString        string
	title            string
	sourceProvider   string
	sourceVideoID    string
	startMS          int64
	endMS            int64
	sourceVersion    string
	indexState       string
	name             string
}

func normalizeAssetCommitFields(req persistence.CommitRequest, now time.Time) assetCommitFields {
	requestedAt := req.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = now
	}
	sourceVersion := req.Metadata.SourceVersion
	if sourceVersion == "" {
		sourceVersion = req.ContentHash
	}
	startMS := req.StartMs
	if startMS == 0 && req.Metadata.StartSec != 0 {
		startMS = int64(req.Metadata.StartSec * 1000)
	}
	endMS := req.EndMs
	if endMS == 0 && req.Metadata.EndSec != 0 {
		endMS = int64(req.Metadata.EndSec * 1000)
	}
	indexState := req.IndexState
	if indexState == "" {
		indexState = "DISCOVERED"
	}
	name := req.Name
	if name == "" {
		name = req.Filename
	}
	return assetCommitFields{
		now: now, requestedAt: requestedAt, nowString: now.UTC().Format(time.RFC3339),
		title:          firstNonEmpty(req.Title, req.Metadata.Title),
		sourceProvider: firstNonEmpty(req.SourceProvider, req.Metadata.SourceProvider),
		sourceVideoID:  firstNonEmpty(req.SourceVideoID, req.Metadata.SourceVideoID),
		startMS:        startMS, endMS: endMS, sourceVersion: sourceVersion,
		indexState: indexState, name: name,
	}
}

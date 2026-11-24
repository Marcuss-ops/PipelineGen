// Package app — canonical_media_committer_derived_reads.go owns the single
// decision "which engine serves the reads derived from the canonical media
// committer?". PostgreSQL is the only media read authority; a committer that
// does not implement pgmedia.EngineHandle yields nil and the caller fails
// closed instead of degrading onto the operational SQLite mirror.
package buildbundle

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/persistence"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
	"go.uber.org/zap"
)

// mediaEngineHandle is the ONLY type assertion on the canonical committer's
// engine. Every derived reader below resolves through this handle.
func mediaEngineHandle(committer persistence.AssetCommitter) pgmedia.EngineHandle {
	if committer == nil {
		return nil
	}
	handle, ok := committer.(pgmedia.EngineHandle)
	if !ok {
		return nil
	}
	return handle
}

func mediaDetailsReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.AssetDetailsReader {
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	return handle.MediaDetailsReader()
}

func mediaDriveFileListerFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDriveFileLister {
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	return handle.MediaDriveFileLister()
}

func mediaYouTubeClipListerFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaYouTubeClipLister {
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	return handle.MediaYouTubeClipLister()
}

func enrichStateStoreFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaEnrichStateStore {
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	return handle.MediaEnrichStateStore()
}

func mediaDuplicateGroupReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.MediaDuplicateGroupReader {
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	return handle.MediaDuplicateGroupReader()
}

func clipsMediaReaderFromCommitter(committer persistence.AssetCommitter) *pgmedia.ClipsMediaReader {
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	return handle.ClipsMediaReader()
}

func folderPathWriterFromCommitter(committer persistence.AssetCommitter) *pgmedia.FolderPathWriter {
	handle := mediaEngineHandle(committer)
	if handle == nil {
		return nil
	}
	return handle.FolderPathWriter()
}

// newClipsRegistry is the one construction point that injects the typed media
// reader alongside the legacy details reader. Keeping it here prevents each
// call site from forgetting the option and silently reopening the SQL bypass.
func newClipsRegistry(querySvc artifacts.AssetDetailsReader, processing persistence.AssetProcessingWriter, committer persistence.AssetCommitter, log *zap.Logger) *artifacts.ClipsRegistry {
	return artifacts.NewClipsRegistryWithLogger(querySvc, processing, committer, log,
		artifacts.WithMediaReader(clipsMediaReaderFromCommitter(committer)))
}

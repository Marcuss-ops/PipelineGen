// Package stockpipeline — step_extract_clips_metadata.go
// (PR-SPLIT-STEP-EXTRACT-CLIPS, August 2026).
//
// Extracted from step_extract_clips.go per godlike/06 SSOT
// one-canonical-owner-per-fact. Owns the timestamp-group metadata
// write + upload logic called by StockExtractClipsStep.Run.
package stockpipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// timestampGroupBuffer groups published chunks by their Drive leaf name.
type timestampGroupBuffer struct {
	leafName   string
	firstIndex int
	chunks     []ChunkState
}

// writeTimestampGroups sorts published chunks by leaf name, writes a
// per-group metadata.json, and uploads it via ArtifactPreparation.
func writeTimestampGroups(
	ctx context.Context,
	runner StepRunner,
	in *RunInput,
	rootFolderName string,
	rootFolderOverride string,
	groupBuckets map[string]*timestampGroupBuffer,
	artifactPrep finalization.ArtifactPreparationService,
) error {
	type orderedGroup struct {
		leafName   string
		firstIndex int
		chunks     []ChunkState
	}
	ordered := make([]orderedGroup, 0, len(groupBuckets))
	for leafName, bucket := range groupBuckets {
		if bucket == nil || len(bucket.chunks) == 0 {
			continue
		}
		ordered = append(ordered, orderedGroup{
			leafName:   leafName,
			firstIndex: bucket.firstIndex,
			chunks:     append([]ChunkState(nil), bucket.chunks...),
		})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].firstIndex == ordered[j].firstIndex {
			return ordered[i].leafName < ordered[j].leafName
		}
		return ordered[i].firstIndex < ordered[j].firstIndex
	})
	for _, group := range ordered {
		metaPath, metaHash, metaSize, metaErr := writeAndHashMetadata(in, group.chunks, runner.RunFingerprint(), runner.LocalFS())
		if metaErr != nil {
			return fmt.Errorf("%w: parent metadata stage for %s: %w",
				ErrStockPublishArtifactFailed, group.leafName, metaErr)
		}
		defer func(p string) {
			if rmErr := runner.LocalFS().Remove(p); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
				if runner.Log() != nil {
					runner.Log().Warn("orchestrator: stock.extract_clips: failed to remove parent metadata temp file",
						zap.String("path", p), zap.Error(rmErr))
				}
			}
		}(metaPath)

		metaIdem, metaIdemErr := asset.SHA256IdempotencyKey("stock:"+runner.RunFingerprint()+":timestamp-group-metadata:"+group.leafName, metaHash)
		if metaIdemErr != nil {
			return fmt.Errorf("%w: parent metadata idem-key for %s: %w",
				ErrStockPublishArtifactFailed, group.leafName, metaIdemErr)
		}
		metaArtifactID := TimestampArtifactID(runner.RunFingerprint(), group.firstIndex, "metadata")
		metaVA := finalization.VerifiedArtifact{
			ArtifactID:         metaArtifactID,
			Kind:               finalization.KindMetadata,
			Filename:           "metadata.json",
			MIMEType:           "application/json",
			LocalPath:          metaPath,
			SizeBytes:          metaSize,
			SHA256:             metaHash,
			Requirement:        finalization.ArtifactRequirementRequired,
			IdempotencyKey:     metaIdem,
			RootFolderName:     rootFolderName,
			RootFolderOverride: rootFolderOverride,
			RootFolderResolved: in != nil && in.DriveFolderResolved,
			PathLeafName:       group.leafName,
		}
		if _, metaPrepErr := artifactPrep.Prepare(ctx, metaVA); metaPrepErr != nil {
			return fmt.Errorf("%w: parent metadata upload for %s (artifact=%s): %w",
				ErrStockPublishArtifactFailed, group.leafName, metaArtifactID, metaPrepErr)
		}
	}
	return nil
}

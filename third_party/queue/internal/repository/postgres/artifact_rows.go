// artifact_rows.go owns the artifact half of the listing projections. ByParent
// and Children join the same render_artifacts row and must read/map it the same
// way, so the column list, the nullable scan destinations and the model mapping
// live here instead of once per query.
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// artifactColumns is the SELECT list of the joined artifact row. It must stay
// in the same order as artifactScan.destinations.
const artifactColumns = `
	       a.id, a.storage_key, a.artifact_url, a.sha256, a.mime_type, a.size_bytes,
	       a.width, a.height, a.fps_num, a.fps_den, a.frame_count, a.duration_us,
	       a.profile_id, a.copy_eligible, a.codec, a.codec_profile, a.closed_gop,
	       a.first_frame_keyframe, a.backend, a.chronon_version,
	       a.drive_file_id, a.drive_link, a.container, a.pixel_format, a.audio_streams`

// parentJobsFrom is the shared FROM/WHERE of the two parent-scoped listings.
// Only the job columns and the ORDER BY differ between ByParent and Children.
const parentJobsFrom = `
		FROM render_jobs j
		LEFT JOIN render_artifacts a ON a.id = j.artifact_id
		WHERE j.parent_job_id = $1`

// parentJobRows runs a parent-scoped listing query and returns its rows plus
// the operation-context release. Callers must close the rows before releasing
// the context: deferring rows.Close after release guarantees that order.
func (r *Repository) parentJobRows(ctx context.Context, parentJobID, query string) (*sql.Rows, context.CancelFunc, error) {
	if parentJobID == "" {
		return nil, nil, fmt.Errorf("parent job id is required")
	}
	ctx, cancel := r.opContext(ctx)
	rows, err := r.db.QueryContext(ctx, query, parentJobID)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	return rows, cancel, nil
}

// artifactScan holds the nullable artifact columns of one joined row. A NULL
// join (a child without an artifact yet) keeps Artifact nil.
type artifactScan struct {
	id, storageKey, artifactURL, sha256, mimeType           sql.NullString
	sizeBytes, width, height, fpsNum, fpsDen                sql.NullInt64
	frameCount, durationUS, audioStreams                    sql.NullInt64
	profileID, codec, codecProfile, backend, chrononVersion sql.NullString
	driveFileID, driveLink, container, pixelFormat          sql.NullString
	copyEligible, closedGOP, firstFrameKeyframe             sql.NullBool
}

// destinations returns the scan targets in artifactColumns order.
func (a *artifactScan) destinations() []any {
	return []any{
		&a.id, &a.storageKey, &a.artifactURL, &a.sha256, &a.mimeType, &a.sizeBytes,
		&a.width, &a.height, &a.fpsNum, &a.fpsDen, &a.frameCount, &a.durationUS,
		&a.profileID, &a.copyEligible, &a.codec, &a.codecProfile, &a.closedGOP,
		&a.firstFrameKeyframe, &a.backend, &a.chrononVersion,
		&a.driveFileID, &a.driveLink, &a.container, &a.pixelFormat, &a.audioStreams,
	}
}

// artifact returns the model projection, or nil when the LEFT JOIN produced no
// artifact row.
func (a *artifactScan) artifact() *model.Artifact {
	if !a.id.Valid {
		return nil
	}
	return &model.Artifact{
		ID:                 a.id.String,
		Kind:               "segment",
		StorageKey:         a.storageKey.String,
		ArtifactURL:        a.artifactURL.String,
		ArtifactHash:       a.sha256.String,
		ContentType:        a.mimeType.String,
		SizeBytes:          a.sizeBytes.Int64,
		Width:              int(a.width.Int64),
		Height:             int(a.height.Int64),
		FPSNum:             int(a.fpsNum.Int64),
		FPSDen:             int(a.fpsDen.Int64),
		FrameCount:         int(a.frameCount.Int64),
		DurationUS:         a.durationUS.Int64,
		ProfileID:          a.profileID.String,
		CopyEligible:       a.copyEligible.Bool,
		Codec:              a.codec.String,
		CodecProfile:       a.codecProfile.String,
		ClosedGOP:          a.closedGOP.Bool,
		FirstFrameKeyframe: a.firstFrameKeyframe.Bool,
		Backend:            a.backend.String,
		ChrononVersion:     a.chrononVersion.String,
		DriveFileID:        a.driveFileID.String,
		DriveLink:          a.driveLink.String,
		Container:          a.container.String,
		PixelFormat:        a.pixelFormat.String,
		AudioStreams:       int(a.audioStreams.Int64),
	}
}

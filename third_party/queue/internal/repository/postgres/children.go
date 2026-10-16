// children.go owns the finalizer fan-in projection: child chunks in
// deterministic chunk order with their artifacts, without the heavy plan
// JSONB blobs the fan-in never reads.
package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// Children returns child chunks in deterministic chunk order.
//
// The projection deliberately excludes render_plan and input_manifest: the
// finalizer's fan-in calls this after every child completion, and loading the
// full plan JSONB per child would turn the fan-in into an O(children²)
// transfer of large blobs nobody reads. A child's frame range, state and
// artifact are all the validation and assembly need.
func (r *Repository) Children(ctx context.Context, parentJobID string) ([]*model.Job, error) {
	if parentJobID == "" {
		return nil, fmt.Errorf("parent job id is required")
	}
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT j.id, j.state, j.chunk_index, j.frame_range,
		       a.id, a.storage_key, a.artifact_url, a.sha256, a.mime_type, a.size_bytes,
		       a.width, a.height, a.fps_num, a.fps_den, a.frame_count, a.duration_us,
		       a.profile_id, a.copy_eligible, a.codec, a.codec_profile, a.closed_gop,
		       a.first_frame_keyframe, a.backend, a.chronon_version,
		       a.drive_file_id, a.drive_link, a.container, a.pixel_format, a.audio_streams
		FROM render_jobs j
		LEFT JOIN render_artifacts a ON a.id = j.artifact_id
		WHERE j.parent_job_id = $1
		ORDER BY j.chunk_index ASC`, parentJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var children []*model.Job
	for rows.Next() {
		var (
			id, state  string
			chunkIndex int
			frameRange []byte
			// Nullable mirrors so a NULL join row (child without an artifact
			// yet) stays a nil job.Artifact.
			nArtifactID, nStorageKey, nArtifactURL, nSHA256, nMimeType   sql.NullString
			nSizeBytes, nWidth, nHeight, nFPSNum, nFPSDen                sql.NullInt64
			nFrameCount, nDurationUS, nAudioStreams                      sql.NullInt64
			nProfileID, nCodec, nCodecProfile, nBackend, nChrononVersion sql.NullString
			nDriveFileID, nDriveLink, nContainer, nPixelFormat           sql.NullString
			nCopyEligible, nClosedGOP, nFirstFrameKeyframe               sql.NullBool
		)
		if err := rows.Scan(
			&id, &state, &chunkIndex, &frameRange,
			&nArtifactID, &nStorageKey, &nArtifactURL, &nSHA256, &nMimeType, &nSizeBytes,
			&nWidth, &nHeight, &nFPSNum, &nFPSDen, &nFrameCount, &nDurationUS,
			&nProfileID, &nCopyEligible, &nCodec, &nCodecProfile, &nClosedGOP,
			&nFirstFrameKeyframe, &nBackend, &nChrononVersion,
			&nDriveFileID, &nDriveLink, &nContainer, &nPixelFormat, &nAudioStreams,
		); err != nil {
			return nil, err
		}
		fr, err := decodeFrameRange(frameRange)
		if err != nil {
			return nil, fmt.Errorf("child %s: %w", id, err)
		}
		job := &model.Job{
			ID:          id,
			State:       model.State(state),
			ParentJobID: parentJobID,
			ChunkIndex:  chunkIndex,
			FrameRange:  fr,
		}
		if nArtifactID.Valid {
			job.Artifact = &model.Artifact{
				ID:                 nArtifactID.String,
				Kind:               "segment",
				StorageKey:         nStorageKey.String,
				ArtifactURL:        nArtifactURL.String,
				ArtifactHash:       nSHA256.String,
				ContentType:        nMimeType.String,
				SizeBytes:          nSizeBytes.Int64,
				Width:              int(nWidth.Int64),
				Height:             int(nHeight.Int64),
				FPSNum:             int(nFPSNum.Int64),
				FPSDen:             int(nFPSDen.Int64),
				FrameCount:         int(nFrameCount.Int64),
				DurationUS:         nDurationUS.Int64,
				ProfileID:          nProfileID.String,
				CopyEligible:       nCopyEligible.Bool,
				Codec:              nCodec.String,
				CodecProfile:       nCodecProfile.String,
				ClosedGOP:          nClosedGOP.Bool,
				FirstFrameKeyframe: nFirstFrameKeyframe.Bool,
				Backend:            nBackend.String,
				ChrononVersion:     nChrononVersion.String,
				DriveFileID:        nDriveFileID.String,
				DriveLink:          nDriveLink.String,
				Container:          nContainer.String,
				PixelFormat:        nPixelFormat.String,
				AudioStreams:       int(nAudioStreams.Int64),
			}
		}
		children = append(children, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return children, nil
}

// by_parent.go owns the run-scoped read: every job submitted under one
// parent_job_id, in submission order.
//
// It is deliberately NOT Children (children.go): that projection answers the
// assembly fan-in ("give me the child chunks of this anchor, in chunk order")
// and only its finalizer reads it. An operator asking "what did this run
// enqueue?" needs the sibling view — parent_job_id is set to the MASTER run id
// by every producer, so the same column carries both meanings and only the
// ordering differs. The projection is shared with Children for the same reason:
// render_plan / input_manifest are large JSONB blobs nobody reads on a listing
// call, and loading them per row would make a diagnostic read as expensive as
// the render it describes.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

// ByParent returns every job whose parent_job_id is parentJobID, ordered by
// queued_at (then id, so equal timestamps stay deterministic). An unknown
// parent yields an empty slice and a nil error.
func (r *Repository) ByParent(ctx context.Context, parentJobID string) ([]*model.Job, error) {
	if parentJobID == "" {
		return nil, fmt.Errorf("parent job id is required")
	}
	ctx, cancel := r.opContext(ctx)
	defer cancel()
	rows, err := r.db.QueryContext(ctx, `
		SELECT j.id, j.state, j.chunk_index, j.frame_range, j.attempt_count,
		       j.queued_at, j.current_worker_id, j.error_message,
		       a.id, a.storage_key, a.artifact_url, a.sha256, a.mime_type, a.size_bytes,
		       a.width, a.height, a.fps_num, a.fps_den, a.frame_count, a.duration_us,
		       a.profile_id, a.copy_eligible, a.codec, a.codec_profile, a.closed_gop,
		       a.first_frame_keyframe, a.backend, a.chronon_version,
		       a.drive_file_id, a.drive_link, a.container, a.pixel_format, a.audio_streams
		FROM render_jobs j
		LEFT JOIN render_artifacts a ON a.id = j.artifact_id
		WHERE j.parent_job_id = $1
		ORDER BY j.queued_at ASC, j.id ASC`, parentJobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []*model.Job
	for rows.Next() {
		var (
			id, state    string
			chunkIndex   int
			attempts     int
			frameRange   []byte
			queuedAt     sql.NullTime
			nWorker      sql.NullString
			nError       sql.NullString
			nArtifactID  sql.NullString
			nStorageKey  sql.NullString
			nArtifactURL sql.NullString
			nSHA256      sql.NullString
			nMimeType    sql.NullString
			nSizeBytes   sql.NullInt64
			nWidth       sql.NullInt64
			nHeight      sql.NullInt64
			nFPSNum      sql.NullInt64
			nFPSDen      sql.NullInt64
			nFrameCount  sql.NullInt64
			nDurationUS  sql.NullInt64
			nProfileID   sql.NullString
			nCopy        sql.NullBool
			nCodec       sql.NullString
			nCodecProf   sql.NullString
			nClosedGOP   sql.NullBool
			nFirstFrame  sql.NullBool
			nBackend     sql.NullString
			nChrononVer  sql.NullString
			nDriveFileID sql.NullString
			nDriveLink   sql.NullString
			nContainer   sql.NullString
			nPixelFormat sql.NullString
			nAudioStrm   sql.NullInt64
		)
		if err := rows.Scan(
			&id, &state, &chunkIndex, &frameRange, &attempts,
			&queuedAt, &nWorker, &nError,
			&nArtifactID, &nStorageKey, &nArtifactURL, &nSHA256, &nMimeType, &nSizeBytes,
			&nWidth, &nHeight, &nFPSNum, &nFPSDen, &nFrameCount, &nDurationUS,
			&nProfileID, &nCopy, &nCodec, &nCodecProf, &nClosedGOP,
			&nFirstFrame, &nBackend, &nChrononVer,
			&nDriveFileID, &nDriveLink, &nContainer, &nPixelFormat, &nAudioStrm,
		); err != nil {
			return nil, err
		}
		fr, err := decodeFrameRange(frameRange)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", id, err)
		}
		job := &model.Job{
			ID:          id,
			State:       model.State(state),
			ParentJobID: parentJobID,
			ChunkIndex:  chunkIndex,
			FrameRange:  fr,
			Attempts:    attempts,
			Worker:      nWorker.String,
			QueuedAt:    queuedAt.Time,
		}
		if nError.Valid {
			job.FailReason = nError.String
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
				CopyEligible:       nCopy.Bool,
				Codec:              nCodec.String,
				CodecProfile:       nCodecProf.String,
				ClosedGOP:          nClosedGOP.Bool,
				FirstFrameKeyframe: nFirstFrame.Bool,
				Backend:            nBackend.String,
				ChrononVersion:     nChrononVer.String,
				DriveFileID:        nDriveFileID.String,
				DriveLink:          nDriveLink.String,
				Container:          nContainer.String,
				PixelFormat:        nPixelFormat.String,
				AudioStreams:       int(nAudioStrm.Int64),
			}
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	return jobs, nil
}

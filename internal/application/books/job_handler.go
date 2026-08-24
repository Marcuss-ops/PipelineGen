package books

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/application/jobs"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ErrBookDrivePublishFailed is the canonical typed sentinel surfaced when
// the post-processing Drive fallback upload fails for at least one
// book-processing artifact. Per godlike/07 (no-fake-availability), the
// HandleJob response now carries an explicit `delivery_status` field
// (PUBLISHED / PUBLISH_FAILED / LOCAL_ONLY) so callers can distinguish
// "Drive succeeded" from "Drive failed but local processing succeeded"
// without parsing the message string. The sentinel is wrapped via
// fmt.Errorf("%w: ...", ErrBookDrivePublishFailed) inside driveToDrive
// so callers can branch on errors.Is(err, ErrBookDrivePublishFailed).
//
// Phase 1.3 closure (July 2026): replaces the pre-closure silent
// log-warn + always-return-success:true behaviour with a strictly
// typed outcome surfaced verbatim in the response map.
var ErrBookDrivePublishFailed = errors.New("books: drive publish failed (delivery_status=PUBLISH_FAILED; see response payload for the per-artifact error)")

// HandleJob processes the background job for book summarization.
// After the Python script finishes, uploads output files to Drive if they
// weren't already uploaded by the script (fallback).
//
// Phase 1.3 closure (July 2026): the legacy `log-warn + always return
// success:true` behaviour was eliminated. The response map now carries
// an explicit `delivery_status` field (PUBLISHED / PUBLISH_FAILED /
// LOCAL_ONLY) so callers can distinguish "Drive succeeded" from
// "Drive failed but local processing succeeded" (godlike/07 no-fake-
// availability). Drive is OPTIONAL: a Drive failure does NOT flip the
// job to FAILED state — the `success` field stays true because local
// processing succeeded; callers branch on `delivery_status` to react to
// the Drive outcome independently of the job terminal status.
//
// The Drive error string (when present) is propagated into the response
// map under `drive_publish_error` so external callers can surface it
// in error banners without parsing it out of `delivery_status`.
func (s *Service) HandleJob(ctx context.Context, job *job.Job, tools *appjobs.JobTools) (map[string]any, error) {
	s.log.Info("handling book.process job", zap.String("job_id", job.ID))

	var req ProcessRequest
	if err := json.Unmarshal(job.Payload, &req); err != nil {
		return nil, fmt.Errorf("failed to unmarshal job payload: %w", err)
	}

	if tools.Progress != nil {
		tools.Progress(10, "Starting book processing")
	}

	// Use ProcessBookWithProgress for real-time progress tracking.
	// The Python script emits [PROGRESS] XX% markers that we parse and
	// forward to the job system for live progress updates.
	result, err := s.ProcessBookWithProgress(ctx, &req, func(pct int, msg string) {
		if tools.Progress != nil {
			tools.Progress(pct, msg)
		}
	})
	if err != nil {
		s.log.Error("book processing failed", zap.Error(err))
		return nil, fmt.Errorf("book processing failed: %w", err)
	}

	if !result.Success {
		return nil, errors.New(result.Error)
	}

	// Fallback Drive upload: if the Python script didn't upload, do it
	// from Go. Phase 1.3 closure: driveToDrive now returns a typed
	// AssetPublishStatus + the (possibly wrapped) publish error so the
	// caller can surface the outcome truthfully (godlike/07).
	deliveryStatus, driveErr := s.driveToDrive(ctx, &req, result, job.ID)

	if tools.Progress != nil {
		tools.Progress(100, "Book processing completed")
	}

	resp := map[string]any{
		"success":          true, // local processing succeeded; Drive is OPTIONAL (Option B).
		"output_path":      result.OutputPath,
		"pdf_path":         result.PDFPath,
		"drive_folder_url": result.DriveFolderURL,
		"drive_doc_url":    result.DriveDocURL,
		"drive_pdf_url":    result.DrivePDFURL,
		"chunks_processed": result.ChunksProcessed,
		"language":         result.Language,
		"delivery_status":  string(deliveryStatus), // PUBLISHED / PUBLISH_FAILED / LOCAL_ONLY — godlike/07 truthful surface.
	}
	if driveErr != nil {
		// Surface the Drive error string verbatim so UI banners can
		// show "publish failed: <inner reason>" without parsing the
		// typed sentinel. Callers branch on errors.Is(err,
		// ErrBookDrivePublishFailed) for typed handling.
		resp["drive_publish_error"] = driveErr.Error()
	}
	return resp, nil
}

// driveToDrive uploads output files to Google Drive when the Python
// script hasn't already done so (fallback). Returns a typed
// AssetPublishStatus (PUBLISHED / PUBLISH_FAILED / LOCAL_ONLY) + the
// (possibly wrapped) publish error. F2.10: the legacy `driveUpload
// *drive.Uploader` field + the per-file `else { driveUpload.UploadFile(...) }`
// branches were dropped entirely (override brutal); the publisher port
// (delivery.Publisher) is the single canonical authority for Drive writes.
//
// godlike/07 no-fake-availability: a nil publisher is NOT silently "Drive
// disabled + success" — it returns AssetPublishLocalOnly so the caller
// can surface the absence of Drive publishing in the response map
// (`delivery_status=LOCAL_ONLY`) instead of pretending a Drive upload
// happened. Mixed outcomes (one artifact OK, one failed) return
// AssetPublishFailed with the first failure wrapped via
// ErrBookDrivePublishFailed; callers can inspect per-artifact Drive
// URLs via the result.*URL fields to see what landed.
// driveToDrive uploads output files to Google Drive when the Python
// script hasn't already done so (fallback). Returns a typed
// AssetPublishStatus (PUBLISHED / PUBLISH_FAILED / LOCAL_ONLY) + the
// (possibly wrapped) publish error. F2.10: the legacy `driveUpload
// *drive.Uploader` field + the per-file `else { driveUpload.UploadFile(...) }`
// branches were dropped entirely (override brutal); the publisher port
// (delivery.Publisher) is the single canonical authority for Drive writes.
//
// godlike/07 no-fake-availability: a nil publisher is NOT silently "Drive
// disabled + success" — it returns AssetPublishLocalOnly so the caller
// can surface the absence of Drive publishing in the response map
// (`delivery_status=LOCAL_ONLY`) instead of pretending a Drive upload
// happened. Mixed outcomes (one artifact OK, one failed) return
// AssetPublishFailed with the first failure wrapped via
// ErrBookDrivePublishFailed; callers can inspect per-artifact Drive
// URLs via the result.*URL fields to see what landed.
//
// PR-P12-CLIPS-AND-BOOKS (July 2026, deadline 2026-08-08): jobID is
// the canonical book processing run ID (auto-derived from book.JobID
// at the call site and threaded in as a parameter). The
// delivery.Publisher resolves the target folder via
// DestinationRegistry + DestinationPolicy.RootFolderID using only the
// semantic fields; the legacy req.DriveFolderID / s.driveFolder
// per-request override is RETIRED.
func (s *Service) driveToDrive(ctx context.Context, req *ProcessRequest, result *ProcessResult, jobID string) (asset.AssetPublishStatus, error) {
	publisher := s.publisher

	// No publisher wired → "Drive disabled". Surface as LOCAL_ONLY (no
	// upload attempted, no failure to mask). Caller distinguishes
	// LOCAL_ONLY from PUBLISH_FAILED via the delivery_status field.
	if publisher == nil {
		return asset.AssetPublishLocalOnly, nil
	}

	// PR-P12-CLIPS-AND-BOOKS (July 2026, deadline 2026-08-08):
	// req.DriveFolderID and s.driveFolder are NO LONGER threaded as
	// ParentFolderID. The canonical Publisher resolves the target
	// folder via DestinationRegistry + DestinationPolicy.RootFolderID
	// (single source of truth per architecture/current.yaml#DRIVE-AS-
	// CENTRAL-CAPABILITY). The per-request folder override is retired;
	// operators configure folder routing via the destination's policy
	// rather than per-request fields. godlike/07 minimum-blast-radius:
	// the ProjectID/Group/Subject semantic fields below carry the
	// book-processing identity (JobID + filename) so the Publisher can
	// resolve a deterministic folder from the registry alone.

	type artifact struct {
		name       string
		path       string
		alreadySet bool              // DriveDocURL/DrivePDFURL already populated by Python script
		publishURL func(link string) // setter that records the new Drive link on the result
	}

	artifacts := []artifact{
		{name: ".txt", path: result.OutputPath, alreadySet: result.DriveDocURL != "", publishURL: func(s string) { result.DriveDocURL = s }},
		{name: ".pdf", path: result.PDFPath, alreadySet: result.DrivePDFURL != "", publishURL: func(s string) { result.DrivePDFURL = s }},
	}

	publishAttempted := false
	anyPublishFailure := false
	var firstErr error

	for _, a := range artifacts {
		if a.path == "" {
			continue // no file to upload (e.g. PDF generation skipped)
		}
		if a.alreadySet {
			continue // Python script already uploaded this artifact
		}
		publishAttempted = true

		pubReq := delivery.PublishRequest{
			Destination: delivery.DestinationBook,
			LocalPath:   a.path,
			Filename:    filepath.Base(a.path),
			ProjectID:   jobID,                 // auto-derive Project from book.JobID (godlike/06 SSOT, PR-P12-CLIPS-AND-BOOKS)
			Group:       "",                    // books don't group; DestinationRegistry picks canonical folder
			Subject:     filepath.Base(a.path), // per-file identity (.txt or .pdf)
			// ParentFolderID RETIRED per PR-P12-CLIPS-AND-BOOKS
			// (July 2026, deadline 2026-08-08). See the block comment
			// above driveToDrive's body for the migration rationale.
		}
		pubResult, err := publisher.Publish(ctx, pubReq)
		if err != nil {
			s.log.Warn("Drive publish failed",
				zap.String("artifact", a.name),
				zap.String("path", a.path),
				zap.Error(err))
			anyPublishFailure = true
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", a.name, err)
			}
			continue
		}
		a.publishURL(pubResult.WebViewLink)
		s.log.Info("published to Drive",
			zap.String("artifact", a.name),
			zap.String("path", a.path),
			zap.String("file_id", pubResult.FileID))
	}

	// State machine:
	//   - No publish attempts AND no already-set URLs from Python →
	//     LOCAL_ONLY (assets have no Drive presence AND no publish was
	//     even attempted).
	//   - At least one failed publish → PUBLISH_FAILED + ErrBookDrivePublishFailed
	//     wrapped around the first failure (caller can errors.Is).
	//   - Otherwise (no failure) → PUBLISHED.
	if !publishAttempted {
		// No path-required upload needed: either all URLs already set
		// by the Python script, or no artifact paths at all. We brand
		// it LOCAL_ONLY when ZERO prior Drive URLs are present
		// (callers can read the result.*URL fields to introspect).
		hasAnyPriorURL := result.DriveDocURL != "" || result.DrivePDFURL != ""
		if !hasAnyPriorURL {
			return asset.AssetPublishLocalOnly, nil
		}
		// Prior publish already happened (Python side); we treat it as
		// PUBLISHED — its URL is in the result.
		return asset.AssetPublishPublished, nil
	}
	if anyPublishFailure {
		return asset.AssetPublishFailed, fmt.Errorf("%w: %w", ErrBookDrivePublishFailed, firstErr)
	}
	return asset.AssetPublishPublished, nil
}

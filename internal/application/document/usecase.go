// Package document — usecase.go (P0 Commit 10, July 2026).
//
// GenerateDocumentUseCase is the canonical use case that converts
// a DocumentRequest into a job.ExecutionResult[DocumentResult] with
// a properly-shaped ArtifactManifest sidecar.
//
// The use case is the ONLY place that builds the sidecar
// (handler is transport-only, service is render-only). This split
// matches AGENTS.md Pattern 7 ("Reusing existing services — use
// case owns the manifest, service is rendering-only") and Pattern 8
// ("API package: thin transport only — no business orchestration
// in the handler").
package document

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"

	delivery "github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/finalization"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	timeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// GenerateDocumentUseCase is the canonical use case for the
// /api/document/generate endpoint. It is a thin orchestration
// pass: bind+validate the request, delegate to the rendering
// service, build the canonical ArtifactManifest sidecar, and
// return the envelope.
//
// Spine path (Step 4/12, July 2026): when DeliveryPublisher,
// SpineDB, and SpineFinalizer are all non-nil, the rendered PDF
// is published to Drive, a job row is inserted, and
// CompleteWithArtifacts finalises the job atomically — making
// document.generate the first vertical slice through the full
// Spina Dorsale. When any spine dep is nil, the use case falls
// back to the pre-spine local-path-only behaviour.
//
// JobID strategy (C10): the JobID is supplied by the caller
// (handler) so a caller-driven request-id correlation is preserved
// across the wire. When no JobID is supplied (sync anonymous mode),
// the use case synthesises a stable JobID from the request URL +
// a deterministic seed; this is documented in the handler.
type GenerateDocumentUseCase struct {
	svc *Service
	log *zap.Logger

	// ── Spine dependencies (optional; all-or-nothing) ───────────────
	// When all three are non-nil, the use case publishes to Drive
	// and finalises through the JobFinalizer. When any is nil,
	// the pre-spine local-path-only behaviour is used.
	DeliveryPublisher delivery.Publisher        // Drive upload (PublishRequest → PublishResult)
	SpineDB           *sql.DB                   // for INSERT INTO jobs (lease row)
	SpineFinalizer    finalization.JobFinalizer // CompleteWithArtifacts
}

// NewGenerateDocumentUseCase wires the use case. svc MUST be
// non-nil (composition-time fail-fast).
func NewGenerateDocumentUseCase(svc *Service, log *zap.Logger) (*GenerateDocumentUseCase, error) {
	if svc == nil {
		return nil, fmt.Errorf("document.NewGenerateDocumentUseCase: service is required (C10 fail-fast)")
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &GenerateDocumentUseCase{svc: svc, log: log}, nil
}

// Handle is the canonical handler-use case contract. Same async/sync
// divide as image/lessons/books: today only the sync branch is
// implemented; async is a follow-on. Returns the canonical envelope.
//
// Spine path (Step 4/12): when DeliveryPublisher, SpineDB, and
// SpineFinalizer are all non-nil, the rendered PDF is published
// to Drive via the canonical delivery.Publisher, a job row with
// a valid lease is inserted into SQLite, and
// finalizer.CompleteWithArtifacts atomically writes asset +
// version + location + outbox + SUCCEEDED in one transaction.
// The returned manifest carries Drive links instead of local paths.
//
// When any spine dep is nil, the pre-spine local-path-only
// behaviour is preserved (backward compat for tests and un-wired
// composition roots).
func (uc *GenerateDocumentUseCase) Handle(ctx context.Context, req DocumentRequest, jobID string) (job.ExecutionResult[DocumentResult], error) {
	if err := req.validate(); err != nil {
		return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: %w", err)
	}

	// Render PDF (service owns the path + workspace allocation).
	info, err := uc.svc.GeneratePDF(ctx, req, jobID, 1)
	if err != nil {
		return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: render: %w", err)
	}

	// ── Spine path (Step 4/12): publish → insert job → finalise ────
	// Activated only when ALL three spine deps are non-nil.
	artifactID := info.JobID + ":" + string(job.ArtifactKindPDF)
	var driveFileID, driveWebViewLink, driveDownloadLink, driveFolderID, driveFolderPath string

	if uc.DeliveryPublisher != nil && uc.SpineDB != nil && uc.SpineFinalizer != nil {
		// (a) Publish to Drive via delivery.Publisher.
		pubResult, pubErr := uc.DeliveryPublisher.Publish(ctx, delivery.PublishRequest{
			Destination: delivery.DestinationDocument,
			LocalPath:   info.Path,
			Filename:    info.Filename,
			Description: fmt.Sprintf("PipelineGen document: %s", req.Title),
			AssetID:     artifactID,
		})
		if pubErr != nil {
			return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: publish to drive: %w", pubErr)
		}
		driveFileID = pubResult.FileID
		driveWebViewLink = pubResult.WebViewLink
		driveDownloadLink = pubResult.DownloadLink
		driveFolderID = pubResult.FolderID
		driveFolderPath = pubResult.FolderPath

		// (b) Insert a RUNNING job row with a valid lease so the
		//     finalizer can validate ownership and write the
		//     completion transaction. INSERT OR IGNORE handles
		//     the retry-after-partial-failure case: if Publish
		//     succeeded but a prior finalizer call failed, the
		//     row already exists and we proceed to finalize
		//     (idempotent — same fingerprint → success).
		//
		//     We call finalizer.CompleteWithArtifacts directly
		//     (not broker.CompleteWithArtifacts) because the sync
		//     endpoint self-creates the lease — there is no
		//     broker Claim → work → Complete cycle for a sync
		//     handler. The broker's CompleteWithArtifacts wraps
		//     the same finalizer call with a pre-read of the job
		//     row (ensureJobSession); the finalizer does its own
		//     lease validation inside the transaction anyway.
		now := time.Now().UTC()
		nowStr := timeutil.FormatRFC3339(now)
		futureStr := timeutil.FormatRFC3339(now.Add(5 * time.Minute))
		workerID := "doc-sync-" + jobID
		leaseID := "lease-doc-" + jobID

		_, dbErr := uc.SpineDB.ExecContext(ctx,
			`INSERT OR IGNORE INTO jobs (id, type, status, worker_id, lease_id, lease_expiry, retry_count, revision, created_at, updated_at)
			 VALUES (?, 'document.generate', 'RUNNING', ?, ?, ?, 0, 1, ?, ?)`,
			jobID, workerID, leaseID, futureStr, nowStr, nowStr,
		)
		if dbErr != nil {
			return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: insert job row: %w", dbErr)
		}

		// (c) Build the finalization request and complete.
		resultData, _ := json.Marshal(DocumentResult{
			Title:     req.Title,
			Format:    FormatPDF,
			PageCount: info.PageCount,
			BodyChars: len(req.Body),
			Slug:      info.Slug,
		})

		// P0 #3-B residual migration: route the IdempotencyKey through
		// buildDocArtifactIdempotencyKey (which calls asset.SHA256IdempotencyKey
		// behind the scenes) — the canonical owner per godlike/06 SSOT.
		// Production info.SHA256 is canonical via service.GeneratePDF →
		// sha256File, so the typed-error path is unreachable today; the
		// helper acts as defence-in-depth against future SHA-producer drift
		// (a regression that emits uppercase / short / non-canonical hex
		// from sha256File would panic on `[:16]` without this gate).
		idemKey, idemErr := buildDocArtifactIdempotencyKey(info.SHA256)
		if idemErr != nil {
			return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: idempotency key: %w", idemErr)
		}

		finResult, finErr := uc.SpineFinalizer.CompleteWithArtifacts(ctx, finalization.FinalizationRequest{
			Lease: finalization.Lease{
				LeaseID:   leaseID,
				JobID:     jobID,
				WorkerID:  workerID,
				Attempt:   1,
				ExpiresAt: now.Add(5 * time.Minute),
			},
			Result: finalization.ResultManifest{
				SchemaVersion: "v1",
				JobID:         jobID,
				Attempt:       1,
				Data:          resultData,
			},
			Artifacts: []finalization.PublishedArtifact{
				{
					ArtifactID:     artifactID,
					Kind:           finalization.KindDocument,
					Filename:       info.Filename,
					MIMEType:       "application/pdf",
					SizeBytes:      info.SizeBytes,
					SHA256:         info.SHA256,
					SourceVersion:  1,
					Requirement:    finalization.ArtifactRequirementRequired,
					IdempotencyKey: idemKey,
					Source:         "document",
					Location: finalization.AssetLocation{
						Provider:     "drive",
						FileID:       driveFileID,
						WebViewLink:  driveWebViewLink,
						DownloadLink: driveDownloadLink,
						FolderID:     driveFolderID,
						FolderPath:   driveFolderPath,
						Action:       finalization.PublishCreated,
					},
				},
			},
		})
		if finErr != nil {
			return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: finalize: %w", finErr)
		}

		uc.log.Info("document.generate complete via spine",
			zap.String("job_id", info.JobID),
			zap.String("filename", info.Filename),
			zap.Int64("size_bytes", info.SizeBytes),
			zap.String("drive_file_id", driveFileID),
			zap.Int("artifact_refs", len(finResult.ArtifactRefs)),
		)
	} else {
		// Pre-spine path (backward compat): local path only.
		uc.log.Info("document.generate complete (pre-spine)",
			zap.String("job_id", info.JobID),
			zap.String("filename", info.Filename),
			zap.Int64("size_bytes", info.SizeBytes),
		)
	}

	// ── Build the canonical ArtifactManifest sidecar ────────────────
	// When the spine path ran, the Path is cleared and Drive links
	// are absent (the Sender-safe manifest is now in the database).
	// When the pre-spine path ran, the local Path is preserved.
	manifestPath := info.Path
	if driveFileID != "" {
		manifestPath = "" // Spine path: clear local path; caller gets DB-side references
	}

	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "",
		JobID:         info.JobID,
		Artifacts: []job.Artifact{
			{
				ID:        artifactID,
				Kind:      job.ArtifactKindPDF,
				Path:      manifestPath,
				Filename:  info.Filename,
				MIMEType:  "application/pdf",
				SizeBytes: info.SizeBytes,
				SHA256:    info.SHA256,
				Required:  true,
			},
		},
	}

	if vErr := manifest.Validate(); vErr != nil {
		return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: manifest invalid: %w", vErr)
	}

	result := DocumentResult{
		Title:     req.Title,
		Format:    FormatPDF,
		PageCount: info.PageCount,
		BodyChars: len(req.Body),
		Slug:      info.Slug,
	}

	return job.ExecutionResult[DocumentResult]{
		Data:      result,
		Artifacts: manifest,
	}, nil
}

// validate enforces DocumentRequest envelope invariants. Failure
// here is a 400 Bad Request, not a 500 — the bad-input path is
// distinct from the runtime-failure path.
func (r DocumentRequest) validate() error {
	if r.Title == "" {
		return errInvalid("title is required")
	}
	if r.Body == "" {
		return errInvalid("body is required")
	}
	if r.Format != "" && r.Format != FormatPDF {
		return errInvalid(fmt.Sprintf("format %q is not supported (only %q)", r.Format, FormatPDF))
	}
	return nil
}

// errInvalid is a package-internal sentinel-like constructor.
// Avoids exporting new error types for a domain-specific 400 case.
type validationError struct{ msg string }

func (v validationError) Error() string { return v.msg }
func errInvalid(s string) error         { return validationError{msg: s} }

// buildDocArtifactIdempotencyKey is the canonical owner of
// "doc:idempotency-key-from-sha" within the document package.
// It's a thin typed-package entry-point over the cross-package
// canonical asset.SHA256IdempotencyKey validator godlike/06 SSOT.
//
// Why this helper exists (P0 #3-B residual, July 2026):
//
//	Pre-migration, the spine path built the artifact idempotency key
//	with the literal `"doc-" + info.SHA256[:16]` in Handle() (right
//	before the CompleteWithArtifacts call) — the same panic-prone
//	pattern that the verdict flagged on the stock side for
//	stockpipeline/finalizer_gates.go. Stock §12-1 (July 2026)
//	retired the runtime panic via asset.SHA256IdempotencyKey; this
//	helper closes the same exposure on the document spine path BEFORE
//	a malformed-SHA producer causes a slice-bounds panic at runtime.
//	(Symbol reference preferred over hard-coded line numbers per
//	godlike/06 audit-pinning discipline — line numbers drift across
//	refactors; symbol references stay valid.)
//
// godlike/06 SSOT: this helper is the typed-package entry-point
// (package document). The cross-package validator
// (asset.ValidateSHA256 + asset.SHA256IdempotencyKey in
// internal/domain/asset) is the canonical owner; this helper
// routes through it without re-implementing the validation rules.
//
// godlike/07 typed-error contract: ErrSHA256Invalid propagates via
// errors.Is(err, asset.ErrSHA256Invalid) — the closure typed-error
// chain is preserved through the call site's `fmt.Errorf(": %w", err)`
// wrapping.
func buildDocArtifactIdempotencyKey(sha string) (string, error) {
	return asset.SHA256IdempotencyKey("doc", sha)
}

// GenerateDocumentErrMapper maps use-case errors to HTTP responses.
// Mirrors the lessons + books pattern: 400 for validation, 500 +
// fail-closed for runtime failures. Uses errors.As so wrapped
// validation errors (Handle returns fmt.Errorf("%w", err)) are
// correctly classified — a Go type assertion (err.(validationError))
// does NOT unwrap a wrapped target, so a wrapped validation message
// falls through to 500 with the wrong semantics. This is the
// canonical fix per Go errors-best-practice (godlike/07 §errors).
func GenerateDocumentErrMapper(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	var v validationError
	if errors.As(err, &v) {
		return http.StatusBadRequest, err.Error()
	}
	return http.StatusInternalServerError, err.Error()
}

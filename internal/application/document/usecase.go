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
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/job"
)

// GenerateDocumentUseCase is the canonical use case for the
// /api/document/generate endpoint. It is a thin orchestration
// pass: bind+validate the request, delegate to the rendering
// service, build the canonical ArtifactManifest sidecar, and
// return the envelope.
//
// JobID strategy (C10): the JobID is supplied by the caller
// (handler) so a caller-driven request-id correlation is preserved
// across the wire. When no JobID is supplied (sync anonymous mode),
// the use case synthesises a stable JobID from the request URL +
// a deterministic seed; this is documented in the handler.
type GenerateDocumentUseCase struct {
	svc *Service
	log *zap.Logger
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
// jobID is supplied by the handler (caller-id correlation). attempt
// is always 1 for the sync branch (a future async branch would
// increment on retry).
func (uc *GenerateDocumentUseCase) Handle(ctx context.Context, req DocumentRequest, jobID string) (job.ExecutionResult[DocumentResult], error) {
	if err := req.validate(); err != nil {
		return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: %w", err)
	}

	// Render PDF (service owns the path + workspace allocation).
	info, err := uc.svc.GeneratePDF(ctx, req, jobID, 1)
	if err != nil {
		return job.ExecutionResult[DocumentResult]{}, fmt.Errorf("document.GenerateDocumentUseCase.Handle: render: %w", err)
	}

	// Build the canonical ArtifactManifest sidecar — one required
	// artifact (the PDF). Sender-side upload cycle reads
	// Filename/MIMEType/SizeBytes from THIS sidecar.
	manifest := &job.ArtifactManifest{
		SchemaVersion: job.SchemaVersionArtifactManifestV1,
		WorkflowID:    "", // optional caller-supplied field; sync handler does not set
		JobID:         info.JobID,
		Artifacts: []job.Artifact{
			{
				ID:        info.JobID + ":" + string(job.ArtifactKindPDF),
				Kind:      job.ArtifactKindPDF,
				Path:      info.Path,
				Filename:  info.Filename,
				MIMEType:  "application/pdf",
				SizeBytes: info.SizeBytes,
				SHA256:    info.SHA256,
				Required:  true, // single required artifact for document.generate
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

	uc.log.Info("document.generate complete",
		zap.String("job_id", info.JobID),
		zap.String("filename", info.Filename),
		zap.Int64("size_bytes", info.SizeBytes),
	)

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

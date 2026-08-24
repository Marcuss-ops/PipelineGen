// Package app — ArtifactServicePort adapter (P0.1, June 2026).
//
// Wraps the concrete *artifacts.Service (internal/application/assets/artifacts)
// into the narrow ArtifactServicePort interface declared in
// internal/application/clips/upload/ports.go.
//
// The adapter converts between the two DTO shapes at the adapter boundary:
//   - upload.ArtifactCreateInput → artifacts.CreateInput
//   - *artifacts.Artifact → *upload.ArtifactRef
//
// This keeps the upload use case free of artifacts package imports
// (AGENTS.md Pattern 0 — port abstraction layer).

package wiring

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	appupload "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips/upload"
)

// artifactServiceAdapter wraps *artifacts.Service and satisfies
// appupload.ArtifactServicePort. Maps CreateAndVerify + LocalPath
// calls through with DTO conversion at the adapter boundary.
type artifactServiceAdapter struct {
	svc *artifacts.Service
}

// Compile-time assertion: artifactServiceAdapter satisfies ArtifactServicePort.
var _ appupload.ArtifactServicePort = (*artifactServiceAdapter)(nil)

// NewArtifactServiceAdapter creates a port adapter from the concrete
// artifacts service. Returns nil when svc is nil (nil-safe — callers
// check the adapter, not the concrete).
func NewArtifactServiceAdapter(svc *artifacts.Service) appupload.ArtifactServicePort {
	if svc == nil {
		return nil
	}
	return &artifactServiceAdapter{svc: svc}
}

// CreateAndVerify delegates to artifacts.Service.CreateAndVerify with
// DTO conversion at the adapter boundary.
func (a *artifactServiceAdapter) CreateAndVerify(ctx context.Context, in appupload.ArtifactCreateInput) (*appupload.ArtifactRef, error) {
	result, err := a.svc.CreateAndVerify(ctx, artifacts.CreateInput{
		ID:       in.ID,
		Kind:     in.Kind,
		MimeType: in.MimeType,
		Reader:   in.Reader,
	})
	if err != nil {
		return nil, err
	}

	return &appupload.ArtifactRef{
		ID:        result.ID,
		SHA256:    result.SHA256,
		SizeBytes: result.SizeBytes,
	}, nil
}

// LocalPath delegates to artifacts.Service.LocalPath directly
// (signatures are identical).
func (a *artifactServiceAdapter) LocalPath(ctx context.Context, id string) (string, error) {
	return a.svc.LocalPath(ctx, id)
}

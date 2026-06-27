// Package adapters — stubs.go provides placeholder types and constructors
// needed by wire_script.go composition to compile after the origin/main merge.
// All stubs are no-ops (return nil / empty structs). Feature restoration is
// tracked for future PRs.
//
// PR 0 build fix (June 2026).
package adapters

import (
	"context"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Source Resolver stubs ────────────────────────────────────────────

// NewTextSourceResolver returns a nil SourceResolver (text-only path).
func NewTextSourceResolver() SourceResolver { return nil }

// NewClipsSourceResolver returns a nil SourceResolver.
func NewClipsSourceResolver(_ interface{}, _ *zap.Logger) SourceResolver { return nil }

// NewCatalogSourceResolver returns a nil SourceResolver.
func NewCatalogSourceResolver(_ interface{}, _ interface{}, _ *zap.Logger) SourceResolver { return nil }

// NewSearchSourceResolver returns a nil SourceResolver.
func NewSearchSourceResolver(_ interface{}, _ interface{}, _ *zap.Logger) SourceResolver { return nil }

// CurateSourceResolver is a stub for the curate resolver.
type CurateSourceResolver struct{}

// Resolve is a stub.
func (r *CurateSourceResolver) Resolve(_ context.Context, _ scriptpkg.SourceSpec, _ scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	return nil, nil
}

// SetClipSearchPort is a stub.
func (r *CurateSourceResolver) SetClipSearchPort(_ interface{}) {}

// NewCurateSourceResolver returns an empty stub.
func NewCurateSourceResolver(_ interface{}, _ *zap.Logger) *CurateSourceResolver {
	return &CurateSourceResolver{}
}

// ── PostProcessor interface stub ────────────────────────────────────

// PostProcessor is the interface wire_script.go expects ppReg.Register to accept.
type PostProcessor interface {
	Process(input ProcessInput) (*PostProcessResult, error)
}

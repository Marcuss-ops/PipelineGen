// Package scripts — media_curator_stubs.go (June 2026, Phase 1b stub).
//
// The MediaCurator, CurateRequest, CurateResult, CurateNoClipsError types
// and the ErrCurateNoClips sentinel were previously defined in the
// internal/media/ package (removed during Wave 13). They are reproduced
// here as a minimal stub so the media_curator_test.go fixture keeps
// compiling without re-introducing the deleted package.
//
// The semantics captured by these stubs are the *contract* the production
// MediaCurator must satisfy — the production wiring lives elsewhere and
// will replace this stub when Wave X re-constitutes the curator.
//
// Stub fidelity:
//   - Curate returns the typed ErrCurateNoClips (errors.Is-detectable)
//     wrapping *CurateNoClipsError (errors.As-detectable) on the
//     no-clips path. HintClipIDs bypasses the gate. AllowTextOnly
//     returns an empty result instead.
//   - CurateResult carries the resolved IDs, source text, and clip
//     evidence. The tests only assert on AcceptedClipIDs today; the
//     other slots are reserved for the future production re-introduction.
package usecase

import (
	"context"
	"errors"
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// (ErrCurateNoClips lives in source_resolver_curate.go to keep the
// sentinel co-located with the resolver that emits it.)
var _ = ErrCurateNoClips // sentinel is co-located in source_resolver_curate.go

// MediaCurator is the stub media-curator service used by the
// generation pipeline when callers opt into the curate (semantic +
// hint) resolution path.
type MediaCurator struct {
	log         *zap.Logger
	clipBuilder any
	clipSearch  any
}

// CurateRequest is the request envelope for MediaCurator.Curate.
type CurateRequest struct {
	Query         string
	HintClipIDs   []string
	AllowTextOnly bool
}

// CurateResult is the response envelope from MediaCurator.Curate.
type CurateResult struct {
	AcceptedClipIDs []string
	SourceText      string
	ClipEvidence    *scriptpkg.ClipEvidence
}

// CurateNoClipsError is the typed error returned when the resolver
// cannot produce any clips. It is wrapped by ErrCurateNoClips so
// callers can use either errors.Is or errors.As to detect it.
type CurateNoClipsError struct {
	Query       string
	ResultCount int
}

// Unwrap returns the ErrCurateNoClips sentinel so errors.Is matches.
func (e *CurateNoClipsError) Unwrap() error { return ErrCurateNoClips }

// Error implements the error interface.
func (e *CurateNoClipsError) Error() string {
	if e == nil {
		return "curate: no clips found"
	}
	return fmt.Sprintf("curate: no clips for query=%q (count=%d)", e.Query, e.ResultCount)
}

// ErrCurateNoClips is the sentinel for "no clips could be resolved".
// Declared in source_resolver_curate.go (Phase 1b, Wave 13); re-used
// here so media_curator_test.go can errors.Is against it without an
// extra import.

// Curate implements the contract pinned by media_curator_test.go.
// Phase 1b stub — production wiring will replace this body when
// Wave X re-constitutes the curator.
func (m *MediaCurator) Curate(ctx context.Context, req CurateRequest) (*CurateResult, error) {
	if m == nil {
		return nil, errors.New("MediaCurator: nil receiver")
	}
	if len(req.HintClipIDs) > 0 {
		return &CurateResult{AcceptedClipIDs: append([]string(nil), req.HintClipIDs...)}, nil
	}
	if !req.AllowTextOnly {
		return nil, &CurateNoClipsError{Query: req.Query, ResultCount: 0}
	}
	return &CurateResult{}, nil
}

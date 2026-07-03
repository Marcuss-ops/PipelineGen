// Package usecase — extraction_request.go: pure normalization helpers
// for an inbound ExtractRequest.
//
// PR-GODOBJ-1 (July 2026): pure-function helpers split out of the
// legacy extraction_service.go god service per godlike/06 SSOT
// (one canonical owner per fact: validation lives ONLY here). All
// helpers are side-effect free so they can be unit-tested without
// the full ExtractionService fixture.
package usecase

import (
	"fmt"
	"strings"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// cleanClipName trims + SafeName + falls back to segment_NNN on empty.
// Mirrors cleanSegmentName in process_segment.go (canonical use case);
// the orchestrator-side fallback (RequestNormalize path) lives here so
// Extract's preflight branch is self-contained without crossing the
// use case boundary.
func cleanClipName(name string, idx int) string {
	name = strings.TrimSpace(name)
	name = textutil.SafeName(name)
	name = strings.TrimSpace(name)
	if name == "" {
		name = fmt.Sprintf("segment_%03d", idx+1)
	}
	return name
}

// resolveKeepAudio is the canonical nil-check for the *bool KeepAudio
// DTO field. PR-C YouTube Cutover Commit 2/6 introduced the typed-pointer
// default; this helper extracts it so the orchestrator stays thin.
//
//   - nil *ExtractRequest OR nil req.KeepAudio → true (canonical default;
//     PR-C flip from legacy silent-default-false).
//   - non-nil req.KeepAudio → *req.KeepAudio (caller's explicit choice).
func resolveKeepAudio(req *youtubetypes.ExtractRequest) bool {
	if req == nil || req.KeepAudio == nil {
		return true
	}
	return *req.KeepAudio
}

// canonicalGroup extracts the canonicalized group from the inbound
// request. Whitespace-trimmed; empty/missing → "general" sentinel.
func canonicalGroup(req *youtubetypes.ExtractRequest) string {
	if req != nil && req.Destination != nil && strings.TrimSpace(req.Destination.Group) != "" {
		return strings.TrimSpace(req.Destination.Group)
	}
	return "general"
}

// ensureSegmentsService returns the canonical SegmentsService constructor
// when the inbound handle is nil. Lazy-init mirrors the prior god-service
// behaviour (extract_service.go:auto-init on nil) — kept here so
// SegmentsSvc is ALWAYS non-nil post-ctor regardless of caller wiring.
func ensureSegmentsService(svc *SegmentsService) *SegmentsService {
	if svc == nil {
		return NewSegmentsService()
	}
	return svc
}

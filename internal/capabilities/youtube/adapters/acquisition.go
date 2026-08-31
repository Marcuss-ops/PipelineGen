// Package adapters — YouTube acquisition phase-1 typed-narrow
// godlike/06 SSOT surface (internal/capabilities/youtube/adapters).
//
// FORWARD-ARCHAEOLOGY: this file replaces
// internal/youtube/acquisition/acquisition.go, retired in P1-4
// step 2 (godlike/07 ZERO_LEGACY_POLICY). The body, exported
// symbols, sentinel contracts, and compile-time pin are preserved
// 1:1; only the canonical owner changed from
// `package acquisition` (legacy root) to `package adapters` (in
// the canonical application/youtube/adapters family).
//
// Error-message backward compat: this file preserves the legacy
// `youtube/acquisition:` prefix verbatim on all sentinel and
// fail-closed errors (godlike/07 NO-OPERATOR-SURPRISE). Operators
// grepping `youtube/acquisition:` for the existing dashboards and
// log-scrape pipelines will continue to match in the post-rename
// tree — the package-decl migration MUST NOT silently rewrite log
// strings the operator community depends on.
//
// PR-YOUTUBE-SERVICE-SPLIT (July 2026, phase 1): typed-narrow godlike/06
// SSOT contract is in place. The ServiceAdapter constructor accepts the
// canonical *youtube.Service so the composition root can validate
// wiring at boot (godlike/07 fail-closed), but the actual
// Extract-request projection is DEFERRED to phase 2 — the youtube/dto
// SegmentInput shape + ExtractItem field map require a dedicated read
// before the delegation can be byte-correct. Phase 1 is the typed
// skeleton (godlike/06) + the typed sentinel (godlike/07
// NO-FAKE-AVAILABILITY).
package adapters

import (
	"context"
	"fmt"

	youtube "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/usecase"
)

// Acquirer is the canonical godlike/06 SSOT narrow port for
// the YouTube acquisition surface.
type Acquirer interface {
	Acquire(ctx context.Context, req *Request) (*DownloadedSource, error)
}

// Request is the package-local command shape.
type Request struct {
	URL      string
	VideoID  string
	Segments []Segment
}

// Segment is the per-cut acquisition input.
type Segment struct {
	Name    string
	StartMs int64
	EndMs   int64
	Index   int
}

// DownloadedSource is the canonical post-acquisition result.
type DownloadedSource struct {
	SourceURL  string
	VideoID    string
	LocalPath  string
	Bytes      int64
	DurationMs int64
	OK         bool
	// Error is populated when OK == false (godlike/07 typed contract).
	Error string
}

// ErrAcquirerNotWired is the typed sentinel returned when no
// canonical *youtube.Service is wired at construction time
// (godlike/07 fail-closed).
var ErrAcquirerNotWired = fmt.Errorf("youtube/acquisition: acquirer not wired (godlike/07 fail-closed)")

// ErrAcquirerNotImplemented is the phase-1 typed sentinel returned
// by Acquire when the canonical implementation is not yet
// promoted into this package's godlike/06 SSOT owner surface.
// godlike/07 NO-FAKE-AVAILABILITY: never a silent empty /
// no-op result — operators see the typed sentinel + the
// deferred-phase metadata.
var ErrAcquirerNotImplemented = fmt.Errorf("youtube/acquisition: canonical Extract delegation deferred to phase 2 (godlike/07 typed sentinel; youtube/dto SegmentInput projection pending)")

// ServiceAdapter is the canonical impl of Acquirer. Phase 1
// accepts the canonical *youtube.Service so composition-root
// wiring validates (godlike/07); phase 2 will project the
// youtube/dto fields into the package-local DownloadedSource.
type ServiceAdapter struct {
	svc *youtube.Service
}

// NewServiceAdapter constructs the canonical Acquirer.
// nil svc → ErrAcquirerNotWired (godlike/07 fail-closed).
func NewServiceAdapter(svc *youtube.Service) (*ServiceAdapter, error) {
	if svc == nil {
		return nil, ErrAcquirerNotWired
	}
	return &ServiceAdapter{svc: svc}, nil
}

// Acquire returns the phase-1 typed sentinel. Phase 2 will
// replace this with the canonical `svc.Extract` projection
// (gated by the youtube/dto SegmentInput verification).
//
// godlike/07 NO-FAKE-AVAILABILITY: never silent empty result —
// the typed sentinel + the deferred-phase reason are observable
// to operators via errors.Is + the wrapper message.
func (a *ServiceAdapter) Acquire(ctx context.Context, req *Request) (*DownloadedSource, error) {
	if a == nil {
		return nil, ErrAcquirerNotWired
	}
	if req == nil || req.URL == "" {
		return nil, fmt.Errorf("youtube/acquisition: URL is required (godlike/07 fail-closed)")
	}
	if len(req.Segments) == 0 {
		return nil, fmt.Errorf("youtube/acquisition: at least one segment is required (godlike/07 fail-closed)")
	}
	// Phase-2 deferral — the canonical Extract delegation is
	// gated on reconciling the dto.ExtractItem field map with the
	// package-local DownloadedSource. Until that reconciliation
	// ships, surface the typed sentinel.
	return nil, fmt.Errorf("%w (url=%q, video_id=%q, segments=%d)",
		ErrAcquirerNotImplemented, req.URL, req.VideoID, len(req.Segments))
}

// Compile-time pinning: *ServiceAdapter satisfies Acquirer.
var _ Acquirer = (*ServiceAdapter)(nil)

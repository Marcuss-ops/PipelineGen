// Package usecase — extraction_stubs_test.go: minimal test stubs +
// helper for ProcessYouTubeSegmentUseCase.
//
// PR-GODOBJ-1 (July 2026): the panic-on-nil ctor contract (godlike/07
// fail-closed) means tests that wire NewService MUST inject a
// non-nil ProcessSeg. This file provides the minimal-viable stubs
// (cache miss / no-op hash / no-op writer) so existing tests keep
// compiling without depending on the full pipeline.
package usecase

import (
	"context"

	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
)

// testStubClipCache satisfies youtubeports.ClipCachePort returning
// cache miss. Value receiver so the zero-value `testStubClipCache{}`
// is usable as the port argument directly.
type testStubClipCache struct{}

func (testStubClipCache) GetExisting(_ context.Context, _ string) (*youtubetypes.ExtractItem, bool, error) {
	return nil, false, nil
}

// testStubHash satisfies youtubeports.HashServicePort as a no-op.
type testStubHash struct{}

func (testStubHash) SHA256File(_ string) (string, error) {
	return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil
}
func (testStubHash) SHA256String(_ string) string {
	return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
}
func (testStubHash) MD5String(_ string) string        { return "" }
func (testStubHash) MD5File(_ string) (string, error) { return "stubhash", nil }

// testStubClipAtomicWriter satisfies youtubeports.ClipAtomicWriter
// as a no-op (CommitClipAndIndexEvent returns nil always).
type testStubClipAtomicWriter struct{}

func (testStubClipAtomicWriter) CommitClipAndIndexEvent(
	_ context.Context,
	_ string,
	_ youtubetypes.ClipAsset,
	_ youtubeports.IndexEventPayload,
) error {
	return nil
}

// newTestProcessSegmentUseCase returns a minimal valid
// *ProcessYouTubeSegmentUseCase for tests that don't exercise the
// full per-segment pipeline (failure-path tests with a fake
// VideoPipeline that returns errors, classification-only tests, etc.).
//
// PR-GODOBJ-1: this helper is the canonical way for tests to wire
// ProcessSeg into NewService — the godlike/07 fail-closed panic
// rejects nil wiring so a stub is the minimum-required injection.
// The caller MUST supply pipeline (typically the test's own
// fakeVideoPipeline instance) so the per-segment path threads
// through the SAME mock observable at the Service-deps level
// (pipeline.called assertions stay reliable).
//
// PR-GRUPOC-2 (July 2026): the pre-PR ProcessSegmentDeps literal
// is replaced by 4 capability-area sub-bundles (Core carries the
// 4 required ports + SegmentsSvc + Log; Media/Metadata/Observability
// are zero-valued — the canonical no-op state).
func newTestProcessSegmentUseCase(log *zap.Logger, pipeline youtubeports.VideoPipelinePort) *ProcessYouTubeSegmentUseCase {
	return NewProcessYouTubeSegmentFromSubBundles(
		ProcessSegmentCoreDeps{
			Cache:         testStubClipCache{},
			VideoPipeline: pipeline,
			Hash:          testStubHash{},
			Writer:        testStubClipAtomicWriter{},
			SegmentsSvc:   NewSegmentsService(),
			Log:           log,
		},
		ProcessSegmentMediaDeps{},
		ProcessSegmentMetadataDeps{},
		ProcessSegmentObservabilityDeps{},
	)
}

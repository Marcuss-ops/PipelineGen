// Package usecase — process_segment_failfast_test.go locks in the
// Commit 1 fail-fast posture introduced in
// process_segment.go::NewProcessYouTubeSegmentUseCase.
//
// Contract under test: the canonical use case MUST panic at
// construction time when ANY of the verdict-required ports (Cache,
// VideoPipeline, Hash, Writer) is nil. The verdict's P0 #3
// fail-closed directive states that nil-port wiring must surface at
// boot rather than silently no-op (the pre-Commit-1
// silent-"processed"-without-write bug).
//
// SegmentsSvc is dependency-free and defaulted to NewSegmentsService()
// when nil — no panic. Subtitles / Transcriber are runtime-gated
// (steps 6 + 7 are guarded by `if u.deps.X != nil`); not panic-tested
// here. DriveFolderMgr is also gated at runtime (the upload step
// checks nil handler-side); not panic-tested here.
package usecase

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// stubs/unchanged — Build the minimal stub surface required to
// satisfy the 4 required ports so each panic test can verify the
// port-by-port panic ordering.

// stubVideoPipeline satisfies VideoPipelinePort (single method).
type stubVideoPipeline struct{}

func (stubVideoPipeline) DownloadAndCutYouTubeVideo(_ context.Context, _ youtubeports.VideoCutRequest) (*youtubeports.VideoCutResult, error) {
	return nil, nil
}

// stubHashService satisfies HashServicePort (four methods: SHA256 + MD5).
type stubHashService struct{}

func (stubHashService) SHA256File(_ string) (string, error) { return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", nil }
func (stubHashService) SHA256String(_ string) string        { return "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" }
func (stubHashService) MD5String(_ string) string           { return "" }
func (stubHashService) MD5File(_ string) (string, error)    { return "", nil }

// stubAtomicWriter satisfies ClipAtomicWriter (single method). Returns
// nil unconditionally; tests only exercise ctor panic shape, not
// the writer runtime.
//
// Commit 2/6 (PR-C-YouTube-Cutover, Correttezza #6): the port
// signature now takes `youtubetypes.ClipAsset` (the canonical, typed
// internal domain entity) instead of `youtubetypes.ExtractItem` (the
// HTTP response shape). The stub signature mirrors that.
type stubAtomicWriter struct{}

func (stubAtomicWriter) CommitClipAndIndexEvent(_ context.Context, _ string, _ youtubetypes.ClipAsset, _ youtubeports.IndexEventPayload) error {
	return nil
}

// youtubetypes_clipAsset is a local alias for youtubetypes.ClipAsset
// kept for legacy readers; the production alias `youtubetypes.ClipAsset`
// is used inline above.
type youtubetypes_clipAsset = youtubetypes.ClipAsset

// stubClipCache satisfies ClipCachePort (single method). The hash-map
// shape is intentionally bare — tests only exercise ctor panic.
type stubClipCache struct{}

func (stubClipCache) GetExisting(_ context.Context, _ string) (*youtubetypes.ExtractItem, bool, error) {
	return nil, false, nil
}

// validProcessSegmentDeps post-Commit-1 fix: SegmentsSvc MUST be wired
// (fail-fast panic for nil). DriveFolderMgr / Subtitles /
// Transcriber are runtime-gated (no panic). SegmentPolicy is the
// duration gate (Commit 2/6 #3); defaults to Min=4s/Max=60s when
// zero-valued, so tests don't need to set it. The 4s/60s window
// matches the user-requested clip-duration policy (no effects,
// no transitions applied by the YouTube extraction endpoint).
//
// PR-GRUPOC-2 (July 2026): the pre-PR ProcessSegmentDeps struct
// (17 fields) is RETIRED. The 17 fields are now split into 4
// capability-area sub-bundles (Core/Media/Metadata/Observability).
// The helper returns 4 sub-bundles — Core is wired with the 4
// required ports + SegmentsSvc + Log; Media, Metadata, and
// Observability are zero-valued (the canonical no-op state —
// their ports are optional, runtime-gated). Tests that need to
// override a specific port mutate the relevant sub-bundle after
// calling this helper.
func validProcessSegmentDeps() (ProcessSegmentCoreDeps, ProcessSegmentMediaDeps, ProcessSegmentMetadataDeps, ProcessSegmentObservabilityDeps) {
	return ProcessSegmentCoreDeps{
			Cache:         stubClipCache{},
			VideoPipeline: stubVideoPipeline{},
			Hash:          stubHashService{},
			Writer:        stubAtomicWriter{},
			SegmentsSvc:   NewSegmentsService(),
			Log:           zap.NewNop(),
		}, ProcessSegmentMediaDeps{},
		ProcessSegmentMetadataDeps{},
		ProcessSegmentObservabilityDeps{}
}

// TestNewProcessYouTubeSegmentUseCase_PanicsOnNilCache pins the
// fail-fast posture for the Cache port.
//
// PR-GRUPOC-2 (July 2026): the mutate pattern now operates on the
// 4 capability-area sub-bundles returned by validProcessSegmentDeps().
// The Cache port lives in ProcessSegmentCoreDeps; nil-ing it triggers
// the same byte-verbatim panic message as the pre-PR implementation.
func TestNewProcessYouTubeSegmentUseCase_PanicsOnNilCache(t *testing.T) {
	core, media, metadata, observability := validProcessSegmentDeps()
	core.Cache = nil
	require.PanicsWithValue(t,
		"usecase.NewProcessYouTubeSegmentUseCase: Cache port is required (composition must wire ClipCacheAdapter from internal/infrastructure/database/sqlite/assets/clip_cache_adapter.go)",
		func() { NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability) },
		"Commit 1 fail-fast: nil Cache MUST panic at ctor (P0 #3 silent-'processed' regression) ")
}

// TestNewProcessYouTubeSegmentUseCase_PanicsOnNilVideoPipeline pins
// the fail-fast posture for the VideoPipeline port.
func TestNewProcessYouTubeSegmentUseCase_PanicsOnNilVideoPipeline(t *testing.T) {
	core, media, metadata, observability := validProcessSegmentDeps()
	core.VideoPipeline = nil
	require.PanicsWithValue(t,
		"usecase.NewProcessYouTubeSegmentUseCase: VideoPipeline port is required (composition must wire the YouTube pipeline adapter)",
		func() { NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability) },
		"Commit 1 fail-fast: nil VideoPipeline MUST panic at ctor")
}

// TestNewProcessYouTubeSegmentUseCase_PanicsOnNilHash pins the
// fail-fast posture for the Hash port.
func TestNewProcessYouTubeSegmentUseCase_PanicsOnNilHash(t *testing.T) {
	core, media, metadata, observability := validProcessSegmentDeps()
	core.Hash = nil
	require.PanicsWithValue(t,
		"usecase.NewProcessYouTubeSegmentUseCase: Hash port is required (composition must wire hashutil.NewHashAdapter)",
		func() { NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability) },
		"Commit 1 fail-fast: nil Hash MUST panic at ctor")
}

// TestNewProcessYouTubeSegmentUseCase_PanicsOnNilWriter pins the
// fail-fast posture for the Writer port — the verdict's P0 #3
// explicit hard-wiring directive ("Writer assente: salta DB e
// outbox e termina comunque con out.Item.Status = \"processed\"").
func TestNewProcessYouTubeSegmentUseCase_PanicsOnNilWriter(t *testing.T) {
	core, media, metadata, observability := validProcessSegmentDeps()
	core.Writer = nil
	require.PanicsWithValue(t,
		"usecase.NewProcessYouTubeSegmentUseCase: Writer port is required — composition must wire ClipAtomicWriterAdapter (PR-C P0 #3 fail-closed; pre-Commit-1 silently wrote nothing and returned 'processed')",
		func() { NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability) },
		"Commit 1 fail-fast: nil Writer MUST panic at ctor (P0 #3 silent-success regression) ")
}

// TestNewProcessYouTubeSegmentUseCase_PanicsOnNilSegmentsSvc pins
// the fail-fast posture for the SegmentsSvc port — added in the
// post-review fix (user msg spec: Cache/Writer/Hash/SegmentsSvc).
// Pre-fix this slot defaulted to NewSegmentsService() silently;
// the explicit panic catches the case where a future SegmentsService
// refactor swaps the canonical impl for a stub the test fixtures
// don't expect.
func TestNewProcessYouTubeSegmentUseCase_PanicsOnNilSegmentsSvc(t *testing.T) {
	core, media, metadata, observability := validProcessSegmentDeps()
	core.SegmentsSvc = nil
	require.PanicsWithValue(t,
		"usecase.NewProcessYouTubeSegmentUseCase: SegmentsSvc port is required (composition must construct *SegmentsService via youtube.NewSegmentsService())",
		func() { NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability) },
		"Commit 1 fail-fast (post-review fix): nil SegmentsSvc MUST panic at ctor (user msg spec deviation; pre-fix silently defaulted to NewSegmentsService())")
}

// TestNewProcessYouTubeSegmentUseCase_HappyPath locks the canonical
// fully-wired ctor — verifies that with all required ports satisfied
// the ctor returns cleanly (no panic, returns a non-nil use case).
func TestNewProcessYouTubeSegmentUseCase_HappyPath(t *testing.T) {
	core, media, metadata, observability := validProcessSegmentDeps()
	uc := NewProcessYouTubeSegmentFromSubBundles(core, media, metadata, observability)
	require.NotNil(t, uc, "with all required ports wired the canonical use case MUST construct (no panic)")
}

// context import alias — the test file does not use context.Context
// directly but the stub signatures reference it for conformance.
// (kept for symmetry with other usecase tests that may need it.)
var _ = context.TODO

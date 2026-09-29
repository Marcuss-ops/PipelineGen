// Package usecase — extraction_staging_test.go: the merged-window staging
// contract.
//
// September 2026 replaced the "stage the whole source once, cut N locally"
// optimization with "stage ONLY the requested windows, merged into contiguous
// blocks, one yt-dlp --download-sections call per block". Two properties are
// load-bearing and pinned here:
//
//  1. the merge is aggressive enough to keep the invocation count low (a
//     contiguous 3-clip batch must be ONE sectioned download, not three) while
//     never downloading more than a bounded gap between distant windows, and
//  2. the mapping segment → (staged file, offset inside that file) is exactly
//     right, because the local cut is `segmentStart - offset`: an off-by-a-block
//     offset silently publishes the wrong seconds.
//
// The fail-soft matrix is pinned too: a block that cannot be staged must drop
// out of the result (its segments fall back to the per-segment yt-dlp path)
// rather than fail the extraction or be reported as staged.
package usecase

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaexec"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/ports"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// sectionStager is the acquisition.SourceStager double for the sectioned
// staging path. It records every Prepare so a test can assert HOW MANY blocks
// were staged and WITH WHICH section, and it can fail a chosen section to pin
// the fail-soft contract. Prepare is called from concurrent goroutines, so
// every access is mutex-guarded.
type sectionStager struct {
	mu           sync.Mutex
	prepares     []acquisition.PrepareRequest
	released     []string
	failSections map[string]bool
	failAll      bool
	// emptyPath makes Prepare succeed with a receipt carrying no local path,
	// the shape a misbehaving stager would return.
	emptyPath bool
}

func (s *sectionStager) Prepare(_ context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	s.mu.Lock()
	s.prepares = append(s.prepares, req)
	s.mu.Unlock()

	if s.failAll || s.failSections[req.Source.DownloadSection] {
		return nil, errors.New("cdn refused")
	}
	if s.emptyPath {
		return &acquisition.PrepareContext{}, nil
	}
	return &acquisition.PrepareContext{
		LocalPath:    "/cache/yt/" + sectionSlug(req.Source.DownloadSection) + ".mp4",
		CleanupToken: "token-" + req.Source.DownloadSection,
		SizeBytes:    1024,
		SHA256:       "deadbeef",
	}, nil
}

func (s *sectionStager) Release(_ context.Context, cleanupToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released = append(s.released, cleanupToken)
	return nil
}

var _ acquisition.SourceStager = (*sectionStager)(nil)

// recordedPrepares returns a copy of the recorded Prepare calls in arrival
// order (concurrent, so arrival order is not the block order).
func (s *sectionStager) recordedPrepares() []acquisition.PrepareRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]acquisition.PrepareRequest(nil), s.prepares...)
}

// recordedSections returns the staged DownloadSection values, sorted, so an
// assertion does not depend on goroutine scheduling.
func (s *sectionStager) recordedSections() []string {
	sections := make([]string, 0, len(s.recordedPrepares()))
	for _, req := range s.recordedPrepares() {
		sections = append(sections, req.Source.DownloadSection)
	}
	sort.Strings(sections)
	return sections
}

func sectionSlug(section string) string {
	replacer := strings.NewReplacer("*", "", ":", "", "-", "_")
	return replacer.Replace(section)
}

// countingFFProbe counts ProbeFacts calls so a test can pin the
// once-per-block probe contract (never once per segment).
type countingFFProbe struct {
	mu    sync.Mutex
	calls int
}

func (p *countingFFProbe) ValidateClip(_ context.Context, _ string, _ int, _ bool) (*youtubeports.FFProbeReport, error) {
	return &youtubeports.FFProbeReport{ContainerReadable: true, VideoStreamPresent: true}, nil
}

func (p *countingFFProbe) ProbeFacts(_ context.Context, _ string) (*mediaexec.MediaFacts, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	return &mediaexec.MediaFacts{
		VideoCodec: "h264",
		AudioCodec: "aac",
		Width:      1920,
		Height:     1080,
		FPSNum:     30,
		FPSDen:     1,
	}, nil
}

func (p *countingFFProbe) probeCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newStagingTestService builds the minimum ExtractionService whose per-segment
// use case carries stager. The rest of the deps are the canonical test stubs
// (the constructor fail-closes on nil wiring, godlike/07).
func newStagingTestService(stager acquisition.SourceStager) *ExtractionService {
	return newStagingTestServiceWithProbe(stager, nil)
}

func newStagingTestServiceWithProbe(stager acquisition.SourceStager, probe youtubeports.FFProbePort) *ExtractionService {
	uc := newTestProcessSegmentUseCase(zap.NewNop(), &fakeVideoPipeline{})
	uc.media.Stager = stager
	uc.media.FFProbe = probe
	return &ExtractionService{log: zap.NewNop(), processSeg: uc}
}

func stagingRequest() *youtubetypes.ExtractRequest {
	return &youtubetypes.ExtractRequest{URL: "https://www.youtube.com/watch?v=vid123"}
}

// stagingSegments builds n CONTIGUOUS one-minute segments.
func stagingSegments(n int) []youtubetypes.Segment {
	out := make([]youtubetypes.Segment, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, segment(i*60, (i+1)*60))
	}
	return out
}

// segment renders one segment from whole seconds using the canonical HH:MM:SS
// formatter — the same form every caller supplies and step1_BuildClipID parses.
func segment(startSec, endSec int) youtubetypes.Segment {
	return youtubetypes.Segment{
		Start: textutil.FormatSecondsToTimestamp(startSec),
		End:   textutil.FormatSecondsToTimestamp(endSec),
	}
}

// ── block merging ────────────────────────────────────────────────────────

// TestSectionBlocksForSegments_MergeMatrix pins when windows share a block.
// Merging too eagerly downloads seconds nobody asked for; merging too rarely
// pays the downloader's fixed per-invocation overhead once per window.
func TestSectionBlocksForSegments_MergeMatrix(t *testing.T) {
	cases := []struct {
		name        string
		segments    []youtubetypes.Segment
		wantWindows [][2]int
		wantIndexes [][]int
	}{
		{
			name:        "touching windows merge into one block",
			segments:    []youtubetypes.Segment{segment(0, 60), segment(60, 120)},
			wantWindows: [][2]int{{0, 120}},
			wantIndexes: [][]int{{0, 1}},
		},
		{
			name:        "overlapping windows merge into one block",
			segments:    []youtubetypes.Segment{segment(0, 30), segment(10, 45)},
			wantWindows: [][2]int{{0, 45}},
			wantIndexes: [][]int{{0, 1}},
		},
		{
			name:        "gap within tolerance merges into one block",
			segments:    []youtubetypes.Segment{segment(0, 10), segment(20, 30)},
			wantWindows: [][2]int{{0, 30}},
			wantIndexes: [][]int{{0, 1}},
		},
		{
			name:        "gap beyond tolerance splits",
			segments:    []youtubetypes.Segment{segment(0, 10), segment(40, 50)},
			wantWindows: [][2]int{{0, 10}, {40, 50}},
			wantIndexes: [][]int{{0}, {1}},
		},
		{
			name:        "unsorted input yields ascending blocks with caller indexes",
			segments:    []youtubetypes.Segment{segment(600, 660), segment(0, 60)},
			wantWindows: [][2]int{{0, 60}, {600, 660}},
			wantIndexes: [][]int{{1}, {0}},
		},
		{
			name:        "chain of near windows collapses into a single block",
			segments:    []youtubetypes.Segment{segment(0, 10), segment(20, 30), segment(40, 50)},
			wantWindows: [][2]int{{0, 50}},
			wantIndexes: [][]int{{0, 1, 2}},
		},
		{
			name:        "unparseable and inverted windows are excluded, never guessed",
			segments:    []youtubetypes.Segment{{Start: "bogus", End: "00:01:00"}, segment(30, 30), segment(0, 60)},
			wantWindows: [][2]int{{0, 60}},
			wantIndexes: [][]int{{2}},
		},
		{
			name:        "no usable window yields no block at all",
			segments:    []youtubetypes.Segment{{Start: "", End: ""}, {Start: "00:00:10", End: "00:00:05"}},
			wantWindows: nil,
			wantIndexes: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			blocks := sectionBlocksForSegments(tc.segments)
			// Declared without make so "no block" stays nil and compares equal to
			// the empty expectation instead of differing only by nil-vs-empty.
			var gotWindows [][2]int
			var gotIndexes [][]int
			for _, b := range blocks {
				gotWindows = append(gotWindows, [2]int{int(b.StartSec), int(b.EndSec)})
				gotIndexes = append(gotIndexes, b.SegmentIndexes)
			}
			if !reflect.DeepEqual(gotWindows, tc.wantWindows) {
				t.Fatalf("block windows = %v, want %v", gotWindows, tc.wantWindows)
			}
			if !reflect.DeepEqual(gotIndexes, tc.wantIndexes) {
				t.Fatalf("block segment indexes = %v, want %v", gotIndexes, tc.wantIndexes)
			}
		})
	}
}

// TestSectionDownloadString_RendersYtDlpAbsoluteRange pins the wire form the
// downloader passes to `--download-sections`.
func TestSectionDownloadString_RendersYtDlpAbsoluteRange(t *testing.T) {
	if got := sectionDownloadString(0, 120); got != "*00:00:00-00:02:00" {
		t.Fatalf("sectionDownloadString(0,120) = %q", got)
	}
	if got := sectionDownloadString(3661, 3721); got != "*01:01:01-01:02:01" {
		t.Fatalf("sectionDownloadString(3661,3721) = %q", got)
	}
}

// ── staging ──────────────────────────────────────────────────────────────

// TestStageSectionBlocks_ContiguousBatchStagesExactlyOneSection is the core
// regression guard for the whole change: a contiguous multi-segment batch must
// cost ONE sectioned download whose range covers exactly the published
// windows — not one download per segment (the per-segment cost the old
// download-once path existed to avoid) and not the whole source (what it did).
func TestStageSectionBlocks_ContiguousBatchStagesExactlyOneSection(t *testing.T) {
	stager := &sectionStager{}
	svc := newStagingTestService(stager)
	segments := stagingSegments(3)

	blocks := sectionBlocksForSegments(segments)
	if len(blocks) != 1 {
		t.Fatalf("contiguous 3-segment batch must collapse into 1 block, got %d", len(blocks))
	}
	staged := svc.stageSectionBlocks(context.Background(), stagingRequest(), "vid123", blocks)
	if len(staged) != 1 {
		t.Fatalf("staged sections = %d, want 1", len(staged))
	}
	if got := stager.recordedSections(); !reflect.DeepEqual(got, []string{"*00:00:00-00:03:00"}) {
		t.Fatalf("staged sections = %v, want one window covering the published seconds", got)
	}
	sources := segmentSourcesFromStaged(len(segments), staged)
	for i, src := range sources {
		if src.Path == "" {
			t.Fatalf("segment %d has no staged path; the run would re-download per segment", i)
		}
		if src.OffsetSec != 0 {
			t.Fatalf("segment %d offset = %v, want the block start (0)", i, src.OffsetSec)
		}
	}
}

// TestStageSectionBlocks_StagesOneDownloadPerBlockWithExactOffsets pins the
// block→segment mapping the local cut depends on: each segment is handed the
// file of ITS block and that block's start as the offset.
func TestStageSectionBlocks_StagesOneDownloadPerBlockWithExactOffsets(t *testing.T) {
	stager := &sectionStager{}
	probe := &countingFFProbe{}
	svc := newStagingTestServiceWithProbe(stager, probe)
	segments := []youtubetypes.Segment{
		segment(0, 20),
		segment(30, 50),   // 10s gap → same block as the first
		segment(600, 630), // far away → its own block
	}

	staged := svc.stageSectionBlocks(context.Background(), stagingRequest(), "vid123", sectionBlocksForSegments(segments))
	if len(staged) != 2 {
		t.Fatalf("staged sections = %d, want 2 blocks", len(staged))
	}
	wantSections := []string{"*00:00:00-00:00:50", "*00:10:00-00:10:30"}
	if got := stager.recordedSections(); !reflect.DeepEqual(got, wantSections) {
		t.Fatalf("staged sections = %v, want %v", got, wantSections)
	}

	for _, req := range stager.recordedPrepares() {
		if !req.Source.ForceKeyframes {
			t.Errorf("section %q staged without ForceKeyframes: the staged file would start at the keyframe BEFORE the block and every local seek would land early", req.Source.DownloadSection)
		}
		if req.Source.MergeFormat != "mp4" {
			t.Errorf("section %q MergeFormat = %q, want mp4", req.Source.DownloadSection, req.Source.MergeFormat)
		}
		if req.CallerRef != "youtube.extract.section:vid123" {
			t.Errorf("CallerRef = %q, want the section-staging owner ref", req.CallerRef)
		}
		if req.IdempotencyKey == "" {
			t.Errorf("section %q staged without an idempotency key", req.Source.DownloadSection)
		}
	}
	// Two windows of the SAME url must never share a stage: the section (and so
	// the key) differs, or a re-run would serve one block's bytes for the other.
	prepares := stager.recordedPrepares()
	if len(prepares) == 2 && prepares[0].IdempotencyKey == prepares[1].IdempotencyKey {
		t.Fatal("both blocks derived the SAME idempotency key; the staged windows would collide")
	}

	sources := segmentSourcesFromStaged(len(segments), staged)
	if sources[0].OffsetSec != 0 || sources[1].OffsetSec != 0 {
		t.Fatalf("segments of the first block must keep offset 0, got %v/%v", sources[0].OffsetSec, sources[1].OffsetSec)
	}
	if sources[2].OffsetSec != 600 {
		t.Fatalf("far segment offset = %v, want 600 (its block's start)", sources[2].OffsetSec)
	}
	if sources[0].Path == "" || sources[0].Path == sources[2].Path {
		t.Fatalf("blocks must map to distinct staged files; got %q and %q", sources[0].Path, sources[2].Path)
	}
	if sources[0].Facts == nil || sources[2].Facts == nil {
		t.Fatal("every staged block must carry its probed facts")
	}
	// Once PER BLOCK, never per segment (3 segments, 2 blocks).
	if got := probe.probeCalls(); got != 2 {
		t.Fatalf("ProbeFacts calls = %d, want 2 (one per staged block)", got)
	}
	// The receipts belong to the CALLER's stack: staging must not release them.
	if len(stager.released) != 0 {
		t.Fatalf("stageSectionBlocks must NOT release its own receipts; released=%v", stager.released)
	}

	svc.releaseStagedSections(context.Background(), svc.processSeg.FullSourceStager(), staged)
	if len(stager.released) != 2 {
		t.Fatalf("releaseStagedSections released %d receipts, want 2 (one per staged block)", len(stager.released))
	}
}

// TestStageSectionBlocks_DropsOnlyTheFailedBlock pins the fail-soft contract:
// a block the stager refuses must disappear from the result so its segments
// take the per-segment path, while the blocks that DID stage stay usable.
func TestStageSectionBlocks_DropsOnlyTheFailedBlock(t *testing.T) {
	stager := &sectionStager{failSections: map[string]bool{"*00:10:00-00:10:30": true}}
	svc := newStagingTestService(stager)
	segments := []youtubetypes.Segment{segment(0, 20), segment(600, 630)}

	staged := svc.stageSectionBlocks(context.Background(), stagingRequest(), "vid123", sectionBlocksForSegments(segments))
	if len(staged) != 1 {
		t.Fatalf("staged sections = %d, want the surviving block only", len(staged))
	}
	sources := segmentSourcesFromStaged(len(segments), staged)
	if sources[0].Path == "" {
		t.Fatal("the segment of the surviving block must keep its staged file")
	}
	if sources[1].Path != "" || sources[1].OffsetSec != 0 {
		t.Fatalf("the segment of the failed block must fall back (zero source), got path=%q offset=%v", sources[1].Path, sources[1].OffsetSec)
	}
}

// TestStageSectionBlocks_ReceiptWithoutLocalPathIsDropped pins the defensive
// branch: a stager that "succeeds" without a path is not a staged section.
func TestStageSectionBlocks_ReceiptWithoutLocalPathIsDropped(t *testing.T) {
	svc := newStagingTestService(&sectionStager{emptyPath: true})
	staged := svc.stageSectionBlocks(context.Background(), stagingRequest(), "vid123", sectionBlocksForSegments(stagingSegments(2)))
	if len(staged) != 0 {
		t.Fatalf("staged sections = %d, want 0 when the receipt carries no local path", len(staged))
	}
}

// TestStageSectionBlocks_SkipsWhenNotApplicable pins the no-op matrix: nothing
// may start a network stage when there is nothing to stage.
func TestStageSectionBlocks_SkipsWhenNotApplicable(t *testing.T) {
	blocks := sectionBlocksForSegments(stagingSegments(2))

	var nilSvc *ExtractionService
	if got := nilSvc.stageSectionBlocks(context.Background(), stagingRequest(), "vid123", blocks); got != nil {
		t.Fatal("a nil service must no-op")
	}

	stager := &sectionStager{}
	svc := newStagingTestService(stager)
	if got := svc.stageSectionBlocks(context.Background(), nil, "vid123", blocks); got != nil {
		t.Fatal("a nil request must no-op")
	}
	if got := svc.stageSectionBlocks(context.Background(), stagingRequest(), "vid123", nil); got != nil {
		t.Fatal("no blocks must no-op")
	}
	if got := svc.stageSectionBlocks(context.Background(), &youtubetypes.ExtractRequest{URL: "   "}, "vid123", blocks); got != nil {
		t.Fatal("an empty URL must no-op")
	}
	// A composition with no stager wired: the extraction still runs, on the
	// per-segment path.
	noStager := newStagingTestService(nil)
	if got := noStager.stageSectionBlocks(context.Background(), stagingRequest(), "vid123", blocks); got != nil {
		t.Fatal("an unwired stager must no-op instead of half-staging")
	}
	if len(stager.prepares) != 0 {
		t.Fatalf("no Prepare may be attempted for a no-op case; got %d", len(stager.prepares))
	}
}

// Package usecase — extraction_staging_test.go: the download-once decision
// matrix.
//
// Speed audit P0.1 (Sept 2026) introduced "stage the full source ONCE, then
// cut N segments locally" for multi-segment YouTube extracts, because every
// segment used to spawn its own `yt-dlp --download-sections` subprocess and
// the CDN throttling dominated 60-70% of wall time on a 9-clip batch.
//
// The behaviour had NO test at all: the file was added without coverage, so
// the two things that actually matter were unpinned —
//
//  1. it must stage ONCE for a multi-segment batch (one Prepare with an
//     EMPTY DownloadSection, i.e. the whole source), and
//  2. it must fall back to the per-segment path (nil,nil) rather than
//     half-stage in every case where the optimization cannot hold: single
//     segment, flag off, stager unwired, empty URL, prepare failure.
//
// A regression in (2) is the dangerous one: half-staging would either
// download the whole video for a one-clip request or leave segments with no
// pre-downloaded source and no fallback.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/acquisition"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// recordingStager is the SourceStager double. It records every Prepare so a
// test can assert that the full source was staged EXACTLY once, and with an
// empty DownloadSection (the whole video, not a section).
type recordingStager struct {
	prepares []acquisition.PrepareRequest
	released []string
	err      error
	// emptyPath makes Prepare succeed with a receipt that carries no
	// local path, the shape a misbehaving stager would return.
	emptyPath bool
}

func (s *recordingStager) Prepare(_ context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	s.prepares = append(s.prepares, req)
	if s.err != nil {
		return nil, s.err
	}
	if s.emptyPath {
		return &acquisition.PrepareContext{}, nil
	}
	return &acquisition.PrepareContext{
		LocalPath:    "/cache/yt/source.mp4",
		CleanupToken: "cleanup-token-1",
		SizeBytes:    1024,
		SHA256:       "deadbeef",
	}, nil
}

func (s *recordingStager) Release(_ context.Context, cleanupToken string) error {
	s.released = append(s.released, cleanupToken)
	return nil
}

var _ acquisition.SourceStager = (*recordingStager)(nil)

// newStagingTestService builds the minimum ExtractionService whose per-segment
// use case carries stager. The rest of the deps are the canonical test stubs
// (the constructor fail-closes on nil wiring, godlike/07).
func newStagingTestService(stager acquisition.SourceStager) *ExtractionService {
	uc := newTestProcessSegmentUseCase(zap.NewNop(), &fakeVideoPipeline{})
	uc.media.Stager = stager
	return &ExtractionService{log: zap.NewNop(), processSeg: uc}
}

func stagingRequest() *youtubetypes.ExtractRequest {
	return &youtubetypes.ExtractRequest{URL: "https://www.youtube.com/watch?v=vid123"}
}

// stagingSegments builds n one-minute segments. Segment start/end are the
// canonical "HH:MM:SS" strings, not seconds.
func stagingSegments(n int) []youtubetypes.Segment {
	out := make([]youtubetypes.Segment, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, youtubetypes.Segment{
			Start: fmt.Sprintf("00:%02d:00", i),
			End:   fmt.Sprintf("00:%02d:00", i+1),
		})
	}
	return out
}

// TestStageFullSourceOnce_StagesWholeSourceExactlyOnceForMultiSegment is the
// core optimization contract: a 3-segment batch performs ONE full-source
// stage, not three section downloads.
func TestStageFullSourceOnce_StagesWholeSourceExactlyOnceForMultiSegment(t *testing.T) {
	t.Setenv("VELOX_YOUTUBE_DOWNLOAD_ONCE", "true")
	stager := &recordingStager{}
	svc := newStagingTestService(stager)

	receipt, gotStager := svc.stageFullSourceOnce(context.Background(), stagingRequest(), "vid123", stagingSegments(3))

	if receipt == nil || gotStager == nil {
		t.Fatal("a multi-segment batch with a wired stager and the flag on MUST stage the full source")
	}
	if len(stager.prepares) != 1 {
		t.Fatalf("Prepare calls = %d, want exactly 1 (this is the whole point: download once, cut N)", len(stager.prepares))
	}
	prep := stager.prepares[0]
	if prep.Source.DownloadSection != "" {
		t.Fatalf("the staged source must be the WHOLE video (empty DownloadSection), got %q", prep.Source.DownloadSection)
	}
	if prep.Source.URL != "https://www.youtube.com/watch?v=vid123" {
		t.Fatalf("staged URL = %q, want the request URL", prep.Source.URL)
	}
	if prep.CallerRef != "youtube.extract.download-once:vid123" {
		t.Fatalf("CallerRef = %q, want the canonical download-once owner ref", prep.CallerRef)
	}
	if receipt.LocalPath != "/cache/yt/source.mp4" {
		t.Fatalf("receipt LocalPath = %q, want the staged path", receipt.LocalPath)
	}
	// The receipt belongs to the CALLER's stack (the service is shared across
	// concurrent requests, and a second request must not be able to clobber
	// the first one's token), so it is released by the caller — not here.
	if len(stager.released) != 0 {
		t.Fatalf("stageFullSourceOnce must NOT release its own receipt; released=%v", stager.released)
	}
}

// TestStageFullSourceOnce_SkipsWhenOptimizationCannotHold pins the fallback
// matrix. Every case must return (nil, nil) so the fanout runs the
// per-segment yt-dlp path, and none may call Prepare.
func TestStageFullSourceOnce_SkipsWhenOptimizationCannotHold(t *testing.T) {
	cases := []struct {
		name     string
		flag     string
		stager   acquisition.SourceStager
		segments []youtubetypes.Segment
		url      string
		// prepareAttempted distinguishes the two reasons a fallback happens:
		// the optimization is not applicable at all (no network work may be
		// started), or it was applicable and the stager failed (the attempt
		// IS how the failure is discovered, and it must not be retried).
		prepareAttempted bool
	}{
		{
			name:     "single segment",
			flag:     "true",
			stager:   &recordingStager{},
			segments: stagingSegments(1),
			url:      "https://www.youtube.com/watch?v=vid123",
		},
		{
			name:     "no segments",
			flag:     "true",
			stager:   &recordingStager{},
			segments: nil,
			url:      "https://www.youtube.com/watch?v=vid123",
		},
		{
			name:     "flag off",
			flag:     "false",
			stager:   &recordingStager{},
			segments: stagingSegments(3),
			url:      "https://www.youtube.com/watch?v=vid123",
		},
		{
			name:     "stager not wired",
			flag:     "true",
			stager:   nil,
			segments: stagingSegments(3),
			url:      "https://www.youtube.com/watch?v=vid123",
		},
		{
			name:     "empty url",
			flag:     "true",
			stager:   &recordingStager{},
			segments: stagingSegments(3),
			url:      "   ",
		},
		{
			name:             "prepare failure",
			flag:             "true",
			stager:           &recordingStager{err: errors.New("cdn refused")},
			segments:         stagingSegments(3),
			url:              "https://www.youtube.com/watch?v=vid123",
			prepareAttempted: true,
		},
		{
			name:             "prepare returns no local path",
			flag:             "true",
			stager:           &recordingStager{emptyPath: true},
			segments:         stagingSegments(3),
			url:              "https://www.youtube.com/watch?v=vid123",
			prepareAttempted: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VELOX_YOUTUBE_DOWNLOAD_ONCE", tc.flag)
			svc := newStagingTestService(tc.stager)
			req := &youtubetypes.ExtractRequest{URL: tc.url}

			receipt, stager := svc.stageFullSourceOnce(context.Background(), req, "vid123", tc.segments)

			if receipt != nil || stager != nil {
				t.Fatalf("must fall back to the per-segment path (nil,nil); got receipt=%v stager=%v", receipt, stager)
			}
			rec, ok := tc.stager.(*recordingStager)
			if !ok {
				return
			}
			if tc.prepareAttempted {
				// The attempt must be made exactly once: a failed stage must
				// not be retried per segment, or the CDN cost the stager was
				// meant to remove comes straight back.
				if len(rec.prepares) != 1 {
					t.Fatalf("Prepare attempts = %d, want exactly 1 (no per-segment retry)", len(rec.prepares))
				}
				return
			}
			if len(rec.prepares) != 0 {
				t.Fatalf("the optimization is not applicable here, so no network stage may start; got %d Prepare call(s)", len(rec.prepares))
			}
		})
	}
}

// TestStageFullSourceOnce_NilServiceAndRequestAreSafe pins the godlike/07
// posture on the shared service: the helper is reached from a fanout that may
// hold a degraded service, so nil inputs are an observable no-op, not a panic.
func TestStageFullSourceOnce_NilServiceAndRequestAreSafe(t *testing.T) {
	t.Setenv("VELOX_YOUTUBE_DOWNLOAD_ONCE", "true")

	var nilSvc *ExtractionService
	if receipt, stager := nilSvc.stageFullSourceOnce(context.Background(), stagingRequest(), "vid123", stagingSegments(3)); receipt != nil || stager != nil {
		t.Fatal("a nil service must no-op")
	}

	svc := newStagingTestService(&recordingStager{})
	if receipt, stager := svc.stageFullSourceOnce(context.Background(), nil, "vid123", stagingSegments(3)); receipt != nil || stager != nil {
		t.Fatal("a nil request must no-op")
	}

	// A service with no per-segment use case (disabled composition).
	bare := &ExtractionService{log: zap.NewNop()}
	if receipt, stager := bare.stageFullSourceOnce(context.Background(), stagingRequest(), "vid123", stagingSegments(3)); receipt != nil || stager != nil {
		t.Fatal("a service without the per-segment pipeline must no-op")
	}
}

// TestIsDownloadOnceEnabled_DefaultsOnAndAcceptsTheDocumentedSpellings pins
// the flag parser. The default is ON (the optimization is the intended
// behaviour); only an explicit negative disables it.
func TestIsDownloadOnceEnabled_DefaultsOnAndAcceptsTheDocumentedSpellings(t *testing.T) {
	on := []string{"", "true", "TRUE", "1", "yes", "on", "True"}
	for _, v := range on {
		t.Run("on/"+v, func(t *testing.T) {
			t.Setenv("VELOX_YOUTUBE_DOWNLOAD_ONCE", v)
			if !isDownloadOnceEnabled() {
				t.Fatalf("%q must keep download-once enabled", v)
			}
		})
	}
	off := []string{"false", "FALSE", "0", "off", "no", "Off"}
	for _, v := range off {
		t.Run("off/"+v, func(t *testing.T) {
			t.Setenv("VELOX_YOUTUBE_DOWNLOAD_ONCE", v)
			if isDownloadOnceEnabled() {
				t.Fatalf("%q must disable download-once", v)
			}
		})
	}
}

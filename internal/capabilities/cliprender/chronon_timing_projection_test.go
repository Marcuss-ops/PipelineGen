package cliprender

import (
	"context"
	"errors"
	"testing"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// stubTimingFetcher records how it was called so the projection's reference
// handling (storage key + URL both forwarded) is observable.
type stubTimingFetcher struct {
	raw     []byte
	err     error
	calls   int
	lastKey string
	lastURL string
}

func (f *stubTimingFetcher) FetchChrononTiming(_ context.Context, key, url string) ([]byte, error) {
	f.calls++
	f.lastKey, f.lastURL = key, url
	if f.err != nil {
		return nil, f.err
	}
	return f.raw, nil
}

func newTimingProjectionWorker(rec *recordingRecorder, fetcher ChrononTimingFetcher) *Worker {
	w := &Worker{log: zap.NewNop()}
	w.SetChrononMetrics(NewChrononMetricsAdapter(rec, zap.NewNop()), fetcher)
	return w
}

// TestPublishChrononTimingRecordsMeasuredPhasesFromTheReferencedSidecar pins
// the whole chain: the outcome's content-addressed reference is fetched once,
// parsed once, and every measured phase lands in performance_operations with
// the certified facts attached.
func TestPublishChrononTimingRecordsMeasuredPhasesFromTheReferencedSidecar(t *testing.T) {
	rec := &recordingRecorder{}
	fetcher := &stubTimingFetcher{raw: []byte(chrononSidecarFixture)}
	w := newTimingProjectionWorker(rec, fetcher)

	outcome := &RenderOutcome{
		SizeBytes:               2_500_000,
		ChrononTimingStorageKey: "timing-key-1",
		ChrononTimingURL:        "http://store:9000/objects/timing-key-1",
	}
	prepared := &Prepared{Source: &MaterializedAsset{SHA256: "source-digest", DurationMS: 45_000}}
	probe := &OutputProbe{Width: 1920, Height: 1080, FPS: 30}

	w.publishChrononTiming(context.Background(), outcome, prepared, probe)

	if fetcher.calls != 1 {
		t.Fatalf("fetch calls = %d, want exactly 1", fetcher.calls)
	}
	if fetcher.lastKey != "timing-key-1" || fetcher.lastURL != "http://store:9000/objects/timing-key-1" {
		t.Fatalf("fetch args = (%q, %q), want the outcome's storage key and URL", fetcher.lastKey, fetcher.lastURL)
	}
	if len(rec.reports) != 7 {
		t.Fatalf("recorded %d phase rows, want 7 (one per measured phase)", len(rec.reports))
	}
	for _, report := range rec.reports {
		if report.Stage != string(StageClipRender) {
			t.Errorf("report stage = %q, want %q", report.Stage, StageClipRender)
		}
		if report.Component != string(kernobs.ComponentChronon) {
			t.Errorf("report component = %q, want %q", report.Component, kernobs.ComponentChronon)
		}
		if report.SourceSHA256 != "source-digest" || report.SourceDurationMS != 45_000 ||
			report.OutputSizeBytes != 2_500_000 || report.Width != 1920 || report.Height != 1080 || report.FPS != 30 {
			t.Errorf("report %s carries incomplete certified facts: %+v", report.Operation, report)
		}
	}
}

// TestPublishChrononTimingNeverFailsTheRender pins the fail-open contract: on
// every failure path the projection records nothing and does not panic — a
// render that produced certified bytes stays complete whether or not its
// metrics could be recorded.
func TestPublishChrononTimingNeverFailsTheRender(t *testing.T) {
	withRef := &RenderOutcome{ChrononTimingStorageKey: "k", ChrononTimingURL: "http://store/x"}

	cases := []struct {
		name    string
		worker  func(rec *recordingRecorder) *Worker
		outcome *RenderOutcome
		want    int
	}{
		{
			name: "fetch failure",
			worker: func(rec *recordingRecorder) *Worker {
				return newTimingProjectionWorker(rec, &stubTimingFetcher{err: errors.New("store down")})
			},
			outcome: withRef,
			want:    0,
		},
		{
			name: "unparsable sidecar",
			worker: func(rec *recordingRecorder) *Worker {
				return newTimingProjectionWorker(rec, &stubTimingFetcher{raw: []byte("not json")})
			},
			outcome: withRef,
			want:    0,
		},
		{
			name: "no timing reference",
			worker: func(rec *recordingRecorder) *Worker {
				return newTimingProjectionWorker(rec, &stubTimingFetcher{raw: []byte(chrononSidecarFixture)})
			},
			outcome: &RenderOutcome{SizeBytes: 1},
			want:    0,
		},
		{
			name: "projection unwired",
			worker: func(*recordingRecorder) *Worker {
				return &Worker{log: zap.NewNop()}
			},
			outcome: withRef,
			want:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingRecorder{}
			w := tc.worker(rec)
			w.publishChrononTiming(context.Background(), tc.outcome, nil, nil)
			if len(rec.reports) != tc.want {
				t.Fatalf("recorded %d rows, want %d", len(rec.reports), tc.want)
			}
		})
	}
}

// TestSetChrononMetricsRequiresBothHalves pins that a half-wiring (adapter
// without fetcher, or fetcher without adapter) leaves the projection OFF
// rather than recording facts nobody fetched.
func TestSetChrononMetricsRequiresBothHalves(t *testing.T) {
	rec := &recordingRecorder{}

	onlyAdapter := &Worker{log: zap.NewNop()}
	onlyAdapter.SetChrononMetrics(NewChrononMetricsAdapter(rec, zap.NewNop()), nil)
	if onlyAdapter.chrononMetrics != nil || onlyAdapter.chrononTimingFetcher != nil {
		t.Fatal("adapter without fetcher must leave the projection off")
	}

	onlyFetcher := &Worker{log: zap.NewNop()}
	onlyFetcher.SetChrononMetrics(nil, &stubTimingFetcher{})
	if onlyFetcher.chrononMetrics != nil || onlyFetcher.chrononTimingFetcher != nil {
		t.Fatal("fetcher without adapter must leave the projection off")
	}

	both := &Worker{log: zap.NewNop()}
	both.SetChrononMetrics(NewChrononMetricsAdapter(rec, zap.NewNop()), &stubTimingFetcher{})
	if both.chrononMetrics == nil || both.chrononTimingFetcher == nil {
		t.Fatal("both halves must enable the projection")
	}
}

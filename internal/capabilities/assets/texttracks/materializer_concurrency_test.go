package texttracks

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

type boundedTestTranslator struct {
	active atomic.Int32
	peak   atomic.Int32
	calls  atomic.Int32
	width  int32
	ready  chan struct{}
	fail   string
	delay  time.Duration
	budget chan struct{}
}

func (p *boundedTestTranslator) Translate(ctx context.Context, cmd translation.TranslationCommand) (translation.TranslationResult, error) {
	p.calls.Add(1)
	if p.budget != nil {
		select {
		case p.budget <- struct{}{}:
			defer func() { <-p.budget }()
		case <-ctx.Done():
			return translation.TranslationResult{}, ctx.Err()
		}
	}
	n := p.active.Add(1)
	defer p.active.Add(-1)
	for peak := p.peak.Load(); n > peak; peak = p.peak.Load() {
		if p.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	if p.ready != nil {
		if n == p.width {
			select {
			case <-p.ready:
			default:
				close(p.ready)
			}
		}
		select {
		case <-p.ready:
		case <-ctx.Done():
			return translation.TranslationResult{}, ctx.Err()
		}
	}
	if p.delay > 0 {
		select {
		case <-time.After(p.delay):
		case <-ctx.Done():
			return translation.TranslationResult{}, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return translation.TranslationResult{}, err
	}
	if cmd.TargetLang == p.fail {
		return translation.TranslationResult{}, errors.New("injected language failure")
	}
	return translation.TranslationResult{TranslatedText: "translated " + cmd.TargetLang, SourceLang: cmd.SourceLang, TargetLang: cmd.TargetLang, UsedProvider: "test", UsedModel: "test-model"}, nil
}

func TestMaterializerBoundedFanoutAndDeterministicReport(t *testing.T) {
	for _, width := range []int{1, 4, 10} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			repo := newFakeRepo()
			seedSourceTrack(repo, "asset", "en", detail.TextTrackTranscript, "v1", "source")
			langs := []string{"it", "es", "fr", "de", "pt", "pl", "ru", "tr", "id", "nl"}
			tr := &boundedTestTranslator{width: int32(width), ready: make(chan struct{}), fail: "fr", delay: time.Millisecond}
			m := newTestMaterializer(t, repo, tr, &fakeOutbox{}, "en", []string{"en", "it"}, "v1", "p1")
			m.SetConcurrency(width)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			report, err := m.Materialize(ctx, "asset", "en", ComputeSourceTextHash("source"), detail.TextTrackTranscript, langs)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"it", "es", "de", "pt", "pl", "ru", "tr", "id", "nl"}
			if !reflect.DeepEqual(report.CreatedLanguages, want) {
				t.Fatalf("created order = %v, want %v", report.CreatedLanguages, want)
			}
			if len(report.FailedLanguages) != 1 || report.FailedLanguages["fr"] == "" {
				t.Fatalf("failure not isolated: %+v", report)
			}
			if tr.peak.Load() != int32(width) || tr.active.Load() != 0 {
				t.Fatalf("peak=%d active=%d, want bounded width %d and drained work", tr.peak.Load(), tr.active.Load(), width)
			}
			before := tr.calls.Load()
			reused, err := m.Materialize(ctx, "asset", "en", ComputeSourceTextHash("source"), detail.TextTrackTranscript, langs)
			if err != nil || !reflect.DeepEqual(reused.SkippedLanguages, want) || tr.calls.Load()-before != 1 {
				t.Fatalf("retry must translate only failed language: report=%+v err=%v calls=%d", reused, err, tr.calls.Load()-before)
			}
		})
	}
}

func TestMaterializerCancellationDrainsWithoutReadyTranslations(t *testing.T) {
	repo := newFakeRepo()
	seedSourceTrack(repo, "asset", "en", detail.TextTrackTranscript, "v1", "source")
	tr := &boundedTestTranslator{delay: time.Hour}
	m := newTestMaterializer(t, repo, tr, &fakeOutbox{}, "en", []string{"en", "it", "es", "fr"}, "v1", "p1")
	m.SetConcurrency(2)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	report, _ := m.Materialize(ctx, "asset", "en", ComputeSourceTextHash("source"), detail.TextTrackTranscript, nil)
	if report == nil || len(report.CreatedLanguages) != 0 || len(report.FailedLanguages) != 3 || tr.active.Load() != 0 {
		t.Fatalf("cancelled work was not drained fail-closed: report=%+v active=%d", report, tr.active.Load())
	}
}

func TestNormalizeReportOrderPreservesAllLists(t *testing.T) {
	report := &MaterializationReport{CreatedLanguages: []string{"unknown", "fr", "it"}, SkippedLanguages: []string{"es", "it"}, RetranslatedLanguages: []string{"fr", "es"}}
	normalizeReportOrder([]string{"it", "es", "fr"}, report)
	if !reflect.DeepEqual(report.CreatedLanguages, []string{"it", "fr", "unknown"}) || !reflect.DeepEqual(report.SkippedLanguages, []string{"it", "es"}) || !reflect.DeepEqual(report.RetranslatedLanguages, []string{"es", "fr"}) {
		t.Fatalf("noncanonical report: %+v", report)
	}
	normalizeReportOrder(nil, nil)
}

// Fixed identical workload, fresh repository each iteration, and an upstream
// admission budget of three. This measures orchestration, not model speed.
func BenchmarkMaterializerBoundedFanout(b *testing.B) {
	langs := []string{"it", "es", "fr", "de", "pt", "pl", "ru", "tr", "id", "nl"}
	registry, err := asset.NewLanguageRegistryFromCodes(append([]string{"en"}, langs...))
	if err != nil {
		b.Fatal(err)
	}
	for _, width := range []int{1, 4, 10} {
		b.Run(fmt.Sprint(width), func(b *testing.B) {
			var calls int32
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				repo := newFakeRepo()
				seedSourceTrack(repo, "asset", "en", detail.TextTrackTranscript, "v1", "source")
				tr := &boundedTestTranslator{delay: 2 * time.Millisecond, budget: make(chan struct{}, 3)}
				m, err := NewMaterializer(repo, tr, &fakeOutbox{}, ResolverConfig{Registry: registry, SourceLanguage: "en", ModelVersion: "v1", PromptVersion: "p1"}, zap.NewNop())
				if err != nil {
					b.Fatal(err)
				}
				m.SetConcurrency(width)
				b.StartTimer()
				report, err := m.Materialize(context.Background(), "asset", "en", ComputeSourceTextHash("source"), detail.TextTrackTranscript, langs)
				if err != nil || len(report.CreatedLanguages) != len(langs) || tr.peak.Load() > 3 {
					b.Fatalf("report=%+v err=%v peak=%d", report, err, tr.peak.Load())
				}
				calls += tr.calls.Load()
			}
			b.ReportMetric(float64(calls)/float64(b.N), "translations/op")
			b.ReportMetric(3, "upstream-limit")
		})
	}
}

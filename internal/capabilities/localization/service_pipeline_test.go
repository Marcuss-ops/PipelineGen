package localization

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/render"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
)

type pipelineRenderExecutor struct {
	started atomic.Int32
	third   chan struct{}
}

type pipelineCompiler struct{}

func (pipelineCompiler) Compile(_ context.Context, plan LocalizedClipPlan) (render.RenderPlan, error) {
	return render.RenderPlan{OutputPath: "/tmp/renders/" + plan.TargetLanguage + ".mp4"}, nil
}

// pipelineTrackLanguages is the fixture's text-track store: each persisted
// track ID maps to the ONE language that track carries. A persisted track holds
// a single language, so a plan rendering Italian must reference the Italian
// track — the subtitle wire refuses to burn a track whose language contradicts
// the plan it is rendering (godlike/07).
var pipelineTrackLanguages = map[int64]string{
	202: "es",
	203: "en",
	204: "it",
}

// pipelineTrackIDForLanguage returns the persisted track ID carrying lang, so
// the fixture never claims a single track serves several target languages.
func pipelineTrackIDForLanguage(t *testing.T, lang string) int64 {
	t.Helper()
	for id, language := range pipelineTrackLanguages {
		if language == lang {
			return id
		}
	}
	t.Fatalf("fixture has no text track for language %q", lang)
	return 0
}

// pipelineSubtitleResolver resolves through the fixture store above, so an
// unknown track ID fails like a real lookup miss instead of yielding an
// untyped track that would mask a mistyped fixture.
type pipelineSubtitleResolver struct{}

func (pipelineSubtitleResolver) ResolveSubtitleTrack(_ context.Context, trackID int64, expectedSHA256 string) (*ResolvedSubtitleTrack, error) {
	language, ok := pipelineTrackLanguages[trackID]
	if !ok {
		return nil, fmt.Errorf("text track %d not found", trackID)
	}
	return &ResolvedSubtitleTrack{
		TrackID: trackID, LanguageCode: language,
		Cues:     []detail.TimedCue{{StartMs: 0, EndMs: 1000, Text: "cue"}},
		TextHash: expectedSHA256,
	}, nil
}

type pipelineSubtitleCompiler struct{}

func (pipelineSubtitleCompiler) Compile(_ context.Context, in SubtitleCompileInput) (*SubtitleAsset, error) {
	return &SubtitleAsset{LocalPath: "/tmp/subtitles/" + in.Language + ".ass", SHA256: "ass-sha", StyleHash: in.StyleHash, TrackID: in.TrackID}, nil
}

func (e *pipelineRenderExecutor) Execute(_ context.Context, _ render.RenderPlan, _ *SubtitleAsset) (RenderFacts, error) {
	n := e.started.Add(1)
	if n == 3 {
		close(e.third)
	}
	return validRenderFacts(), nil
}

type blockingPipelineUploader struct {
	started chan struct{}
	release <-chan struct{}
	mu      sync.Mutex
	calls   int
}

type boundedPipelineUploader struct {
	mu     sync.Mutex
	active int
	max    int
	calls  int
	delay  time.Duration
	failAt map[int]error
}

func (u *boundedPipelineUploader) Upload(ctx context.Context, in DriveUploadInput) (*DriveUploadResult, error) {
	u.mu.Lock()
	u.calls++
	call := u.calls
	u.active++
	if u.active > u.max {
		u.max = u.active
	}
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.active--
		u.mu.Unlock()
	}()
	select {
	case <-time.After(u.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := u.failAt[call]; err != nil {
		return nil, err
	}
	return &DriveUploadResult{FileID: "drive-" + in.Language, Link: "https://drive/" + in.Language}, nil
}

func (u *blockingPipelineUploader) Upload(_ context.Context, in DriveUploadInput) (*DriveUploadResult, error) {
	u.mu.Lock()
	u.calls++
	u.mu.Unlock()
	select {
	case u.started <- struct{}{}:
	default:
	}
	<-u.release
	return &DriveUploadResult{FileID: "drive-" + in.Language, Link: "https://drive/" + in.Language}, nil
}

func TestService_RenderContinuesWhileDriveUploadIsBlocked(t *testing.T) {
	executor := &pipelineRenderExecutor{third: make(chan struct{})}
	renderer := newTestRenderer(t,
		pipelineCompiler{},
		newTestWire(t, pipelineSubtitleResolver{}, pipelineSubtitleCompiler{}),
		executor,
	)
	driveRelease := make(chan struct{})
	uploader := &blockingPipelineUploader{started: make(chan struct{}, 3), release: driveRelease}
	drivePublisher := newTestPublisher(t, uploader)
	docAssembler, err := NewDocumentAssembler(&fakeDocPublisher{result: &DocPublishResult{ID: "doc", Link: "https://docs/doc"}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewServiceWithConcurrency(renderer, drivePublisher, docAssembler, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	plans := make([]LocalizedClipPlan, 3)
	for i, lang := range []string{"en", "es", "it"} {
		plans[i] = validPlan()
		plans[i].TargetLanguage = lang
		plans[i].SubtitleTrackID = pipelineTrackIDForLanguage(t, lang)
		plans[i].Priority = i
		plans[i].Fingerprint = Fingerprint(plans[i])
	}

	done := make(chan *LocalizeResult, 1)
	go func() {
		result, localizeErr := service.Localize(context.Background(), LocalizeInput{
			Concurrency:  2,
			FolderID:     "folder",
			SkipDocument: true,
			Plans:        plans,
		})
		if localizeErr != nil {
			done <- nil
			return
		}
		done <- result
	}()

	select {
	case <-executor.third:
		// The third render started while the first two uploads were blocked.
	case <-time.After(2 * time.Second):
		close(driveRelease)
		t.Fatalf("third render did not start while Drive uploads were blocked; renders_started=%d", executor.started.Load())
	}
	close(driveRelease)
	select {
	case result := <-done:
		if result == nil || len(result.Artifacts) != 3 || len(result.Failures) != 0 {
			t.Fatalf("unexpected pipeline result: %+v", result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pipeline did not finish after Drive was released")
	}
}

func TestService_UploadRenderedDoesNotInvokeRenderer(t *testing.T) {
	executor := &pipelineRenderExecutor{third: make(chan struct{})}
	renderer := newTestRenderer(t,
		pipelineCompiler{},
		newTestWire(t, pipelineSubtitleResolver{}, pipelineSubtitleCompiler{}),
		executor,
	)
	uploader := &fakeDriveUploader{result: &DriveUploadResult{FileID: "drive-recovered", Link: "https://drive/recovered"}}
	service, err := NewServiceWithConcurrency(renderer, newTestPublisher(t, uploader),
		mustTestDocumentAssembler(t), 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	art := renderedArtifact()
	out, err := service.UploadRendered(context.Background(), art, "folder")
	if err != nil {
		t.Fatalf("UploadRendered: %v", err)
	}
	if out.Status != LocalizedClipUploaded || out.DriveFileID != "drive-recovered" {
		t.Fatalf("unexpected recovered artifact: %+v", out)
	}
	if executor.started.Load() != 0 {
		t.Fatalf("upload-only recovery invoked renderer %d time(s)", executor.started.Load())
	}
}

func mustTestDocumentAssembler(t *testing.T) *DocumentAssembler {
	t.Helper()
	a, err := NewDocumentAssembler(&fakeDocPublisher{result: &DocPublishResult{ID: "doc", Link: "https://docs/doc"}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestService_UploadPoolIsBoundedAndFailureIsolated(t *testing.T) {
	executor := &pipelineRenderExecutor{third: make(chan struct{})}
	renderer := newTestRenderer(t, pipelineCompiler{}, newTestWire(t, pipelineSubtitleResolver{}, pipelineSubtitleCompiler{}), executor)
	uploader := &boundedPipelineUploader{delay: 5 * time.Millisecond, failAt: map[int]error{2: context.DeadlineExceeded}}
	service, err := NewServiceWithConcurrency(renderer, newTestPublisher(t, uploader), mustTestDocumentAssembler(t), 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	plans := make([]LocalizedClipPlan, 20)
	for i := range plans {
		plans[i] = validPlan()
		plans[i].SceneID = fmt.Sprintf("scene-%d", i)
		plans[i].ClipID = fmt.Sprintf("clip-%d", i)
		plans[i].TargetLanguage = "es"
		plans[i].Priority = i
		plans[i].Fingerprint = Fingerprint(plans[i])
	}
	result, err := service.Localize(context.Background(), LocalizeInput{Concurrency: 3, FolderID: "folder", SkipDocument: true, Plans: plans})
	if err != nil {
		t.Fatalf("Localize: %v", err)
	}
	if len(result.Artifacts) != 19 || len(result.Failures) != 1 {
		t.Fatalf("unexpected isolated failure result: artifacts=%d failures=%d", len(result.Artifacts), len(result.Failures))
	}
	uploader.mu.Lock()
	max := uploader.max
	uploader.mu.Unlock()
	if max > 2 {
		t.Fatalf("upload concurrency exceeded bound: max=%d", max)
	}
}

func TestService_ScalingAndConcurrentJobs(t *testing.T) {
	executor := &pipelineRenderExecutor{third: make(chan struct{})}
	renderer := newTestRenderer(t, pipelineCompiler{}, newTestWire(t, pipelineSubtitleResolver{}, pipelineSubtitleCompiler{}), executor)
	uploader := &boundedPipelineUploader{delay: time.Millisecond, failAt: map[int]error{}}
	service, err := NewServiceWithConcurrency(renderer, newTestPublisher(t, uploader), mustTestDocumentAssembler(t), 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{5, 20, 50} {
		t.Run(fmt.Sprintf("scaling_%d", size), func(t *testing.T) {
			result, err := service.Localize(context.Background(), LocalizeInput{
				Concurrency: 3, FolderID: "folder", SkipDocument: true, Plans: pipelinePlans(size),
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Artifacts) != size || len(result.Failures) != 0 {
				t.Fatalf("size=%d: artifacts=%d failures=%d", size, len(result.Artifacts), len(result.Failures))
			}
		})
	}

	jobs := make(chan *LocalizeResult, 3)
	for i := 0; i < 3; i++ {
		go func() {
			result, err := service.Localize(context.Background(), LocalizeInput{
				Concurrency: 3, FolderID: "folder", SkipDocument: true, Plans: pipelinePlans(20),
			})
			if err != nil {
				jobs <- nil
				return
			}
			jobs <- result
		}()
	}
	for i := 0; i < 3; i++ {
		result := <-jobs
		if result == nil || len(result.Artifacts) != 20 || len(result.Failures) != 0 {
			t.Fatalf("concurrent job %d did not complete 20/20", i)
		}
	}
}

func TestService_Soak100RunsHasNoCumulativeFailures(t *testing.T) {
	executor := &pipelineRenderExecutor{third: make(chan struct{})}
	renderer := newTestRenderer(t, pipelineCompiler{}, newTestWire(t, pipelineSubtitleResolver{}, pipelineSubtitleCompiler{}), executor)
	uploader := &boundedPipelineUploader{delay: time.Microsecond, failAt: map[int]error{}}
	service, err := NewServiceWithConcurrency(renderer, newTestPublisher(t, uploader), mustTestDocumentAssembler(t), 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	plans := pipelinePlans(5)
	for run := 0; run < 100; run++ {
		result, err := service.Localize(context.Background(), LocalizeInput{Concurrency: 2, FolderID: "folder", SkipDocument: true, Plans: plans})
		if err != nil || result == nil || len(result.Artifacts) != len(plans) || len(result.Failures) != 0 {
			t.Fatalf("soak run %d failed: err=%v result=%+v", run, err, result)
		}
	}
}

func pipelinePlans(n int) []LocalizedClipPlan {
	plans := make([]LocalizedClipPlan, n)
	for i := range plans {
		plans[i] = validPlan()
		plans[i].SceneID = fmt.Sprintf("scene-%d", i)
		plans[i].ClipID = fmt.Sprintf("clip-%d", i)
		plans[i].TargetLanguage = "es"
		plans[i].Priority = i
		plans[i].Fingerprint = Fingerprint(plans[i])
	}
	return plans
}

// TestRenderWidth pins the clamp that makes the shared render gate legible: a
// request wider than the machine bound resolves to the bound and reports
// cramped, so the composition root can warn instead of letting the operator pay
// an unexplained extra per-scene wait (2026-09-21 tail audit: the fifth of five
// scenes always waited for a slot at global=4 while the payload asked 5).
func TestRenderWidth(t *testing.T) {
	cases := []struct {
		name      string
		machine   int
		requested int
		want      int
		cramped   bool
	}{
		{name: "request under the bound", machine: 6, requested: 5, want: 5},
		{name: "request equal to the bound", machine: 5, requested: 5, want: 5},
		{name: "request above the bound is cramped", machine: 4, requested: 5, want: 4, cramped: true},
		{name: "unset machine bound is not a bound", machine: 0, requested: 5, want: 5},
		{name: "zero request falls back to the default", machine: 6, requested: 0, want: DefaultRenderConcurrency},
		{name: "negative request falls back to the default", machine: 0, requested: -3, want: DefaultRenderConcurrency},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, cramped := RenderWidth(tc.machine, tc.requested)
			if got != tc.want || cramped != tc.cramped {
				t.Fatalf("RenderWidth(%d, %d) = (%d, %v), want (%d, %v)", tc.machine, tc.requested, got, cramped, tc.want, tc.cramped)
			}
		})
	}
}

// TestService_DocumentAssemblyRequiresUploadedLinks pins the tail invariant:
// the manifest IS the set of uploaded Drive links, so a plan whose upload
// failed is absent from the doc and every entry that reaches it carries a
// non-empty link. It exists because "assemble the doc once the renders are
// ready, in parallel with the remaining uploads" reads like a safe latency win
// but publishes a manifest with blank links (godlike/07 no-fake-availability).
func TestService_DocumentAssemblyRequiresUploadedLinks(t *testing.T) {
	executor := &pipelineRenderExecutor{third: make(chan struct{})}
	renderer := newTestRenderer(t, pipelineCompiler{}, newTestWire(t, pipelineSubtitleResolver{}, pipelineSubtitleCompiler{}), executor)
	// The 2nd upload fails: exactly one plan must be dropped from the manifest.
	uploader := &boundedPipelineUploader{delay: time.Millisecond, failAt: map[int]error{2: context.DeadlineExceeded}}
	docs := &fakeDocPublisher{result: &DocPublishResult{ID: "doc", Link: "https://docs/doc"}}
	assembler, err := NewDocumentAssembler(docs)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewServiceWithConcurrency(renderer, newTestPublisher(t, uploader), assembler, 2, 2)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Localize(context.Background(), LocalizeInput{
		Concurrency: 2, FolderID: "folder", DocTitle: "Localization", Plans: pipelinePlans(3),
	})
	if err != nil {
		t.Fatalf("Localize: %v", err)
	}
	if len(result.Artifacts) != 2 || len(result.Failures) != 1 {
		t.Fatalf("artifacts=%d failures=%d, want 2/1", len(result.Artifacts), len(result.Failures))
	}
	if result.Ref == nil || len(result.Ref.Entries) != 2 {
		t.Fatalf("doc ref = %+v, want exactly the 2 uploaded plans", result.Ref)
	}
	for _, entry := range result.Ref.Entries {
		if entry.DriveLink == "" || entry.DriveFileID == "" {
			t.Fatalf("doc entry reached the manifest without its Drive link: %+v", entry)
		}
	}
	if strings.Contains(docs.got.Content, "no link") {
		t.Fatalf("manifest lists a clip with no link:\n%s", docs.got.Content)
	}
	if got := strings.Count(docs.got.Content, "<a href="); got != 2 {
		t.Fatalf("manifest links = %d, want 2 (the failed upload must not be listed)", got)
	}
}

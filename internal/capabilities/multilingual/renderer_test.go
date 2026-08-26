package multilingual

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"go.uber.org/zap"
)

// ── mock RustRenderer ─────────────────────────────────────────────────────

type mockRustRenderer struct {
	mu    sync.Mutex
	calls int
}

func newMockRustRenderer() *mockRustRenderer { return &mockRustRenderer{} }

func (m *mockRustRenderer) RenderClip(_ context.Context, plan cliprender.ClipRenderPlanV1) (RustRenderResult, error) {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	// Write a real output file on disk so the probe + sha256 + publish phases succeed.
	if plan.OutputPath != "" {
		if err := os.MkdirAll(filepath.Dir(plan.OutputPath), 0o755); err != nil {
			return RustRenderResult{}, fmt.Errorf("mock mkdir: %w", err)
		}
		data := make([]byte, 100)
		for i := range data {
			data[i] = byte("abcdefghijklmnopqrstuvwxyz"[i%26])
		}
		if err := os.WriteFile(plan.OutputPath, data, 0o644); err != nil {
			return RustRenderResult{}, fmt.Errorf("mock write: %w", err)
		}
	}
	return RustRenderResult{OutputPath: plan.OutputPath}, nil
}

// mockOutputProber returns a clip.render OutputProbe with contract-compliant
// values for the canonical VeloxEditingClipV1 contract.
type mockOutputProber struct {
	videoCodec  string
	pixelFormat string
	width       int
	height      int
	fps         float64
	audioCodec  string
	sampleRate  int
	channels    int
}

func (p *mockOutputProber) ProbeOutput(_ context.Context, _ string) (*cliprender.OutputProbe, error) {
	return &cliprender.OutputProbe{
		HasVideo:    true,
		VideoCodec:  p.videoCodec,
		PixelFormat: p.pixelFormat,
		Width:       p.width,
		Height:      p.height,
		FPS:         p.fps,
		HasAudio:    true,
		AudioCodec:  p.audioCodec,
		SampleRate:  p.sampleRate,
		Channels:    p.channels,
	}, nil
}

func defaultMockProber() *mockOutputProber {
	return &mockOutputProber{
		videoCodec:  "h264",
		pixelFormat: "yuv420p",
		width:       1920,
		height:      1080,
		fps:         30,
		audioCodec:  "aac",
		sampleRate:  48000,
		channels:    2,
	}
}

// ── fake variant repo + publisher ─────────────────────────────────────────

type fakeVariantRepo struct {
	mu   sync.Mutex
	rows map[string]*detail.RenderVariant
}

func newFakeVariantRepo() *fakeVariantRepo {
	return &fakeVariantRepo{rows: map[string]*detail.RenderVariant{}}
}

func (f *fakeVariantRepo) key(sourceClipID, language, fingerprint string) string {
	return sourceClipID + "\x00" + language + "\x00" + fingerprint
}

func (f *fakeVariantRepo) Upsert(_ context.Context, v *detail.RenderVariant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[f.key(v.SourceClipID, v.LanguageCode, v.Fingerprint)] = v
	return nil
}

func (f *fakeVariantRepo) FindCurrent(_ context.Context, sourceClipID, languageCode string) (*detail.RenderVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.rows {
		if v.SourceClipID == sourceClipID && v.LanguageCode == languageCode && v.IsCurrent {
			return v, nil
		}
	}
	return nil, nil
}

func (f *fakeVariantRepo) FindByFingerprint(_ context.Context, sourceClipID, languageCode, fingerprint string) (*detail.RenderVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.rows[f.key(sourceClipID, languageCode, fingerprint)]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeVariantRepo) ListBySourceClip(_ context.Context, sourceClipID string) ([]detail.RenderVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []detail.RenderVariant
	for _, v := range f.rows {
		if v.SourceClipID == sourceClipID {
			out = append(out, *v)
		}
	}
	return out, nil
}

type fakePublisher struct {
	mu    sync.Mutex
	calls int
}

func (f *fakePublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return &delivery.PublishResult{
		FileID:      "file-" + strings.TrimSuffix(req.Filename, filepath.Ext(req.Filename)),
		WebViewLink: "https://drive.example.com/" + req.Filename,
	}, nil
}

func (f *fakePublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "folder-1", nil
}

// ── test helpers ──────────────────────────────────────────────────────────

func validASSForTest(lang string) string {
	return "[Script Info]\nScriptType: v4.00+\nPlayResX: 1920\nPlayResY: 1080\n\n" +
		"[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n" +
		"Style: vidrush-default,Arial,56,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,3,2,2,10,10,24,1\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:00.00,0:00:01.00,vidrush-default,,0,0,0,,hello " + lang + "\n"
}

func variantForTest(clipID, lang, src string, durationSec float64, dir string) VariantInput {
	return VariantInput{
		SourceClipID:         clipID,
		SourcePath:           src,
		SourceSHA256:         "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		SourceDuration:       time.Duration(durationSec * float64(time.Second)),
		SourceFPS:            30,
		Language:             lang,
		TranscriptSHA256:     "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a",
		TranslationVersion:   "model-v1",
		SubtitleStyleVersion: "vidrush-default:vidrush-ass-v2",
		ASSPath:              filepath.Join(dir, lang+".ass"),
		ASSHash:              "",
		OutputFilename:       "clip." + lang + ".mp4",
		DriveFolderID:        "folder-1",
		WorkDir:              filepath.Join(dir, "renders"),
	}
}

func countReused(report *RenderReport) int {
	n := 0
	for _, v := range report.Variants {
		if v.Status == "reused" {
			n++
		}
	}
	return n
}

func runRenderAll(t *testing.T, r *Renderer, ctx context.Context, inputs []VariantInput, conc int) *RenderReport {
	t.Helper()
	report, err := r.RenderAll(ctx, inputs, conc)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	return report
}

// ── tests ─────────────────────────────────────────────────────────────────

func TestRenderVariantFingerprint_DeterministicAndSensitive(t *testing.T) {
	base := []string{
		"source_sha", "transcript_sha", "it", "model-v1", "style-v1", detail.RenderProfileFFmpegAss1080pV1,
	}
	fp := detail.RenderVariantFingerprint(base[0], base[1], base[2], base[3], base[4], base[5])
	if fp == "" {
		t.Fatal("fingerprint must be non-empty")
	}
	if fp2 := detail.RenderVariantFingerprint(base[0], base[1], base[2], base[3], base[4], base[5]); fp2 != fp {
		t.Fatalf("fingerprint must be deterministic: %q != %q", fp, fp2)
	}

	cases := []struct {
		name  string
		index int
		value string
	}{
		{"source_clip_sha256", 0, "other_source_sha"},
		{"transcript_sha256", 1, "other_transcript_sha"},
		{"target_language", 2, "es"},
		{"translation_version", 3, "model-v2"},
		{"subtitle_style_version", 4, "style-v2"},
	}
	for _, c := range cases {
		args := append([]string{}, base...)
		args[c.index] = c.value
		if got := detail.RenderVariantFingerprint(args[0], args[1], args[2], args[3], args[4], args[5]); got == fp {
			t.Errorf("%s: fingerprint must change when the input changes", c.name)
		}
	}
}

// TestRenderer_ColdThenWarmCache certifies the idempotency contract via
// real mock Rust renders. COLD run: empty variant repo → every language
// renders once; WARM run: same inputs → all reused, zero renderer calls.
func TestRenderer_ColdThenWarmCache(t *testing.T) {
	dir := t.TempDir()

	mockRust := newMockRustRenderer()
	repo := newFakeVariantRepo()
	pub := &fakePublisher{}

	r, err := NewRenderer(repo, pub, "", zap.NewNop())
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	r.WithRustRenderer(mockRust, 1920, 1080, 30, 1)
	r.WithOutputProber(defaultMockProber())

	src := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	const durationSec = 5.0
	inputs := []VariantInput{
		variantForTest("clip-1", "en", src, durationSec, dir),
		variantForTest("clip-1", "it", src, durationSec, dir),
		variantForTest("clip-1", "es", src, durationSec, dir),
	}
	for i := range inputs {
		in := &inputs[i]
		if err := os.WriteFile(in.ASSPath, []byte(validASSForTest(in.Language)), 0o644); err != nil {
			t.Fatalf("write ASS %s: %v", in.ASSPath, err)
		}
		in.ASSHash, _, err = sha256File(in.ASSPath)
		if err != nil {
			t.Fatalf("hash ASS %s: %v", in.ASSPath, err)
		}
	}
	ctx := context.Background()

	// ── COLD run: nothing cached → 3 renders ──────────────────────────
	cold := runRenderAll(t, r, ctx, inputs, 2)
	coldHits := countReused(cold)
	coldCalls := mockRust.calls
	if coldHits != 0 {
		t.Fatalf("cold run: expected 0 cache hits, got %d", coldHits)
	}
	for i, v := range cold.Variants {
		if v.Status != "ready" {
			t.Fatalf("cold run: variant %d (%s) status %q, want ready (err=%s)", i, v.Language, v.Status, v.Error)
		}
	}
	if coldCalls != len(inputs) {
		t.Fatalf("cold run: renderer executed %d times, want %d (one per language)", coldCalls, len(inputs))
	}

	// ── WARM run: same fingerprints → 0 new renders, all reused ────────
	warm := runRenderAll(t, r, ctx, inputs, 2)
	warmHits := countReused(warm)
	warmCalls := mockRust.calls - coldCalls
	if warmHits != len(inputs) {
		t.Fatalf("warm run: expected %d cache hits, got %d", len(inputs), warmHits)
	}
	if warmCalls != 0 {
		t.Fatalf("warm run: renderer executed %d times, want 0 new calls", warmCalls)
	}
	for i, v := range warm.Variants {
		if v.Status != "reused" {
			t.Fatalf("warm run: variant %d (%s) status %q, want reused", i, v.Language, v.Status)
		}
	}

	coldWork := cold.Concurrency.TotalWorkMS
	warmWork := warm.Concurrency.TotalWorkMS
	if warmWork > coldWork {
		t.Fatalf("warm run did MORE work than cold: warm %dms vs cold %dms", warmWork, coldWork)
	}
	if pub.calls != len(inputs) {
		t.Fatalf("publisher called %d times, want %d (cold run only; warm run must not re-upload)", pub.calls, len(inputs))
	}

	t.Logf("cold: renderer=%d cache_hits=%d work=%dms", coldCalls, coldHits, coldWork)
	t.Logf("warm: renderer_new=%d cache_hits=%d work=%dms", warmCalls, warmHits, warmWork)
}

// TestRenderPool_StreamingOrderAndTimestamps certifies streaming render pool
// semantics: priority order preserved, per-variant timestamps populated.
func TestRenderPool_StreamingOrderAndTimestamps(t *testing.T) {
	dir := t.TempDir()

	mockRust := newMockRustRenderer()
	repo := newFakeVariantRepo()
	pub := &fakePublisher{}

	r, err := NewRenderer(repo, pub, "", zap.NewNop())
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	r.WithRustRenderer(mockRust, 1920, 1080, 30, 1)
	r.WithOutputProber(defaultMockProber())

	src := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(src, []byte("src"), 0o644); err != nil {
		t.Fatal(err)
	}

	enReady := time.Now().Add(-2 * time.Second)
	esReady := time.Now().Add(-1 * time.Second)

	en := variantForTest("clip-1", "en", src, 5.0, dir)
	en.Priority = 0
	en.TextReadyAt = enReady
	es := variantForTest("clip-1", "es", src, 5.0, dir)
	es.Priority = 1
	es.TextReadyAt = esReady
	for _, in := range []VariantInput{en, es} {
		if err := os.WriteFile(in.ASSPath, []byte(validASSForTest(in.Language)), 0o644); err != nil {
			t.Fatalf("write ASS %s: %v", in.ASSPath, err)
		}
	}

	pool := r.NewRenderPool(context.Background(), 2)
	pool.Submit(en)
	pool.Submit(es)
	report := pool.Wait()

	if len(report.Variants) != 2 {
		t.Fatalf("expected 2 variants, got %d", len(report.Variants))
	}
	if report.Variants[0].Language != "en" || report.Variants[1].Language != "es" {
		t.Fatalf("priority order not preserved: %+v", report.Variants)
	}
	for i, v := range report.Variants {
		if v.Priority != i {
			t.Errorf("variant %d priority %d, want %d", i, v.Priority, i)
		}
		if v.WorkerID != i {
			t.Errorf("variant %d worker_id %d, want %d", i, v.WorkerID, i)
		}
		if v.TextReadyAt.IsZero() || v.QueuedAt.IsZero() || v.RenderStartedAt.IsZero() || v.RenderCompletedAt.IsZero() {
			t.Errorf("variant %d has zero timestamps: %+v", i, v)
		}
		if v.RenderStartedAt.Before(v.QueuedAt) {
			t.Errorf("variant %d render_started_at before queued_at", i)
		}
		if v.RenderCompletedAt.Before(v.RenderStartedAt) {
			t.Errorf("variant %d render_completed_at before render_started_at", i)
		}
		if v.Status == "ready" && v.UploadCompletedAt.IsZero() {
			t.Errorf("variant %d ready but upload_completed_at is zero", i)
		}
	}
	if report.Variants[0].TextReadyAt != enReady {
		t.Errorf("EN text_ready_at not propagated: got %v want %v", report.Variants[0].TextReadyAt, enReady)
	}
}

// TestFailsWithoutRust verifies the multilingual renderer fails closed when
// RustRenderer is not wired.
func TestFailsWithoutRust(t *testing.T) {
	repo := newFakeVariantRepo()
	r, err := NewRenderer(repo, &fakePublisher{}, "", zap.NewNop())
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	res := r.RenderOne(context.Background(), VariantInput{
		SourceClipID:     "clip-1",
		SourceSHA256:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		TranscriptSHA256: "a7ffc6f8bf1ed76651c14756a061d662f580ff4de43b49fa82d80a4b80f8434a",
	})
	if res.Status != "failed" {
		t.Fatalf("expected failed without Rust, got %q", res.Status)
	}
}

func TestParseFPS(t *testing.T) {
	cases := []struct {
		in   string
		want float64
	}{
		{"30000/1001", 29.97002997},
		{"25/1", 25},
		{"30", 30},
		{"0/0", 0},
		{"", 0},
		{"garbage", 0},
	}
	for _, c := range cases {
		got := ParseFPS(c.in)
		if c.want == 0 {
			if got != 0 {
				t.Errorf("ParseFPS(%q) = %v, want 0", c.in, got)
			}
			continue
		}
		if got < c.want-0.01 || got > c.want+0.01 {
			t.Errorf("ParseFPS(%q) = %v, want ~%v", c.in, got, c.want)
		}
	}
}

func TestNoLanguageContamination(t *testing.T) {
	dir := t.TempDir()
	r := &Renderer{}

	assPath := filepath.Join(dir, "subs.ass")
	content := validASSForTest("it")
	if err := os.WriteFile(assPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	h, _, err := sha256File(assPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := r.noLanguageContamination(VariantInput{ASSPath: assPath}); err != nil {
		t.Fatalf("empty hash should pass: %v", err)
	}
	if err := r.noLanguageContamination(VariantInput{ASSPath: assPath, ASSHash: h}); err != nil {
		t.Fatalf("correct hash should pass: %v", err)
	}
	if err := r.noLanguageContamination(VariantInput{ASSPath: assPath, ASSHash: "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef"}); err == nil {
		t.Fatal("wrong hash should fail")
	}
}

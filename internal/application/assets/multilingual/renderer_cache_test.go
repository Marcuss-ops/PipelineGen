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

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
)

// TestRenderer_ColdThenWarmCache certifies the render fan-out's idempotency
// contract with real (fake-binary) render executions:
//
//   - COLD run: empty variant repo → every language burns via ffmpeg exactly
//     once (cache misses), producing "ready" variants persisted by fingerprint.
//   - WARM run: same inputs → every language is a fingerprint HIT ("reused"),
//     ffmpeg executions drop to ZERO, and the avoided render work is the whole
//     cold-run render cost.
//
// The fake ffmpeg appends a byte to a counter file per execution, so the
// assertion "ffmpeg executions drop to 0 in the warm run" is a deterministic
// filesystem observation, not a timing estimate.
func TestRenderer_ColdThenWarmCache(t *testing.T) {
	dir := t.TempDir()
	counterPath := filepath.Join(dir, "ffmpeg.counter")
	_ = os.WriteFile(counterPath, nil, 0o644)

	const durationSec = 5.0
	ffmpegPath, _ := writeFakeFfmpeg(t, dir, counterPath, durationSec)

	repo := newFakeVariantRepo()
	pub := &fakePublisher{}
	r, err := NewRenderer(repo, pub, ffmpegPath, zap.NewNop())
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	src := filepath.Join(dir, "source.mp4")
	if err := os.WriteFile(src, []byte("source"), 0o644); err != nil {
		t.Fatal(err)
	}

	inputs := []VariantInput{
		variantForTest("clip-1", "en", src, durationSec, dir),
		variantForTest("clip-1", "it", src, durationSec, dir),
		variantForTest("clip-1", "es", src, durationSec, dir),
	}
	// The renderer's validation requires a non-empty .ass (burn-in present), so
	// write a minimal valid ASS for each language before running.
	for _, in := range inputs {
		if err := os.WriteFile(in.ASSPath, []byte(validASSForTest(in.Language)), 0o644); err != nil {
			t.Fatalf("write ASS %s: %v", in.ASSPath, err)
		}
	}
	ctx := context.Background()

	// ── COLD run: nothing cached → 3 ffmpeg burns ──────────────────────────
	cold := runRenderAll(t, r, ctx, inputs, 2)
	coldHits := countReused(cold)
	coldExec := ffmpegExecCount(t, counterPath)
	if coldHits != 0 {
		t.Fatalf("cold run: expected 0 cache hits, got %d", coldHits)
	}
	for i, v := range cold.Variants {
		if v.Status != "ready" {
			t.Fatalf("cold run: variant %d (%s) status %q, want ready (err=%s)", i, v.Language, v.Status, v.Error)
		}
	}
	if coldExec != len(inputs) {
		t.Fatalf("cold run: ffmpeg executed %d times, want %d (one per language)", coldExec, len(inputs))
	}

	// ── WARM run: same fingerprints → 0 ffmpeg burns, all reused ───────────
	warm := runRenderAll(t, r, ctx, inputs, 2)
	warmHits := countReused(warm)
	warmExec := ffmpegExecCount(t, counterPath)
	if warmHits != len(inputs) {
		t.Fatalf("warm run: expected %d cache hits, got %d", len(inputs), warmHits)
	}
	if warmExec != coldExec {
		t.Fatalf("warm run: ffmpeg executed %d times (new executions=%d), want 0 new (reuse must skip ffmpeg)",
			warmExec, warmExec-coldExec)
	}
	for i, v := range warm.Variants {
		if v.Status != "reused" {
			t.Fatalf("warm run: variant %d (%s) status %q, want reused", i, v.Language, v.Status)
		}
	}

	// avoided_work_ms = render work the warm run skipped thanks to the cache.
	coldWork := cold.Concurrency.TotalWorkMS
	warmWork := warm.Concurrency.TotalWorkMS
	avoidedWorkMS := coldWork - warmWork
	if avoidedWorkMS < 0 {
		t.Fatalf("avoided_work_ms must be >= 0, got %d (cold work %d, warm work %d)", avoidedWorkMS, coldWork, warmWork)
	}
	if warmWork > coldWork {
		t.Fatalf("warm run did MORE work than cold: warm %dms vs cold %dms", warmWork, coldWork)
	}
	if pub.calls != len(inputs) {
		t.Fatalf("publisher called %d times, want %d (cold run only; warm run must not re-upload)", pub.calls, len(inputs))
	}

	t.Logf("cold: ffmpeg=%d cache_hits=%d work=%dms", coldExec, coldHits, coldWork)
	t.Logf("warm: ffmpeg_new=%d cache_hits=%d work=%dms avoided_work_ms=%d",
		warmExec-coldExec, warmHits, warmWork, avoidedWorkMS)
}

func runRenderAll(t *testing.T, r *Renderer, ctx context.Context, inputs []VariantInput, conc int) *RenderReport {
	t.Helper()
	report, err := r.RenderAll(ctx, inputs, conc)
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	return report
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

func ffmpegExecCount(t *testing.T, counterPath string) int {
	t.Helper()
	b, err := os.ReadFile(counterPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read ffmpeg counter: %v", err)
	}
	return len(b)
}

func variantForTest(clipID, lang, src string, durationSec float64, dir string) VariantInput {
	return VariantInput{
		SourceClipID:         clipID,
		SourcePath:           src,
		SourceSHA256:         "source-sha-256",
		SourceDuration:       time.Duration(durationSec * float64(time.Second)),
		SourceFPS:            30000.0 / 1001.0, // must match the fake ffprobe
		Language:             lang,
		TranscriptSHA256:     "transcript-sha-256",
		TranslationVersion:   "model-v1",
		SubtitleStyleVersion: "vidrush-default:vidrush-ass-v2",
		ASSPath:              filepath.Join(dir, lang+".ass"),
		ASSHash:              "ass-hash",
		OutputFilename:       "clip." + lang + ".mp4",
		DriveFolderID:        "folder-1",
		WorkDir:              filepath.Join(dir, "renders"),
	}
}

// validASSForTest returns a minimal ASS with a single dialogue line, so the
// burn-in presence check passes.
func validASSForTest(lang string) string {
	return "[Script Info]\nScriptType: v4.00+\nPlayResX: 1920\nPlayResY: 1080\n\n" +
		"[V4+ Styles]\nFormat: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding\n" +
		"Style: vidrush-default,Arial,56,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,3,2,2,10,10,24,1\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:00.00,0:00:01.00,vidrush-default,,0,0,0,,hello " + lang + "\n"
}

// writeFakeFfmpeg installs a fake ffmpeg + ffprobe pair in dir. The fake
// ffmpeg writes a non-empty output file (so the renderer's size validation
// passes) and appends one byte to counterPath per execution. The fake ffprobe
// returns a video + audio stream with the given duration so the renderer's
// contract validation passes. Returns the ffmpeg path (ffprobe is derived
// alongside it by the renderer).
func writeFakeFfmpeg(t *testing.T, dir, counterPath string, durationSec float64) (string, string) {
	t.Helper()
	ffmpegPath := filepath.Join(dir, "ffmpeg")
	ffprobePath := filepath.Join(dir, "ffprobe")

	// counterPath from t.TempDir() is a Linux path with no quotes/spaces, so
	// single-quoting is safe here.
	ffmpegScript := fmt.Sprintf(`#!/bin/sh
out=""
for a in "$@"; do out="$a"; done
printf 'fake-video-bytes\n' > "$out"
printf x >> '%s'
`, counterPath)

	ffprobeScript := fmt.Sprintf(`#!/bin/sh
printf '{"streams":[{"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"avg_frame_rate":"30000/1001"},{"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"%f"}}'
`, durationSec)

	if err := os.WriteFile(ffmpegPath, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	if err := os.WriteFile(ffprobePath, []byte(ffprobeScript), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}
	return ffmpegPath, ffprobePath
}

// fakeVariantRepo is an in-memory RenderVariantRepository keyed by
// (source_clip_id, language_code, fingerprint), mirroring the real port.
type fakeVariantRepo struct {
	mu   sync.Mutex
	rows map[string]*asset.RenderVariant
}

func newFakeVariantRepo() *fakeVariantRepo {
	return &fakeVariantRepo{rows: map[string]*asset.RenderVariant{}}
}

func (f *fakeVariantRepo) key(sourceClipID, language, fingerprint string) string {
	return sourceClipID + "\x00" + language + "\x00" + fingerprint
}

func (f *fakeVariantRepo) Upsert(_ context.Context, v *asset.RenderVariant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[f.key(v.SourceClipID, v.LanguageCode, v.Fingerprint)] = v
	return nil
}

func (f *fakeVariantRepo) FindCurrent(_ context.Context, sourceClipID, languageCode string) (*asset.RenderVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.rows {
		if v.SourceClipID == sourceClipID && v.LanguageCode == languageCode && v.IsCurrent {
			return v, nil
		}
	}
	return nil, nil
}

func (f *fakeVariantRepo) FindByFingerprint(_ context.Context, sourceClipID, languageCode, fingerprint string) (*asset.RenderVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.rows[f.key(sourceClipID, languageCode, fingerprint)]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeVariantRepo) ListBySourceClip(_ context.Context, sourceClipID string) ([]asset.RenderVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []asset.RenderVariant
	for _, v := range f.rows {
		if v.SourceClipID == sourceClipID {
			out = append(out, *v)
		}
	}
	return out, nil
}

// fakePublisher records the number of Publish calls so the test can assert the
// warm run never re-uploads (upload happens only on a cache miss).
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

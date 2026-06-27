// Package scripts_test — processor_images_voiceover_test.go exercises
// the ImageProcessor and VoiceoverProcessor (PR 8).
package scripts_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"

	"go.uber.org/zap"
)

// ── Image processor fakes ─────────────────────────────────────────

type fakeImageGen struct {
	results []*asset.ImageAsset
	errs    []error
	calls   atomic.Int32
}

func (f *fakeImageGen) SearchAndDownload(_ context.Context, _, _, _, _ string, _ interface{}) (*asset.ImageAsset, error) {
	i := int(f.calls.Add(1) - 1)
	if i >= len(f.results) {
		return nil, errors.New("unexpected call index")
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.results[i], nil
}

func (f *fakeImageGen) GenerateSmartImage(_ context.Context, _, _, _ string, _, _ []string, _, _ int, _ string, _ bool) (*asset.ImageAsset, error) {
	return nil, errors.New("not implemented")
}

// ── Voiceover processor fakes ─────────────────────────────────────

type fakeVoiceoverGen struct {
	results []map[string]any
	errs    []error
	calls   atomic.Int32
}

func (f *fakeVoiceoverGen) Generate(_ context.Context, _, _, _ string) (interface{}, error) {
	i := int(f.calls.Add(1) - 1)
	if i >= len(f.results) {
		return nil, errors.New("unexpected call index")
	}
	if i < len(f.errs) && f.errs[i] != nil {
		return nil, f.errs[i]
	}
	return f.results[i], nil
}

// ── Helpers ───────────────────────────────────────────────────────

func makePlanWithClips(count int) *scriptpkg.ResolvedGenerationPlan {
	ids := make([]string, count)
	links := make(map[string]string, count)
	for i := 0; i < count; i++ {
		id := string(rune('a' + i))
		ids[i] = id
		links[id] = "https://drive.example.com/" + id
	}
	return &scriptpkg.ResolvedGenerationPlan{
		ID:    "item-1",
		Title: "Test Plan",
		ClipEvidence: &scriptpkg.ClipEvidence{
			ClipIDs:    ids,
			ClipCount:  count,
			DriveLinks: links,
		},
	}
}

func imgAsset(url string) *asset.ImageAsset {
	return &asset.ImageAsset{SourceURL: url}
}

func voResult(link, path string) map[string]any {
	return map[string]any{"drive_link": link, "path": path}
}

// ── Test: ImageProcessor ──────────────────────────────────────────

func TestImageProcessorNilGen(t *testing.T) {
	t.Parallel()
	proc := scripts.NewImageProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), makePlanWithClips(2), "some script")
	if err == nil {
		t.Fatal("expected error when ImageGenService is nil")
	}
}

func TestImageProcessorNoClipEvidence(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*asset.ImageAsset{imgAsset("http://img1")}}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "text-only"}
	result, err := proc.Process(context.Background(), plan, "some script")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SceneImages) != 0 {
		t.Errorf("expected 0 images, got %d", len(result.SceneImages))
	}
}

func TestImageProcessorEmptyScript(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{results: []*asset.ImageAsset{imgAsset("http://img1")}}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), makePlanWithClips(2), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SceneImages) != 0 {
		t.Errorf("expected 0 images for empty script, got %d", len(result.SceneImages))
	}
}

func TestImageProcessorSuccess(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{
		results: []*asset.ImageAsset{imgAsset("http://img1.jpg")},
	}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), makePlanWithClips(1), "Scene one text.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.SceneImages) != 1 {
		t.Fatalf("expected 1 image, got %d", len(result.SceneImages))
	}
	img := result.SceneImages[0]
	if img.Index != 0 {
		t.Errorf("image index: %d, want 0", img.Index)
	}
	if img.URL != "http://img1.jpg" {
		t.Errorf("image URL: %q", img.URL)
	}
	if img.Text == "" {
		t.Error("image text should not be empty")
	}
}

func TestImageProcessorPartialFailure(t *testing.T) {
	t.Parallel()
	gen := &fakeImageGen{
		results: []*asset.ImageAsset{
			imgAsset("http://img1.jpg"),
			nil,
		},
		errs: []error{nil, errors.New("timeout")},
	}
	proc := scripts.NewImageProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), makePlanWithClips(2), "Scene1.\n\nScene2.")
	if err != nil {
		t.Fatalf("unexpected error (partial failures should not abort): %v", err)
	}
	if len(result.SceneImages) != 2 {
		t.Fatalf("expected 2 images (one placeholder), got %d", len(result.SceneImages))
	}
	if result.SceneImages[0].URL != "http://img1.jpg" {
		t.Errorf("scene 0 URL: %q", result.SceneImages[0].URL)
	}
	if result.SceneImages[1].URL != "" {
		t.Errorf("scene 1 URL should be empty (failed), got %q", result.SceneImages[1].URL)
	}
	if result.SceneImages[0].Index != 0 {
		t.Errorf("scene 0 index: %d", result.SceneImages[0].Index)
	}
	if result.SceneImages[1].Index != 1 {
		t.Errorf("scene 1 index preserved: %d", result.SceneImages[1].Index)
	}
}

// ── Test: VoiceoverProcessor ──────────────────────────────────────

func TestVoiceoverProcessorNilGen(t *testing.T) {
	t.Parallel()
	proc := scripts.NewVoiceoverProcessor(nil, zap.NewNop())
	_, err := proc.Process(context.Background(), makePlanWithClips(2), "some script")
	if err == nil {
		t.Fatal("expected error when VoiceoverService is nil")
	}
}

func TestVoiceoverProcessorNoClipEvidence(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{results: []map[string]any{voResult("http://vo1", "/tmp/vo1.mp3")}}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	plan := &scriptpkg.ResolvedGenerationPlan{ID: "text-only"}
	result, err := proc.Process(context.Background(), plan, "some script")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Voiceovers) != 0 {
		t.Errorf("expected 0 voiceovers, got %d", len(result.Voiceovers))
	}
}

func TestVoiceoverProcessorEmptyScript(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{results: []map[string]any{voResult("http://vo1", "/tmp/vo1.mp3")}}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), makePlanWithClips(2), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Voiceovers) != 0 {
		t.Errorf("expected 0 voiceovers for empty script, got %d", len(result.Voiceovers))
	}
}

func TestVoiceoverProcessorSuccess(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{
		results: []map[string]any{voResult("http://vo.mp3", "/tmp/scene-1.mp3")},
	}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), makePlanWithClips(1), "Scene one text.")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Voiceovers) != 1 {
		t.Fatalf("expected 1 voiceover, got %d", len(result.Voiceovers))
	}
	vo := result.Voiceovers[0]
	if vo.SceneIndex != 0 {
		t.Errorf("voiceover index: %d, want 0", vo.SceneIndex)
	}
	if vo.Status != "completed" {
		t.Errorf("voiceover status: %q, want \"completed\"", vo.Status)
	}
	if vo.Link != "http://vo.mp3" {
		t.Errorf("voiceover link: %q", vo.Link)
	}
	if vo.LocalPath != "/tmp/scene-1.mp3" {
		t.Errorf("voiceover localPath: %q", vo.LocalPath)
	}
}

func TestVoiceoverProcessorPartialFailure(t *testing.T) {
	t.Parallel()
	gen := &fakeVoiceoverGen{
		results: []map[string]any{
			voResult("http://vo1.mp3", "/tmp/s1.mp3"),
			nil,
		},
		errs: []error{nil, errors.New("synthesis timeout")},
	}
	proc := scripts.NewVoiceoverProcessor(gen, zap.NewNop())
	result, err := proc.Process(context.Background(), makePlanWithClips(2), "Scene1.\n\nScene2.")
	if err != nil {
		t.Fatalf("unexpected error (partial failures should not abort): %v", err)
	}
	if len(result.Voiceovers) != 2 {
		t.Fatalf("expected 2 voiceovers (one placeholder), got %d", len(result.Voiceovers))
	}
	if result.Voiceovers[0].Status != "completed" {
		t.Errorf("scene 0 status: %q", result.Voiceovers[0].Status)
	}
	if result.Voiceovers[1].Status != "failed" {
		t.Errorf("scene 1 status: %q, want \"failed\"", result.Voiceovers[1].Status)
	}
	if result.Voiceovers[0].SceneIndex != 0 {
		t.Errorf("scene 0 index: %d", result.Voiceovers[0].SceneIndex)
	}
	if result.Voiceovers[1].SceneIndex != 1 {
		t.Errorf("scene 1 index preserved: %d", result.Voiceovers[1].SceneIndex)
	}
}

// ── Test: processor names ─────────────────────────────────────────

func TestImageProcessorName(t *testing.T) {
	proc := scripts.NewImageProcessor(&fakeImageGen{}, zap.NewNop())
	if proc.Name() != "images" {
		t.Errorf("expected name \"images\", got %q", proc.Name())
	}
}

func TestVoiceoverProcessorName(t *testing.T) {
	proc := scripts.NewVoiceoverProcessor(&fakeVoiceoverGen{}, zap.NewNop())
	if proc.Name() != "voiceover" {
		t.Errorf("expected name \"voiceover\", got %q", proc.Name())
	}
}

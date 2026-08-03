package shorts

import (
	"errors"
	"testing"

	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

func TestBuildShortsPlan(t *testing.T) {
	got, err := Build(Request{
		ID: "short-1", Text: "one two three four five six", DurationMs: 6000,
		Clips:               []Clip{{ID: "clip-a"}},
		IncludeSoundEffects: true,
		SoundEffects:        []SoundEffect{{ID: "sfx-1", File: "/assets/sfx/hit.wav", AtMs: 1200}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != SchemaVersion || len(got.Captions) != 2 || len(got.SoundEffects) != 1 {
		t.Fatalf("unexpected shorts response: %+v", got)
	}
	if got.SoundEffects[0].Volume != 0.5 {
		t.Fatalf("default volume = %v", got.SoundEffects[0].Volume)
	}
}

func TestBuildRenderJobAssociatesEachClipSubtitleDriveURL(t *testing.T) {
	oldRepo := subtitleArtifacts
	SetSubtitleArtifactRepository(subtitleArtifactRepoFake{artifacts: map[string][]asset.SubtitleArtifact{
		"clip-a": {{DriveFileID: "drive-a", DriveURL: "https://drive.google.com/file/d/drive-a/view", LocalPath: "/tmp/a.ass", FileHash: "hash-a", Format: asset.SubtitleFormatASS, Status: asset.SubtitleStatusReady, IsCurrent: true}},
		"clip-b": {{DriveFileID: "drive-b", DriveURL: "https://drive.google.com/file/d/drive-b/view", LocalPath: "/tmp/b.ass", FileHash: "hash-b", Format: asset.SubtitleFormatASS, Status: asset.SubtitleStatusReady, IsCurrent: true}},
	}})
	defer SetSubtitleArtifactRepository(oldRepo)

	req := Request{ID: "short-subtitles", Text: "one", DurationMs: 1000,
		Clips: []Clip{{ID: "clip-a"}, {ID: "clip-b"}}}
	plan, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	job, err := BuildRenderJob(req, plan)
	if err != nil {
		t.Fatal(err)
	}
	clips := job.Props["clips"].([]map[string]any)
	if clips[0]["subtitlesURL"] != "https://drive.google.com/file/d/drive-a/view" || clips[1]["subtitlesURL"] != "https://drive.google.com/file/d/drive-b/view" {
		t.Fatalf("subtitle URLs were not kept per clip: %#v", clips)
	}
}

type subtitleArtifactRepoFake struct {
	artifacts map[string][]asset.SubtitleArtifact
}

func (f subtitleArtifactRepoFake) Upsert(context.Context, *asset.SubtitleArtifact) error { return nil }
func (f subtitleArtifactRepoFake) FindCurrent(context.Context, string, string, asset.SubtitleFormat) (*asset.SubtitleArtifact, error) {
	return nil, nil
}
func (f subtitleArtifactRepoFake) ListByAsset(_ context.Context, id string) ([]asset.SubtitleArtifact, error) {
	return f.artifacts[id], nil
}

func TestBuildShortsPlanCanDisableSoundEffects(t *testing.T) {
	got, err := Build(Request{ID: "short-1", Text: "one two", DurationMs: 1000, Clips: []Clip{{ID: "clip-a"}}, SoundEffects: []SoundEffect{{File: "hit.wav", AtMs: 100}}})
	if err != nil {
		t.Fatal(err)
	}
	if got.IncludeSoundEffects || len(got.SoundEffects) != 0 {
		t.Fatalf("sound effects should be disabled: %+v", got)
	}
}

func TestBuildShortsPlanRejectsInvalidSoundEffect(t *testing.T) {
	_, err := Build(Request{ID: "short-1", Text: "one", DurationMs: 1000, Clips: []Clip{{ID: "clip-a"}}, IncludeSoundEffects: true, SoundEffects: []SoundEffect{{File: "hit.wav", AtMs: 1000}}})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v", err)
	}
}

func TestBuildRenderJobMapsCaptionsAndSoundEffects(t *testing.T) {
	req := Request{
		ID: "short-render-1", Text: "one two three", DurationMs: 3000,
		Clips:               []Clip{{ID: "clip-a", Path: "data/clip.mp4", EndMs: 3000}},
		IncludeSoundEffects: true,
		SoundEffects:        []SoundEffect{{ID: "sfx-a", File: "data/hit.wav", AtMs: 1000, Volume: 0.4}},
	}
	plan, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	job, err := BuildRenderJob(req, plan)
	if err != nil {
		t.Fatal(err)
	}
	if job.Composition != "YouTubeShortComposition" || job.DurationInFrames != 90 || job.FPS != 30 || job.Width != 1080 || job.Height != 1920 {
		t.Fatalf("unexpected render job: %+v", job)
	}
	if job.Props["brollPath"] == "data/clip.mp4" {
		t.Fatalf("relative asset path was not resolved: %+v", job.Props["brollPath"])
	}
	if len(job.Props["captions"].([]map[string]any)) != 1 || len(job.Props["soundEffects"].([]map[string]any)) != 1 {
		t.Fatalf("caption/sfx props were not mapped: %+v", job.Props)
	}
}

func TestBuildRenderJobNormalizesDriveFolderURL(t *testing.T) {
	req := Request{ID: "short-drive", Text: "one", Language: "it-IT", DurationMs: 1000, Clips: []Clip{{ID: "clip-a"}}, UploadToDrive: true, DriveFolderID: "https://drive.google.com/drive/u/2/folders/folder-123?usp=sharing"}
	plan, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	job, err := BuildRenderJob(req, plan)
	if err != nil {
		t.Fatal(err)
	}
	if job.DriveFolderID != "folder-123" || job.Language != "it-IT" || job.DriveFilename != "short-drive.mp4" {
		t.Fatalf("unexpected Drive render fields: %+v", job)
	}
}

func TestBuildRenderJobRejectsLongformComposition(t *testing.T) {
	req := Request{
		ID: "longform-must-not-render", Text: "one", DurationMs: 1000,
		Composition: "LongformCompilationComposition",
		Clips:       []Clip{{ID: "clip-a"}},
	}
	plan, err := Build(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildRenderJob(req, plan); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid request for longform composition, got %v", err)
	}
}

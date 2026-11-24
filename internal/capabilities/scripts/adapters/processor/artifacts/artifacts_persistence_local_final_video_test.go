package artifacts

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	script "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	filesystem "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

func TestPersistGeneratedArtifactsAddsJoinedLocalFinalVideo(t *testing.T) {
	jobID := "local-final-video-persistence-test"
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID)) })
	source := filepath.Join(t.TempDir(), "final_video.mp4")
	payload := []byte("joined-overlay-final-video")
	if err := os.WriteFile(source, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	videoSHA, err := job.ComputeSHA256(filesystem.NewOS(), source)
	if err != nil {
		t.Fatal(err)
	}
	result := &script.GenerationResult{
		Title:    "Campioni e rivalita",
		Language: "it",
		LocalFinalVideo: &script.LocalFinalVideoArtifact{
			Path: source, SHA256: videoSHA, SizeBytes: int64(len(payload)),
			DurationUS: 77_520_000, Width: 1920, Height: 1080, FPS: 24,
			JoinedItems: 4, PlanItems: 4,
		},
	}

	artifacts, err := PersistGeneratedArtifacts(context.Background(), jobID, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	var final job.Artifact
	for _, artifact := range artifacts {
		if artifact.Kind == job.ArtifactKindFinalVideo {
			final = artifact
		}
	}
	if final.Kind != job.ArtifactKindFinalVideo || !final.Required {
		t.Fatalf("joined final video was not emitted as a required artifact: %+v", final)
	}
	if final.SHA256 != videoSHA || final.SizeBytes != int64(len(payload)) {
		t.Fatalf("joined final video identity was not preserved: %+v", final)
	}
	if got := final.ArtifactMetadata["source"]; got != "overlay_join" {
		t.Fatalf("source=%v, want overlay_join", got)
	}
	if got := final.ArtifactMetadata["joined_items"]; got != 4 {
		t.Fatalf("joined_items=%v, want 4", got)
	}
	if got := final.ArtifactMetadata["drive_publication"]; got != "deferred" {
		t.Fatalf("drive_publication=%v, want deferred", got)
	}

	scriptJSON, err := os.ReadFile(filepath.Join(workspaceOutputDir(jobID), "script.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(scriptJSON), source) {
		t.Fatalf("script.json leaked the local final video path: %s", scriptJSON)
	}
	if !strings.Contains(string(scriptJSON), `"joined_items": 4`) {
		t.Fatalf("script.json lost the join receipt: %s", scriptJSON)
	}
}

// A remote master owns delivery when both exist: the local join must not
// compete with it or the job would publish two different videos.
func TestPersistGeneratedArtifactsPrefersRemoteFinalVideoOverLocalJoin(t *testing.T) {
	jobID := "local-vs-remote-final-video-test"
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID)) })
	dir := t.TempDir()
	local := filepath.Join(dir, "local.mp4")
	remote := filepath.Join(dir, "remote.mp4")
	for path, body := range map[string]string{local: "local", remote: "remote"} {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result := &script.GenerationResult{
		Title:           "Both",
		Language:        "it",
		LocalFinalVideo: &script.LocalFinalVideoArtifact{Path: local, SizeBytes: 5},
		RemoteFinalVideo: &script.RemoteFinalVideoArtifact{
			JobID: "job_remote", Path: remote, SizeBytes: 6,
		},
	}
	artifacts, err := PersistGeneratedArtifacts(context.Background(), jobID, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	videos := 0
	for _, artifact := range artifacts {
		if artifact.Kind == job.ArtifactKindFinalVideo {
			videos++
			if artifact.Path != remote {
				t.Fatalf("local join displaced the remote master: %+v", artifact)
			}
		}
	}
	if videos != 1 {
		t.Fatalf("final video artifacts=%d, want exactly the remote master", videos)
	}
}

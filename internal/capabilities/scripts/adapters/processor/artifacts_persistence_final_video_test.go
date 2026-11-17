package processor

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

func TestPersistGeneratedArtifactsAddsRemoteFinalVideo(t *testing.T) {
	jobID := "final-video-persistence-test"
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Join(os.TempDir(), "pipelinegen", "jobs", jobID)) })
	source := filepath.Join(t.TempDir(), "master.mp4")
	payload := []byte("certified-final-video")
	if err := os.WriteFile(source, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	videoSHA, err := job.ComputeSHA256(filesystem.NewOS(), source)
	if err != nil {
		t.Fatal(err)
	}
	result := &script.GenerationResult{
		Title:    "Obama: venti minuti",
		Language: "it",
		RemoteFinalVideo: &script.RemoteFinalVideoArtifact{
			JobID:         "job_remote",
			Path:          source,
			SHA256:        videoSHA,
			SizeBytes:     int64(len(payload)),
			ArtifactURL:   "https://master.example/artifacts/final.mp4",
			DriveFolderID: "folder-1",
		},
	}

	artifacts, err := PersistGeneratedArtifacts(context.Background(), jobID, result, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts=%d, want script_json plus final_video", len(artifacts))
	}
	var final job.Artifact
	for _, artifact := range artifacts {
		if artifact.Kind == job.ArtifactKindFinalVideo {
			final = artifact
		}
	}
	if final.Kind != job.ArtifactKindFinalVideo || final.Required != true {
		t.Fatalf("final video artifact was not emitted as required: %+v", final)
	}
	if final.Path != source || final.SHA256 != videoSHA || final.SizeBytes != int64(len(payload)) {
		t.Fatalf("final video identity was not preserved: %+v", final)
	}
	if final.MIMEType != "video/mp4" || final.Filename != "Obama- venti minuti.mp4" {
		t.Fatalf("final video presentation is wrong: %+v", final)
	}
	if got := final.ArtifactMetadata["resolved_folder_id"]; got != "folder-1" {
		t.Fatalf("resolved_folder_id=%v, want folder-1", got)
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
	if !strings.Contains(string(scriptJSON), `"job_id": "job_remote"`) {
		t.Fatalf("script.json lost the remote final video receipt: %s", scriptJSON)
	}
}

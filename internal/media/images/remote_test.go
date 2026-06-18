package images

import (
	"testing"

	"github.com/Marcuss-ops/PipelineGen/pkg/googleaccounting"
)

func TestExtractRemoteImageNamesFromResultImages(t *testing.T) {
	job := &RemoteImageJob{
		Result: map[string]any{
			"images": []any{"generated-01.jpg", "generated-02.jpg", "generated-01.jpg"},
		},
	}

	got := extractRemoteImageNames(job)
	want := []string{"generated-01.jpg", "generated-02.jpg"}
	if len(got) != len(want) {
		t.Fatalf("expected %d images, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("image %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestExtractRemoteImageNamesFromArtifacts(t *testing.T) {
	job := &RemoteImageJob{
		Result: map[string]any{
			"artifacts": []any{
				map[string]any{
					"kind":  "screenshot",
					"name":  "page.png",
					"value": "ignored",
				},
				map[string]any{
					"kind":  "downloaded_images",
					"name":  "",
					"value": []any{"generated-03.jpg", "generated-04.jpg"},
				},
			},
		},
	}

	got := extractRemoteImageNames(job)
	want := []string{"generated-03.jpg", "generated-04.jpg", "page.png"}
	if len(got) != len(want) {
		t.Fatalf("expected %d images, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("image %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestJobStatusIsTerminal(t *testing.T) {
	// googleaccounting only exposes StatusSucceeded and StatusFailed as
	// terminal states (see pkg/googleaccounting/models.go JobStatus.IsTerminal).
	// Historical aliases StatusDone/StatusCompleted were removed; they map to
	// StatusSucceeded on ingest.
	terminal := []googleaccounting.JobStatus{
		googleaccounting.StatusSucceeded,
		googleaccounting.StatusFailed,
	}
	for _, status := range terminal {
		if !status.IsTerminal() {
			t.Fatalf("expected %q to be terminal", status)
		}
	}

	if googleaccounting.StatusQueued.IsTerminal() {
		t.Fatal("expected queued status to be non-terminal")
	}
}

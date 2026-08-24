package stockplan

import (
	"context"
	"testing"
)

func TestDefaultResolver_ProducesPlanWithClips(t *testing.T) {
	spec := BatchSpec{
		SourceURL: "https://example.com/video.mp4",
		Destination: DestinationSpec{
			DriveFolderID: "drive-123",
			FolderName:    "Test",
		},
		Sampling: SamplingPolicy{ClipDurationSec: 4, MaxGroupDurationSec: 60, MaxClipsPerGroup: 15},
		Groups: []GroupSpec{
			{Key: "round-1", Title: "Round 1", StartSec: 14, EndSec: 139},
			{Key: "decision", Title: "Decision", StartSec: 1803, EndSec: 1863},
		},
	}

	r := NewDefaultResolver()
	plan, err := r.Resolve(context.Background(), spec)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if plan.SourceURL != spec.SourceURL {
		t.Fatalf("SourceURL = %q, want %q", plan.SourceURL, spec.SourceURL)
	}
	if len(plan.Groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(plan.Groups))
	}

	for _, g := range plan.Groups {
		if len(g.Clips) == 0 {
			t.Fatalf("group %q has no clips", g.Key)
		}
		for i, c := range g.Clips {
			if c.URL != spec.SourceURL {
				t.Fatalf("group %s clip %d URL = %q, want %q", g.Key, i, c.URL, spec.SourceURL)
			}
			if c.Slug != g.Key || c.ParentSlug != g.Key {
				t.Fatalf("group %s clip %d has wrong slug/parent_slug: %+v", g.Key, i, c)
			}
			if c.StartSec >= c.EndSec {
				t.Fatalf("group %s clip %d non-positive duration", g.Key, i)
			}
		}
	}
}

func TestDefaultResolver_RequiresSourceURL(t *testing.T) {
	r := NewDefaultResolver()
	_, err := r.Resolve(context.Background(), BatchSpec{Groups: []GroupSpec{{Key: "g"}}})
	if err == nil {
		t.Fatal("expected error for empty source_url")
	}
}

func TestDefaultResolver_RequiresAtLeastOneGroup(t *testing.T) {
	r := NewDefaultResolver()
	_, err := r.Resolve(context.Background(), BatchSpec{SourceURL: "https://x"})
	if err == nil {
		t.Fatal("expected error for empty groups")
	}
}

func TestDefaultResolver_RequiresGroupKey(t *testing.T) {
	r := NewDefaultResolver()
	_, err := r.Resolve(context.Background(), BatchSpec{
		SourceURL: "https://x",
		Groups:    []GroupSpec{{StartSec: 0, EndSec: 10}},
	})
	if err == nil {
		t.Fatal("expected error for missing group key")
	}
}

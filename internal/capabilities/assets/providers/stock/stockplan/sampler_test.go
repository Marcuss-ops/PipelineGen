package stockplan

import (
	"testing"
)

func TestDeterministicSampler_SlicesGroupIntoClips(t *testing.T) {
	s := NewDeterministicSampler()
	group := GroupSpec{Key: "round-1", Title: "Round 1", StartSec: 0, EndSec: 60}
	policy := SamplingPolicy{ClipDurationSec: 4, MaxGroupDurationSec: 1000, MaxClipsPerGroup: 15}

	clips, err := s.Sample(group, policy)
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}

	if len(clips) == 0 {
		t.Fatal("expected at least one clip")
	}

	for i, c := range clips {
		if c.StartSec >= c.EndSec {
			t.Fatalf("clip %d has non-positive duration [%.3f, %.3f]", i, c.StartSec, c.EndSec)
		}
		if c.StartSec < group.StartSec || c.EndSec > group.EndSec {
			t.Fatalf("clip %d [%.3f, %.3f] outside group range", i, c.StartSec, c.EndSec)
		}
		if c.Slug != group.Key {
			t.Fatalf("clip %d slug = %q, want %q", i, c.Slug, group.Key)
		}
	}

	if clips[0].StartSec != group.StartSec {
		t.Fatalf("first clip starts at %.3f, want %.3f", clips[0].StartSec, group.StartSec)
	}

	last := clips[len(clips)-1]
	if last.EndSec != group.EndSec {
		t.Fatalf("last clip ends at %.3f, want %.3f", last.EndSec, group.EndSec)
	}
}

func TestDeterministicSampler_RespectsMaxClipsPerGroup(t *testing.T) {
	s := NewDeterministicSampler()
	group := GroupSpec{Key: "long", Title: "Long", StartSec: 0, EndSec: 120}
	policy := SamplingPolicy{ClipDurationSec: 4, MaxGroupDurationSec: 120, MaxClipsPerGroup: 15}

	clips, err := s.Sample(group, policy)
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if len(clips) != 15 {
		t.Fatalf("expected 15 clips, got %d", len(clips))
	}
}

func TestDeterministicSampler_RespectsMaxGroupDuration(t *testing.T) {
	s := NewDeterministicSampler()
	group := GroupSpec{Key: "capped", Title: "Capped", StartSec: 0, EndSec: 120}
	policy := SamplingPolicy{ClipDurationSec: 4, MaxGroupDurationSec: 30, MaxClipsPerGroup: 100}

	clips, err := s.Sample(group, policy)
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}

	if len(clips) != 7 {
		t.Fatalf("expected 7 clips (30s / 4s), got %d", len(clips))
	}

	last := clips[len(clips)-1]
	if last.EndSec > group.StartSec+float64(policy.MaxGroupDurationSec) {
		t.Fatalf("last clip end %.3f exceeds max duration %.3f", last.EndSec, group.StartSec+float64(policy.MaxGroupDurationSec))
	}
}

func TestDeterministicSampler_RejectsInvalidRange(t *testing.T) {
	s := NewDeterministicSampler()
	group := GroupSpec{Key: "bad", Title: "Bad", StartSec: 10, EndSec: 10}
	_, err := s.Sample(group, SamplingPolicy{ClipDurationSec: 4})
	if err == nil {
		t.Fatal("expected error for empty range")
	}
}

func TestDeterministicSampler_NormalizesZeroClipDuration(t *testing.T) {
	s := NewDeterministicSampler()
	group := GroupSpec{Key: "ok", Title: "OK", StartSec: 0, EndSec: 12}
	clips, err := s.Sample(group, SamplingPolicy{ClipDurationSec: 0})
	if err != nil {
		t.Fatalf("Sample() error = %v", err)
	}
	if len(clips) == 0 {
		t.Fatal("expected clips after normalization")
	}
	for _, c := range clips {
		if c.EndSec-c.StartSec != 4 {
			t.Fatalf("expected normalized 4s clips, got duration %.3f", c.EndSec-c.StartSec)
		}
	}
}

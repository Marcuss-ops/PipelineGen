package scriptgeneration

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// probeStub is a ClipPreflighter with a fixed result per asset id.
type probeStub struct {
	ok map[string]bool
}

type countingProbeStub struct {
	mu    sync.Mutex
	calls map[string]int
	err   error
}

func (p *countingProbeStub) ProbeClip(_ context.Context, clipID string) error {
	p.mu.Lock()
	p.calls[clipID]++
	p.mu.Unlock()
	return p.err
}

func (p *countingProbeStub) callCounts() map[string]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	calls := make(map[string]int, len(p.calls))
	for id, count := range p.calls {
		calls[id] = count
	}
	return calls
}

func TestRunMediaPreflightDeduplicatesClipExistenceProbes(t *testing.T) {
	path := t.TempDir() + "/fixed-audio.m4a"
	if err := os.WriteFile(path, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := &countingProbeStub{calls: make(map[string]int)}
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{
		ClipIDs: []string{"clip-a", "clip-a", "", "clip-b"},
		FixedClips: []FixedClipPreflight{
			{ClipID: "clip-a"},
			{ClipID: "clip-b"},
		},
		ClipProber:      probe,
		ClipAudioSource: fixedAudioPreflightStub{path: path, durationUS: 5_000_000},
	})
	if result.HasFailures() {
		t.Fatalf("preflight = %s, want success", result.Error())
	}
	want := map[string]int{"clip-a": 1, "clip-b": 1, "": 1}
	calls := probe.callCounts()
	if len(calls) != len(want) {
		t.Fatalf("probe calls = %#v, want %#v", calls, want)
	}
	for id, count := range want {
		if calls[id] != count {
			t.Errorf("probe calls for %q = %d, want %d", id, calls[id], count)
		}
	}
}

func TestRunMediaPreflightDeduplicatedClipFailureRemainsFailClosed(t *testing.T) {
	probe := &countingProbeStub{calls: make(map[string]int), err: errors.New("not reachable")}
	result := RunMediaPreflight(context.Background(), MediaPreflightInput{
		ClipIDs:    []string{"clip-a", "clip-a"},
		ClipProber: probe,
	})
	if !result.HasFailures() || !strings.Contains(result.Error(), "[clip] clip-a") {
		t.Fatalf("preflight = %s, want a fail-closed clip-a failure", result.Error())
	}
	if calls := probe.callCounts()["clip-a"]; calls != 1 {
		t.Fatalf("probe calls for duplicate failing clip = %d, want 1", calls)
	}
}

func (p probeStub) ProbeClip(_ context.Context, clipID string) error {
	if p.ok[clipID] {
		return nil
	}
	return errors.New("asset not reachable")
}

// TestRunMediaPreflight_BackgroundAssetFailFast verifies the render
// background layer (mode=asset) is a hard media requirement: a missing or
// unwired background asset fails the run exactly like the watermark.
func TestRunMediaPreflight_BackgroundAssetFailFast(t *testing.T) {
	t.Run("missing background asset fails", func(t *testing.T) {
		res := RunMediaPreflight(context.Background(), MediaPreflightInput{
			RenderEnabled:      true,
			BackgroundAssetID:  "bg-ghost",
			BackgroundResolver: probeStub{ok: map[string]bool{"bg-real": true}},
		})
		if !res.HasFailures() {
			t.Fatal("missing background asset must fail the preflight")
		}
		if !strings.Contains(res.Error(), "[background] bg-ghost") {
			t.Fatalf("failure must name the background asset: %s", res.Error())
		}
	})

	t.Run("available background asset passes", func(t *testing.T) {
		res := RunMediaPreflight(context.Background(), MediaPreflightInput{
			RenderEnabled:      true,
			BackgroundAssetID:  "bg-real",
			BackgroundResolver: probeStub{ok: map[string]bool{"bg-real": true}},
		})
		if res.HasFailures() {
			t.Fatalf("available background asset must pass: %s", res.Error())
		}
	})

	t.Run("unwired background resolver fails closed", func(t *testing.T) {
		res := RunMediaPreflight(context.Background(), MediaPreflightInput{
			RenderEnabled:     true,
			BackgroundAssetID: "bg-real",
		})
		if !res.HasFailures() || !strings.Contains(res.Error(), "resolver not wired") {
			t.Fatalf("unwired resolver must fail closed: %s", res.Error())
		}
	})

	t.Run("blur_source carries no asset requirement", func(t *testing.T) {
		// No BackgroundAssetID → no check runs, even with an unwired resolver.
		res := RunMediaPreflight(context.Background(), MediaPreflightInput{RenderEnabled: true})
		if res.HasFailures() {
			t.Fatalf("no asset check must not fail: %s", res.Error())
		}
	})
}

// TestRunMediaPreflight_WatermarkAssetFailFast mirrors the background check
// for the watermark layer, pinning the existing fail-fast contract.
func TestRunMediaPreflight_WatermarkAssetFailFast(t *testing.T) {
	res := RunMediaPreflight(context.Background(), MediaPreflightInput{
		RenderEnabled:     true,
		WatermarkAssetID:  "wm-ghost",
		WatermarkResolver: probeStub{ok: map[string]bool{"wm-real": true}},
	})
	if !res.HasFailures() {
		t.Fatal("missing watermark asset must fail the preflight")
	}
	if !strings.Contains(res.Error(), "[watermark] wm-ghost") {
		t.Fatalf("failure must name the watermark asset: %s", res.Error())
	}
}

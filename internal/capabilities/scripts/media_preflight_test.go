package scriptgeneration

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// probeStub is a ClipPreflighter with a fixed result per asset id.
type probeStub struct {
	ok map[string]bool
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

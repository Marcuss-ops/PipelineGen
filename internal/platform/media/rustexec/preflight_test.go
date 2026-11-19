package rustexec

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClientPreflightSendsTheTypedPayload(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(
		`{"ok":true,"operation":"preflight","preflight":{"ok":true,"issues":[]}}`,
	)}
	client := NewClient("muscles", "/usr/bin/ffmpeg", nil)
	client.runner = runner

	duration := int64(12500)
	decision, err := client.Preflight(context.Background(), PreflightRequest{
		VideoDurationMS: &duration,
		Assets:          []PreflightAsset{{AssetID: "clip-001", Path: "/fixtures/input.mp4"}},
		Subtitles:       []PreflightCue{{ItemID: "cue-001", InUS: 0, OutUS: 3_000_000, Text: "Titolo"}},
		Overlays:        []PreflightOverlay{{ItemID: "overlay-001", Track: 0, InUS: 1_000_000, OutUS: 4_000_000}},
	})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if decision.Blocked {
		t.Fatalf("clean report must not block: %+v", decision)
	}

	var sent request
	if err := json.Unmarshal(runner.input, &sent); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if sent.Operation != OperationPreflight || sent.FFmpegPath != "/usr/bin/ffmpeg" {
		t.Fatalf("unexpected envelope: %+v", sent)
	}
	if sent.Preflight == nil || sent.Preflight.VideoDurationMS == nil || *sent.Preflight.VideoDurationMS != 12500 {
		t.Fatalf("preflight payload missing or drifted: %+v", sent.Preflight)
	}
	if len(sent.Preflight.Assets) != 1 || sent.Preflight.Assets[0].AssetID != "clip-001" {
		t.Fatalf("asset payload drifted: %+v", sent.Preflight.Assets)
	}
	if len(sent.Preflight.Subtitles) != 1 || len(sent.Preflight.Overlays) != 1 {
		t.Fatalf("cue/overlay payload drifted: %+v", sent.Preflight)
	}
}

func TestClientPreflightDerivesTheBlockingDecisionFromErrorSeverity(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(
		`{"ok":true,"operation":"preflight","preflight":{"ok":false,"issues":[` +
			`{"severity":"error","code":"asset_missing","item_id":"ghost","path":"/missing.mp4","detail":"asset file does not exist"},` +
			`{"severity":"warning","code":"cue_reading_too_fast","item_id":"cue-001","detail":"too dense"}]}}`,
	)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner

	decision, err := client.Preflight(context.Background(), PreflightRequest{})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if !decision.Blocked {
		t.Fatal("an error-severity issue must block")
	}
	if len(decision.BlockingCodes) != 1 || decision.BlockingCodes[0] != "asset_missing" {
		t.Fatalf("blocking codes = %v", decision.BlockingCodes)
	}
	if len(decision.WarningCodes) != 1 || decision.WarningCodes[0] != "cue_reading_too_fast" {
		t.Fatalf("warning codes = %v", decision.WarningCodes)
	}
}

func TestClientPreflightWarningsDoNotBlock(t *testing.T) {
	runner := &fakeRunner{stdout: []byte(
		`{"ok":true,"operation":"preflight","preflight":{"ok":true,"issues":[` +
			`{"severity":"warning","code":"cue_reading_too_fast","item_id":"cue-001","detail":"too dense"}]}}`,
	)}
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = runner

	decision, err := client.Preflight(context.Background(), PreflightRequest{})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if decision.Blocked {
		t.Fatalf("warnings are advisory: %+v", decision)
	}
	if len(decision.WarningCodes) != 1 {
		t.Fatalf("warning codes = %v", decision.WarningCodes)
	}
}

func TestClientPreflightFailsClosedOnAMissingOrInconsistentReport(t *testing.T) {
	client := NewClient("muscles", "ffmpeg", nil)
	client.runner = &fakeRunner{stdout: []byte(`{"ok":true,"operation":"preflight"}`)}
	if _, err := client.Preflight(context.Background(), PreflightRequest{}); err == nil {
		t.Fatal("a response without a report must fail closed")
	}

	client.runner = &fakeRunner{stdout: []byte(
		`{"ok":true,"operation":"preflight","preflight":{"ok":false,"issues":[]}}`,
	)}
	decision, err := client.Preflight(context.Background(), PreflightRequest{})
	if err != nil {
		t.Fatalf("Preflight() error = %v", err)
	}
	if !decision.Blocked || decision.BlockingCodes[0] != "preflight_report_not_ok" {
		t.Fatalf("an ok=false report without an error must still block: %+v", decision)
	}
}

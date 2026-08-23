package jobs

import (
	"fmt"
	"strings"
	"testing"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

func TestBuildSingleFailureEnvelope_ClipNativePlanningError_SetsErrorCode(t *testing.T) {
	inner := &domainScript.ClipNativePlanningError{
		Code:   "CLIP_NATIVE_PLANNING_FAILED",
		ItemID: "item-1",
		Policy: "strict",
		Reason: "scene-clip count mismatch",
	}
	env := buildSingleFailureEnvelope("item-1", inner)
	if len(env.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(env.Items))
	}
	item := env.Items[0]
	if item.ErrorCode != "CLIP_NATIVE_PLANNING_FAILED" {
		t.Errorf("expected error_code CLIP_NATIVE_PLANNING_FAILED, got %q", item.ErrorCode)
	}
	if item.Error == "" {
		t.Error("expected non-empty error message")
	}
	if !strings.Contains(item.Error, "scene-clip count mismatch") {
		t.Errorf("expected error message to contain reason, got %q", item.Error)
	}
}

func TestBuildSingleFailureEnvelope_WrappedClipNativePlanningError_SetsErrorCode(t *testing.T) {
	inner := &domainScript.ClipNativePlanningError{
		Code:   "CLIP_NATIVE_PLANNING_FAILED",
		ItemID: "item-1",
		Policy: "strict",
		Reason: "prose fallback not allowed",
	}
	wrapped := fmt.Errorf("%w: script generation failed", inner)
	env := buildSingleFailureEnvelope("item-1", wrapped)
	if env.Items[0].ErrorCode != "CLIP_NATIVE_PLANNING_FAILED" {
		t.Errorf("expected error_code CLIP_NATIVE_PLANNING_FAILED, got %q", env.Items[0].ErrorCode)
	}
}

func TestBuildSingleFailureEnvelope_PreservesResultAndMarksFailed(t *testing.T) {
	result := &domainScript.GenerationResult{ItemID: "item-1", Status: ""}
	env := buildSingleFailureEnvelope("item-1", fmt.Errorf("failure"), result)
	if env.Items[0].Result == nil {
		t.Fatal("expected preserved result")
	}
	if env.Items[0].Result.Status != domainScript.ItemStatusFailed {
		t.Errorf("expected status %s, got %q", domainScript.ItemStatusFailed, env.Items[0].Result.Status)
	}
}

func TestBuildSingleSuccessEnvelope_StrictSucceed(t *testing.T) {
	result := &domainScript.GenerationResult{ItemID: "item-1"}
	env := buildSingleSuccessEnvelope("item-1", result)
	if env.Items[0].Result.Status != domainScript.ItemStatusSucceeded {
		t.Errorf("expected status %s, got %q", domainScript.ItemStatusSucceeded, env.Items[0].Result.Status)
	}
}

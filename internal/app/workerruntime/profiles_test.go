// Package workerruntime — profiles_test.go (Creator Blocco 1.1, July 2026).
//
// Synthetic tests for WorkerProfile, WorkerProfileRegistry,
// and ResolveCapabilities. No integration tests — all mocks are
// in-process with controlled env override strings and
// registered-type sets.
package workerruntime

import (
	"sort"
	"strings"
	"testing"
)

// ── NewProfileRegistry ──────────────────────────────────────────────

func TestNewProfileRegistry_LookupCreator(t *testing.T) {
	reg := NewProfileRegistry()
	profile, err := reg.Lookup("creator")
	if err != nil {
		t.Fatalf("Lookup(creator): %v", err)
	}
	if profile.Name != "creator" {
		t.Errorf("Name = %q, want %q", profile.Name, "creator")
	}
	if profile.MaxParallel != 1 {
		t.Errorf("MaxParallel = %d, want 1", profile.MaxParallel)
	}
	// Snapshot: the creator profile must include at least
	// script.generate and voiceover.generate_item.
	hasScript := false
	hasVO := false
	for _, jt := range profile.AllowedJobTypes {
		if jt == "script.generate" {
			hasScript = true
		}
		if jt == "voiceover.generate_item" {
			hasVO = true
		}
	}
	if !hasScript {
		t.Error("creator profile missing script.generate")
	}
	if !hasVO {
		t.Error("creator profile missing voiceover.generate_item")
	}
}

func TestNewProfileRegistry_LookupRendererIsOverlayOnly(t *testing.T) {
	profile, err := NewProfileRegistry().Lookup("renderer")
	if err != nil {
		t.Fatal(err)
	}
	if !profile.RequiresGPU || !profile.RequiresFFmpeg {
		t.Fatalf("renderer requirements = gpu:%v ffmpeg:%v", profile.RequiresGPU, profile.RequiresFFmpeg)
	}
	if !stringSlicesEqual(profile.AllowedJobTypes, []string{"overlay.prepare", "overlay.render"}) {
		t.Fatalf("renderer jobs=%v", profile.AllowedJobTypes)
	}
	registered := []string{"overlay.prepare", "overlay.render", "script.generate"}
	caps, err := ResolveCapabilities(profile, `{"job_types":["overlay.render"]}`, registered)
	if err != nil {
		t.Fatal(err)
	}
	if len(caps.JobTypes) != 1 || caps.JobTypes[0] != "overlay.render" || !caps.GPU || !caps.FFmpeg {
		t.Fatalf("renderer caps=%+v", caps)
	}
	if _, err := ResolveCapabilities(profile, `{"job_types":["script.generate"]}`, registered); err == nil {
		t.Fatal("renderer profile expanded beyond ceiling")
	}
}

func TestNewProfileRegistry_LookupUnknown(t *testing.T) {
	reg := NewProfileRegistry()
	_, err := reg.Lookup("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown profile")
	}
	if !strings.Contains(err.Error(), "unknown worker profile") {
		t.Errorf("error message should mention 'unknown worker profile', got: %v", err)
	}
	if !strings.Contains(err.Error(), "creator") {
		t.Errorf("error message should list available profiles, got: %v", err)
	}
}

func TestNewProfileRegistry_LookupEmpty(t *testing.T) {
	reg := NewProfileRegistry()
	_, err := reg.Lookup("")
	if err == nil {
		t.Fatal("expected error for empty profile name")
	}
}

func TestNewProfileRegistry_LookupWhitespaceOnly(t *testing.T) {
	reg := NewProfileRegistry()
	_, err := reg.Lookup("   ")
	if err == nil {
		t.Fatal("expected error for whitespace-only profile name")
	}
}

func TestNewProfileRegistry_LookupNilRegistry(t *testing.T) {
	var reg *WorkerProfileRegistry
	_, err := reg.Lookup("creator")
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

// ── ResolveCapabilities with profile (empty env) ────────────────────

func TestResolveCapabilities_EmptyEnv_UsesProfileTypes(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate", "voiceover.generate_item"},
	}
	registered := []string{
		"script.generate",
		"voiceover.generate_item",
		"youtube.upload",
		"media.artlist",
	}
	caps, err := ResolveCapabilities(profile, "", registered)
	if err != nil {
		t.Fatalf("ResolveCapabilities: %v", err)
	}
	expected := []string{"script.generate", "voiceover.generate_item"}
	if !stringSlicesEqual(caps.JobTypes, expected) {
		t.Errorf("JobTypes = %v, want %v", caps.JobTypes, expected)
	}
}

// ── ResolveCapabilities with env narrowing ──────────────────────────

func TestResolveCapabilities_EnvNarrowing(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate", "voiceover.generate_item"},
	}
	registered := []string{
		"script.generate",
		"voiceover.generate_item",
	}
	envJSON := `{"job_types": ["script.generate"]}`
	caps, err := ResolveCapabilities(profile, envJSON, registered)
	if err != nil {
		t.Fatalf("ResolveCapabilities: %v", err)
	}
	expected := []string{"script.generate"}
	if !stringSlicesEqual(caps.JobTypes, expected) {
		t.Errorf("JobTypes = %v, want %v", caps.JobTypes, expected)
	}
}

// ── ResolveCapabilities rejects expansion beyond profile ────────────

func TestResolveCapabilities_EnvExpansionRejected(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate", "voiceover.generate_item"},
	}
	registered := []string{
		"script.generate",
		"youtube.upload",
	}
	envJSON := `{"job_types": ["script.generate", "youtube.upload"]}`
	_, err := ResolveCapabilities(profile, envJSON, registered)
	if err == nil {
		t.Fatal("expected error for env expansion beyond profile ceiling")
	}
	if !strings.Contains(err.Error(), "youtube.upload") {
		t.Errorf("error should mention the disallowed job type, got: %v", err)
	}
	if !strings.Contains(err.Error(), "not allowed by profile") {
		t.Errorf("error should explain the ceiling violation, got: %v", err)
	}
}

func TestResolveCapabilities_EnvAllExpansionRejected(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate"},
	}
	registered := []string{
		"script.generate",
		"render.video",
		"youtube.upload",
	}
	// Try to claim render.video — not in creator profile.
	envJSON := `{"job_types": ["render.video"]}`
	_, err := ResolveCapabilities(profile, envJSON, registered)
	if err == nil {
		t.Fatal("expected error for env claiming types outside profile")
	}
}

// ── ResolveCapabilities gates on registered types ───────────────────

func TestResolveCapabilities_ProfileTypeNotRegistered(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate", "image.generate.google"},
	}
	// "image.generate.google" is not in the registered set.
	registered := []string{"script.generate"}
	caps, err := ResolveCapabilities(profile, "", registered)
	if err != nil {
		t.Fatalf("ResolveCapabilities should not error on registered subset: %v", err)
	}
	// Only script.generate survives the registration gate.
	expected := []string{"script.generate"}
	if !stringSlicesEqual(caps.JobTypes, expected) {
		t.Errorf("JobTypes = %v, want %v", caps.JobTypes, expected)
	}
}

func TestResolveCapabilities_NoProfileTypesRegistered_Error(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate"},
	}
	// No handler registered for script.generate — should error.
	registered := []string{"youtube.upload"}
	_, err := ResolveCapabilities(profile, "", registered)
	if err == nil {
		t.Fatal("expected error when no profile types are registered")
	}
	if !strings.Contains(err.Error(), "resolved to empty capability set") {
		t.Errorf("error should mention empty capability set, got: %v", err)
	}
}

// ── ResolveCapabilities nil / empty edge cases ──────────────────────

func TestResolveCapabilities_NilProfile(t *testing.T) {
	_, err := ResolveCapabilities(nil, "", []string{"script.generate"})
	if err == nil {
		t.Fatal("expected error for nil profile")
	}
}

func TestResolveCapabilities_EmptyAllowedJobTypes(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "empty",
		AllowedJobTypes: []string{},
	}
	_, err := ResolveCapabilities(profile, "", []string{"script.generate"})
	if err == nil {
		t.Fatal("expected error for profile with no allowed job types")
	}
}

func TestResolveCapabilities_MalformedEnvJSON(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate"},
	}
	_, err := ResolveCapabilities(profile, `{bad json`, []string{"script.generate"})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should mention malformed JSON, got: %v", err)
	}
}

func TestResolveCapabilities_EmptyJobTypesArray(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate"},
	}
	_, err := ResolveCapabilities(profile, `{"job_types": []}`, []string{"script.generate"})
	if err == nil {
		t.Fatal("expected error for empty job_types array")
	}
	if !strings.Contains(err.Error(), "empty job_types") {
		t.Errorf("error should mention empty job_types, got: %v", err)
	}
}

// ── ResolveCapabilities dedup + sort ────────────────────────────────

func TestResolveCapabilities_DedupAndSort(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"voiceover.generate_item", "script.generate"},
	}
	registered := []string{
		"script.generate",
		"voiceover.generate_item",
	}
	// Profile types are already unsorted; result must be sorted.
	caps, err := ResolveCapabilities(profile, "", registered)
	if err != nil {
		t.Fatalf("ResolveCapabilities: %v", err)
	}
	expected := []string{"script.generate", "voiceover.generate_item"}
	if !stringSlicesEqual(caps.JobTypes, expected) {
		t.Errorf("JobTypes = %v, want sorted %v", caps.JobTypes, expected)
	}
}

func TestResolveCapabilities_EnvDeduplicatesDuplicates(t *testing.T) {
	profile := &WorkerProfile{
		Name:            "creator",
		AllowedJobTypes: []string{"script.generate", "voiceover.generate_item"},
	}
	registered := []string{
		"script.generate",
		"voiceover.generate_item",
	}
	envJSON := `{"job_types": ["script.generate", "script.generate"]}`
	caps, err := ResolveCapabilities(profile, envJSON, registered)
	if err != nil {
		t.Fatalf("ResolveCapabilities: %v", err)
	}
	expected := []string{"script.generate"}
	if !stringSlicesEqual(caps.JobTypes, expected) {
		t.Errorf("JobTypes = %v, want %v", caps.JobTypes, expected)
	}
}

// ── ParseAndValidateCaps: empty env → error (Blocco 1.2) ────────────

func TestParseAndValidateCaps_EmptyEnv_ReturnsError(t *testing.T) {
	registered := []string{"script.generate", "voiceover.generate_item", "youtube.upload"}
	_, err := ParseAndValidateCaps("", registered)
	if err == nil {
		t.Fatal("expected error for empty VELOX_WORKER_CAPABILITIES with no profile")
	}
	if !strings.Contains(err.Error(), "empty and no worker profile") {
		t.Errorf("error should mention empty capabilities + no profile, got: %v", err)
	}
}

func TestParseAndValidateCaps_WhitespaceOnlyEnv_ReturnsError(t *testing.T) {
	registered := []string{"script.generate"}
	_, err := ParseAndValidateCaps("   ", registered)
	if err == nil {
		t.Fatal("expected error for whitespace-only VELOX_WORKER_CAPABILITIES")
	}
}

func TestParseAndValidateCaps_NonEmptyEnv_ParsesAndValidates(t *testing.T) {
	registered := []string{"script.generate", "voiceover.generate_item"}
	caps, err := ParseAndValidateCaps(`{"job_types": ["script.generate"]}`, registered)
	if err != nil {
		t.Fatalf("ParseAndValidateCaps: %v", err)
	}
	expected := []string{"script.generate"}
	if !stringSlicesEqual(caps.JobTypes, expected) {
		t.Errorf("JobTypes = %v, want %v", caps.JobTypes, expected)
	}
}

func TestParseAndValidateCaps_UnknownType_ReturnsError(t *testing.T) {
	registered := []string{"script.generate"}
	_, err := ParseAndValidateCaps(`{"job_types": ["youtube.upload"]}`, registered)
	if err == nil {
		t.Fatal("expected error for unknown job type")
	}
	if !strings.Contains(err.Error(), "unknown job type") {
		t.Errorf("error should mention unknown job type, got: %v", err)
	}
}

func TestParseAndValidateCaps_MalformedJSON_ReturnsError(t *testing.T) {
	_, err := ParseAndValidateCaps(`{bad`, []string{"script.generate"})
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Errorf("error should mention malformed JSON, got: %v", err)
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string{}, a...)
	bb := append([]string{}, b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}

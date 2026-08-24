// cmd/admin/index_drive_clip_test.go — pure-function tests for the
// index-drive-clip manifest loader + validator.
//
// These tests cover the only piece of cmd/admin/index_drive_clip.go
// that does not require a live Drive / outbox / composition root.
// The runtime path (download → hash → EnqueueAndIndex → wait) is
// exercised by integration tests outside the unit suite.

package drive

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadIndexClipManifest_HappyPath(t *testing.T) {
	fixture := filepath.Join("manifests", "stargazer-fish.json")
	m, err := loadIndexClipManifest(fixture)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.DriveFileID != "19zV7kxyINQeMGP68VxlhuvWCRJUX-bc4" {
		t.Errorf("DriveFileID: got %q", m.DriveFileID)
	}
	if m.Name == "" {
		t.Error("Name: empty")
	}
	if m.Description == "" {
		t.Error("Description: empty")
	}
	if m.DescriptionAlt == "" {
		t.Error("DescriptionAlt: empty (fish fixture must declare the Italian variant)")
	}
	if len(m.Tags) == 0 {
		t.Error("Tags: empty")
	}
	if m.Source != "ai_generated" {
		t.Errorf("Source: got %q, want ai_generated", m.Source)
	}
	if m.Category != "Ocean" {
		t.Errorf("Category: got %q, want Ocean", m.Category)
	}
	if m.Group != "topfive_fish" {
		t.Errorf("Group: got %q, want topfive_fish", m.Group)
	}
	if m.LocalSubdir != "fish_shorts" {
		t.Errorf("LocalSubdir: got %q, want fish_shorts", m.LocalSubdir)
	}
	if m.DurationFallbackSeconds != 0 {
		t.Errorf("DurationFallbackSeconds: got %d, want 0 (fail-closed default for Fish)", m.DurationFallbackSeconds)
	}
	if m.Metadata["content_type"] != "vertical fish short" {
		t.Errorf("metadata.content_type: got %q", m.Metadata["content_type"])
	}
	if m.Metadata["timeline_json"] == "" {
		t.Error("metadata.timeline_json: empty")
	}
	if m.Metadata["hook"] == "" {
		t.Error("metadata.hook: empty")
	}
	if m.Metadata["audio_policy"] == "" {
		t.Error("metadata.audio_policy: empty")
	}
}

func TestLoadIndexClipManifest_BelugaPreservesFallback(t *testing.T) {
	fixture := filepath.Join("manifests", "beluga.json")
	m, err := loadIndexClipManifest(fixture)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	if m.DurationFallbackSeconds != 9 {
		t.Errorf("Beluga fallback: got %d, want 9 (preserves retired silent fallback)", m.DurationFallbackSeconds)
	}
	if m.Source != "clip_drive" {
		t.Errorf("Source: got %q, want clip_drive", m.Source)
	}
	if m.Group != "funny_animals" {
		t.Errorf("Group: got %q, want funny_animals", m.Group)
	}
	if m.LocalSubdir != "viral_videos" {
		t.Errorf("LocalSubdir: got %q, want viral_videos", m.LocalSubdir)
	}
	// Beluga fixture carries the short alt description (the legacy
	// SearchText was [name, para1, para2, tags-joined]); the manifest
	// preserves both paragraphs and index_drive_clip.go splices them
	// in order via DescriptionAlt. The hook / audio_policy keys are
	// Fish-only and MUST be absent here.
	if m.DescriptionAlt != "beluga whale scaring kid at aquarium prank funny animal video" {
		t.Errorf("DescriptionAlt: got %q, want beluga alt blurb", m.DescriptionAlt)
	}
	if _, ok := m.Metadata["hook"]; ok {
		t.Errorf("Beluga metadata.hook should be absent, got %q", m.Metadata["hook"])
	}
	if _, ok := m.Metadata["audio_policy"]; ok {
		t.Errorf("Beluga metadata.audio_policy should be absent, got %q", m.Metadata["audio_policy"])
	}
}

func TestIndexClipManifest_Validate_RejectsMissingFields(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*indexClipManifest)
		want   string
	}{
		{"missing drive_file_id", func(m *indexClipManifest) { m.DriveFileID = "" }, "drive_file_id"},
		{"missing name", func(m *indexClipManifest) { m.Name = "" }, "name"},
		{"missing description", func(m *indexClipManifest) { m.Description = "" }, "description"},
		{"empty tags", func(m *indexClipManifest) { m.Tags = nil }, "tags"},
		{"missing source", func(m *indexClipManifest) { m.Source = "" }, "source"},
		{"missing category and local_subdir", func(m *indexClipManifest) { m.Category = ""; m.LocalSubdir = "" }, "category"},
		{"missing group", func(m *indexClipManifest) { m.Group = "" }, "group"},
		{"negative duration_fallback", func(m *indexClipManifest) { m.DurationFallbackSeconds = -1 }, "duration_fallback_seconds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := minimalValidManifest()
			tc.mutate(m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error message: got %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestLoadIndexClipManifest_RejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	contents := `{
		"drive_file_id": "x",
		"name": "x",
		"description": "x",
		"tags": ["x"],
		"source": "x",
		"category": "x",
		"group": "x",
		"this_field_does_not_exist": true
	}`
	if err := os.WriteFile(bad, []byte(contents), 0o644); err != nil {
		t.Fatalf("write bad manifest: %v", err)
	}
	_, err := loadIndexClipManifest(bad)
	if err == nil {
		t.Fatal("expected unknown-field error, got nil")
	}
	if !strings.Contains(err.Error(), "this_field_does_not_exist") {
		t.Errorf("error message: got %q, want mention of unknown field", err.Error())
	}
}

func TestLoadIndexClipManifest_RejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(bad, []byte("{not valid json"), 0o644); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}
	_, err := loadIndexClipManifest(bad)
	if err == nil {
		t.Fatal("expected JSON decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode JSON") {
		t.Errorf("error message: got %q, want substring 'decode JSON'", err.Error())
	}
}

func TestLoadIndexClipManifest_RejectsMissingFile(t *testing.T) {
	_, err := loadIndexClipManifest(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected file-read error, got nil")
	}
	if !strings.Contains(err.Error(), "read manifest") {
		t.Errorf("error message: got %q, want substring 'read manifest'", err.Error())
	}
}

func minimalValidManifest() *indexClipManifest {
	return &indexClipManifest{
		DriveFileID: "x",
		Name:        "x",
		Description: "x",
		Tags:        []string{"x"},
		Source:      "x",
		Category:    "x",
		Group:       "x",
	}
}

// resolveClipDuration tests — Sprint 2.3 pin for the silent-false-success
// fix. Five cases cover the full decision matrix.

// 1. Measured success — the happy path.
func TestResolveClipDuration_MeasuredSuccess(t *testing.T) {
	dur, source, err := resolveClipDuration(5*time.Second, nil, 9, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dur != 5*time.Second {
		t.Errorf("dur: got %s, want 5s", dur)
	}
	if source != "measured" {
		t.Errorf("source: got %q, want measured", source)
	}
}

//  2. Probe failed, fallback declared, --allow-declared-duration passed.
//     The asset is tagged declared_fallback so downstream consumers know
//     the duration was not measured.
func TestResolveClipDuration_DeclaredFallbackWithFlag(t *testing.T) {
	probeErr := errors.New("ffprobe exited 1")
	dur, source, err := resolveClipDuration(0, probeErr, 9, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dur != 9*time.Second {
		t.Errorf("dur: got %s, want 9s", dur)
	}
	if source != "declared_fallback" {
		t.Errorf("source: got %q, want declared_fallback", source)
	}
}

//  3. Probe failed, fallback declared, flag NOT passed. MUST fail closed
//     with an error that mentions the flag AND wraps the probe error.
func TestResolveClipDuration_FailsClosedWithoutFlag(t *testing.T) {
	probeErr := errors.New("ffprobe exited 1")
	_, source, err := resolveClipDuration(0, probeErr, 9, false)
	if err == nil {
		t.Fatal("expected error, got nil (silent false success)")
	}
	if source != "" {
		t.Errorf("source: got %q, want empty on fail-closed", source)
	}
	if !strings.Contains(err.Error(), "--allow-declared-duration") {
		t.Errorf("error message: got %q, want mention of --allow-declared-duration", err.Error())
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("error must wrap the original probeErr for diagnostics")
	}
}

//  4. Probe failed, NO fallback declared. MUST fail closed regardless of
//     the flag (no fallback to honour).
func TestResolveClipDuration_FailsClosedWithoutFallback(t *testing.T) {
	probeErr := errors.New("ffprobe exited 1")
	_, source, err := resolveClipDuration(0, probeErr, 0, true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if source != "" {
		t.Errorf("source: got %q, want empty on fail-closed", source)
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("error must wrap the original probeErr for diagnostics")
	}
}

//  5. Degenerate probe result (no error but non-positive duration). MUST
//     fail closed — e.g. probeSoundEffectDuration returns (0, nil) for an
//     empty path; the caller must not silently treat that as 0 seconds.
func TestResolveClipDuration_FailsClosedOnZeroMeasuredDuration(t *testing.T) {
	_, source, err := resolveClipDuration(0, nil, 9, false)
	if err == nil {
		t.Fatal("expected error on zero measured duration, got nil (silent zero)")
	}
	if source != "" {
		t.Errorf("source: got %q, want empty on fail-closed", source)
	}
	if !strings.Contains(err.Error(), "non-positive duration") {
		t.Errorf("error message: got %q, want mention of non-positive duration", err.Error())
	}
}

//  6. Sanity: the negative-fallback validator in manifest.Validate still
//     refuses negative fallbackSeconds before resolveClipDuration ever
//     runs — pins the manifest-level guard.
func TestResolveClipDuration_NegativeFallbackRejectedByManifest(t *testing.T) {
	m := minimalValidManifest()
	m.DurationFallbackSeconds = -1
	if err := m.Validate(); err == nil {
		t.Fatal("manifest.Validate must reject negative fallback")
	}
}

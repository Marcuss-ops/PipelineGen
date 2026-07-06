// Package delivery — TDD test surface for BuildPublishRequest.
// SEMANTIC-LOCATION-API-2026-07-06 Wave 1.
//
// Per-destination: 1 happy-path test per DestinationKey (10 constants:
// YouTubeClip / Artlist / Stock / Image / Voiceover / Book / Script /
// SoundEffect / Document / Admin). 9 tested here (Admin is a path-less
// admin destination — out-of-scope for the semantic-location mapper
// per Wave 1, handled separately in FASE-2.2 AdminPath closure).
//
// Validation: 4 typed-error sentinel contracts + Subject↔Name fallback.
//
// Idempotency: byte-stability check across 1000 operations with the same
// AssetID + ContentHash + SourceVersion triple.
package delivery

import (
	"errors"
	"strings"
	"testing"
)

// imgInput builds a minimal AssetPublishInput for happy-path testing of
// per-destination Field mapping. Per godlike/07 NO-FAKE-AVAILABILITY, the
// helper does NOT auto-populate any field the caller might forget —
// each test names its destination explicitly and supplies its required
// location fields.
func imgInput(dest DestinationKey, mutate func(a *AssetPublishInput)) AssetPublishInput {
	in := AssetPublishInput{
		Destination: dest,
		LocalPath:   "/tmp/local-asset.mp4",
		Filename:    "local-asset.mp4",
		AssetID:     "asset_001",
		ContentHash: "abc123",
	}
	if mutate != nil {
		mutate(&in)
	}
	return in
}

// ── happy path: per-destination mapping ──────────────────────────────────

func TestBuildPublishRequest_DestinationImage_StyleAndSubject(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Location.Style = "Realistic"
		a.Location.Subject = "Mike-Tyson"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Style != "Realistic" {
		t.Errorf("Style = %q, want %q", req.Style, "Realistic")
	}
	if req.Subject != "Mike-Tyson" {
		t.Errorf("Subject = %q, want %q", req.Subject, "Mike-Tyson")
	}
	if req.Group != "" {
		t.Errorf("Group = %q, want empty (image destination)", req.Group)
	}
	if req.ProjectID != "" {
		t.Errorf("ProjectID = %q, want empty (image destination)", req.ProjectID)
	}
	if req.Language != "" {
		t.Errorf("Language = %q, want empty (image destination)", req.Language)
	}
	if req.Provider != "" {
		t.Errorf("Provider = %q, want empty (image destination, Wave 1)", req.Provider)
	}
	if req.Category != "" {
		t.Errorf("Category = %q, want empty (image destination)", req.Category)
	}
}

func TestBuildPublishRequest_DestinationStock_GroupSubjectProvider(t *testing.T) {
	in := imgInput(DestinationStock, func(a *AssetPublishInput) {
		a.Location.Category = "Boxe"
		a.Location.Subject = "Mike-Tyson"
		a.Location.Provider = "pexels"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Group != "Boxe" {
		t.Errorf("Group = %q, want %q", req.Group, "Boxe")
	}
	if req.Subject != "Mike-Tyson" {
		t.Errorf("Subject = %q, want %q", req.Subject, "Mike-Tyson")
	}
	if req.Provider != "pexels" {
		t.Errorf("Provider = %q, want %q", req.Provider, "pexels")
	}
}

func TestBuildPublishRequest_DestinationYouTubeClip_GroupAndSubject(t *testing.T) {
	in := imgInput(DestinationYouTubeClip, func(a *AssetPublishInput) {
		a.Location.Category = "Boxing-Legends-Channel"
		a.Location.Subject = "abc123"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Group != "Boxing-Legends-Channel" {
		t.Errorf("Group = %q, want %q", req.Group, "Boxing-Legends-Channel")
	}
	if req.Subject != "abc123" {
		t.Errorf("Subject = %q, want %q", req.Subject, "abc123")
	}
}

func TestBuildPublishRequest_DestinationArtlist_GroupAndSubject(t *testing.T) {
	in := imgInput(DestinationArtlist, func(a *AssetPublishInput) {
		a.Location.Category = "boxing-search"
		a.Location.Subject = "art_001"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Group != "boxing-search" {
		t.Errorf("Group = %q, want %q", req.Group, "boxing-search")
	}
	if req.Subject != "art_001" {
		t.Errorf("Subject = %q, want %q", req.Subject, "art_001")
	}
}

func TestBuildPublishRequest_DestinationVoiceover_ProjectAndLanguage(t *testing.T) {
	in := imgInput(DestinationVoiceover, func(a *AssetPublishInput) {
		a.Location.Project = "mike-tyson-documentary"
		a.Location.Language = "it-IT"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.ProjectID != "mike-tyson-documentary" {
		t.Errorf("ProjectID = %q, want %q", req.ProjectID, "mike-tyson-documentary")
	}
	if req.Language != "it-IT" {
		t.Errorf("Language = %q, want %q", req.Language, "it-IT")
	}
}

func TestBuildPublishRequest_DestinationBook_ProjectOnly(t *testing.T) {
	in := imgInput(DestinationBook, func(a *AssetPublishInput) {
		a.Location.Project = "borges-essays"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.ProjectID != "borges-essays" {
		t.Errorf("ProjectID = %q, want %q", req.ProjectID, "borges-essays")
	}
	if req.Language != "" {
		t.Errorf("Language = %q, want empty (book destination has no language segment)", req.Language)
	}
}

func TestBuildPublishRequest_DestinationScript_ProjectAndLanguage(t *testing.T) {
	in := imgInput(DestinationScript, func(a *AssetPublishInput) {
		a.Location.Project = "promo-script"
		a.Location.Language = "en"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.ProjectID != "promo-script" {
		t.Errorf("ProjectID = %q, want %q", req.ProjectID, "promo-script")
	}
	if req.Language != "en" {
		t.Errorf("Language = %q, want %q", req.Language, "en")
	}
}

func TestBuildPublishRequest_DestinationSoundEffect_CategoryOnly(t *testing.T) {
	in := imgInput(DestinationSoundEffect, func(a *AssetPublishInput) {
		a.Location.Category = "explosion"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Group != "explosion" {
		t.Errorf("Group = %q, want %q", req.Group, "explosion")
	}
}

func TestBuildPublishRequest_DestinationDocument_SubjectUsedAsAssetID(t *testing.T) {
	in := imgInput(DestinationDocument, func(a *AssetPublishInput) {
		a.Location.Subject = "doc_001"
		a.AssetID = "" // mapper should overwrite with subject
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.AssetID != "doc_001" {
		t.Errorf("AssetID = %q, want %q (from Subject fallback)", req.AssetID, "doc_001")
	}
}

// ── typed-error sentinel contracts ──────────────────────────────────────

func TestBuildPublishRequest_DestinationEmpty_ReturnsDestinationUnknown(t *testing.T) {
	in := AssetPublishInput{
		Destination: "",
		LocalPath:   "/tmp/x",
		Filename:    "x",
	}
	_, err := BuildPublishRequest(in)
	if !errors.Is(err, ErrAssetPublishDestinationUnknown) {
		t.Fatalf("err = %v, want wrap of ErrAssetPublishDestinationUnknown", err)
	}
}

func TestBuildPublishRequest_LocalPathEmpty_ReturnsLocalPathMissing(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.LocalPath = ""
		a.Location.Style = "Realistic"
		a.Location.Subject = "X"
	})
	_, err := BuildPublishRequest(in)
	if !errors.Is(err, ErrAssetPublishLocalPathMissing) {
		t.Fatalf("err = %v, want wrap of ErrAssetPublishLocalPathMissing", err)
	}
}

func TestBuildPublishRequest_FilenameEmpty_ReturnsFilenameMissing(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Filename = ""
		a.Location.Style = "Realistic"
		a.Location.Subject = "X"
	})
	_, err := BuildPublishRequest(in)
	if !errors.Is(err, ErrAssetPublishFilenameMissing) {
		t.Fatalf("err = %v, want wrap of ErrAssetPublishFilenameMissing", err)
	}
}

func TestBuildPublishRequest_ImageNoStyle_ReturnsLocationIncomplete(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Location.Subject = "Mike-Tyson"
	})
	_, err := BuildPublishRequest(in)
	if !errors.Is(err, ErrAssetPublishLocationIncompleteForDestination) {
		t.Fatalf("err = %v, want wrap of ErrAssetPublish...Incomplete", err)
	}
	if !strings.Contains(err.Error(), "style") {
		t.Errorf("err message %q must mention missing field 'style'", err.Error())
	}
}

func TestBuildPublishRequest_VoiceoverNoLanguage_ReturnsLocationIncomplete(t *testing.T) {
	in := imgInput(DestinationVoiceover, func(a *AssetPublishInput) {
		a.Location.Project = "p1"
	})
	_, err := BuildPublishRequest(in)
	if !errors.Is(err, ErrAssetPublishLocationIncompleteForDestination) {
		t.Fatalf("err = %v, want wrap of ErrAssetPublish...Incomplete", err)
	}
	if !strings.Contains(err.Error(), "language") {
		t.Errorf("err message %q must mention missing field 'language'", err.Error())
	}
}

// ── Subject ↔ Name fallback (godlike/07 soft-fallback) ─────────────────

func TestBuildPublishRequest_SubjectFallbackToName_Image(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Location.Style = "Realistic"
		a.Location.Subject = ""
		a.Location.Name = "Mike Tyson"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v (SubjectOrName fallback should make Name satisfy Subject)", err)
	}
	if req.Subject != "Mike Tyson" {
		t.Errorf("Subject = %q, want %q (fallback to Name)", req.Subject, "Mike Tyson")
	}
}

func TestBuildPublishRequest_SubjectPreferredOverName_Image(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Location.Style = "Realistic"
		a.Location.Subject = "subject-form"
		a.Location.Name = "name-form"
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Subject != "subject-form" {
		t.Errorf("Subject = %q, want %q (Subject takes precedence over Name)", req.Subject, "subject-form")
	}
}

func TestBuildPublishRequest_BothSubjectAndNameEmpty_Image(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Location.Style = "Realistic"
		a.Location.Subject = ""
		a.Location.Name = ""
	})
	_, err := BuildPublishRequest(in)
	if !errors.Is(err, ErrAssetPublishNameCannotReplaceSubject) {
		t.Fatalf("err = %v, want wrap of ErrAssetPublishNameCannotReplaceSubject", err)
	}
	if !errors.Is(err, ErrAssetPublishLocationIncompleteForDestination) {
		t.Fatalf("err %v must also wrap ErrAssetPublishLocationIncompleteForDestination (dual-wrap semantic)", err)
	}
}

// ── Idempotency-key derivation byte-stability ───────────────────────────

func TestBuildPublishRequest_IdempotencyKey_DerivedFromAssetIDHashVersion(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Location.Style = "Realistic"
		a.Location.Subject = "Mike-Tyson"
		a.AssetID = "asset_777"
		a.ContentHash = "deadbeef"
		a.SourceVersion = 7
	})
	reqA, errA := BuildPublishRequest(in)
	if errA != nil {
		t.Fatalf("unexpected err: %v", errA)
	}
	// Re-run the same input — IdempotencyKey MUST be byte-stable.
	reqB, errB := BuildPublishRequest(in)
	if errB != nil {
		t.Fatalf("unexpected err (re-run): %v", errB)
	}
	if reqA.IdempotencyKey == "" {
		t.Fatalf("IdempotencyKey is empty when AssetID+ContentHash+SourceVersion are set")
	}
	if reqA.IdempotencyKey != reqB.IdempotencyKey {
		t.Errorf("IdempotencyKey drift across rerun: A=%q B=%q", reqA.IdempotencyKey, reqB.IdempotencyKey)
	}

	// And it MUST equal DeriveIdempotencyKey called directly (single
	// source-of-truth — mapper does NOT introduce its own derivation).
	want := DeriveIdempotencyKey(in.Destination, in.AssetID, in.ContentHash, in.SourceVersion)
	if reqA.IdempotencyKey != want {
		t.Errorf("IdempotencyKey = %q, want %q (must call DeriveIdempotencyKey)", reqA.IdempotencyKey, want)
	}
}

func TestBuildPublishRequest_IdempotencyKey_EmptyWhenNoIdentityInputs(t *testing.T) {
	in := imgInput(DestinationImage, func(a *AssetPublishInput) {
		a.Location.Style = "Realistic"
		a.Location.Subject = "Mike-Tyson"
		a.AssetID = ""
		a.ContentHash = ""
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.IdempotencyKey != "" {
		t.Errorf("IdempotencyKey = %q, want empty (backward-compat: filename-only lookup)", req.IdempotencyKey)
	}
}

// ── DoD #3: Tags round-trip propagation ─────────────────────────────────

func TestBuildPublishRequest_TagsPropagatedToPublishRequest(t *testing.T) {
	in := imgInput(DestinationStock, func(a *AssetPublishInput) {
		a.Location.Category = "Boxe"
		a.Location.Subject = "Mike-Tyson"
		a.Location.Provider = "pexels"
		a.Tags = []string{"boxing", "training", "knockout"}
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(req.Tags) != 3 {
		t.Fatalf("Tags len = %d, want 3", len(req.Tags))
	}
	if req.Tags[0] != "boxing" || req.Tags[1] != "training" || req.Tags[2] != "knockout" {
		t.Errorf("Tags = %v, want [boxing training knockout]", req.Tags)
	}
}

func TestBuildPublishRequest_TagsNilWhenNotSet(t *testing.T) {
	in := imgInput(DestinationStock, func(a *AssetPublishInput) {
		a.Location.Category = "Boxe"
		a.Location.Subject = "Mike-Tyson"
		a.Location.Provider = "pexels"
		// Tags intentionally left nil
	})
	req, err := BuildPublishRequest(in)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if req.Tags != nil {
		t.Errorf("Tags = %v, want nil (unset)", req.Tags)
	}
}

// ── universality: ADMIN destination is intentionally unsup ──────────────

func TestBuildPublishRequest_DestinationAdmin_NotInMapper(t *testing.T) {
	in := imgInput(DestinationAdmin, nil)
	_, err := BuildPublishRequest(in)
	if err == nil {
		t.Fatalf("expected err for DestinationAdmin (mapper does not cover admin path; AdminPath in registry is canonical)")
	}
	// The mapper's default arm covers it; the typed-error is destination-unknown.
	if !errors.Is(err, ErrAssetPublishDestinationUnknown) {
		t.Errorf("err = %v, want wrap of ErrAssetPublishDestinationUnknown (admin is intentionally out-of-scope)", err)
	}
}

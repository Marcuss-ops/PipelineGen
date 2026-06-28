package voiceover_test

import (
	"errors"
	"testing"

	vo "github.com/Marcuss-ops/PipelineGen/internal/domain/voiceover"
)

// TestCommand_ID_Deterministic verifies that two commands with identical
// fields produce the same ID, and that changing any field produces a
// different ID.
func TestCommand_ID_Deterministic(t *testing.T) {
	a := vo.GenerateVoiceoverCommand{
		Text:        "Hello world",
		Locale:      "en-US",
		Voice:       "en-US-RogerNeural",
		Destination: vo.DestinationRef{FolderID: "1abc"},
	}
	b := vo.GenerateVoiceoverCommand{
		Text:        "Hello world",
		Locale:      "en-US",
		Voice:       "en-US-RogerNeural",
		Destination: vo.DestinationRef{FolderID: "1abc"},
	}
	if a.ID() != b.ID() {
		t.Errorf("identical commands must produce identical IDs: got %q vs %q", a.ID(), b.ID())
	}

	// Different text → different ID
	c := a
	c.Text = "Different text"
	if a.ID() == c.ID() {
		t.Errorf("different text must produce different IDs")
	}

	// Different locale → different ID
	d := a
	d.Locale = "it-IT"
	if a.ID() == d.ID() {
		t.Errorf("different locale must produce different IDs")
	}

	// Different destination → different ID
	e := a
	e.Destination.FolderID = "2def"
	if a.ID() == e.ID() {
		t.Errorf("different destination FolderID must produce different IDs")
	}
}

// TestCommand_Filename_Deterministic verifies filename format.
func TestCommand_Filename_Deterministic(t *testing.T) {
	cmd := vo.GenerateVoiceoverCommand{
		Text:        "Test",
		Locale:      "en-us",
		Voice:       "en-US-RogerNeural",
		Destination: vo.DestinationRef{FolderID: "abc123"},
	}
	cmd = cmd.Normalize()

	filename := cmd.Filename()

	// Must start with vo_ and end with .mp3
	if len(filename) < 7 {
		t.Fatalf("filename too short: %q", filename)
	}
	prefix := filename[:3]
	suffix := filename[len(filename)-4:]
	if prefix != "vo_" {
		t.Errorf("filename must start with vo_: got %q", filename)
	}
	if suffix != ".mp3" {
		t.Errorf("filename must end with .mp3: got %q", filename)
	}
}

// TestCommand_Filename_WithReference uses script/scene reference.
func TestCommand_Filename_WithReference(t *testing.T) {
	cmd := vo.GenerateVoiceoverCommand{
		Text:      "Scene dialogue",
		Locale:    "it-IT",
		Reference: vo.Reference{ScriptID: "script-001", SceneID: "scene-04"},
	}
	cmd = cmd.Normalize()

	filename := cmd.Filename()
	expected := "vo_script-001_scene-04_it-it.mp3"
	if filename != expected {
		t.Errorf("expected filename %q, got %q", expected, filename)
	}
}

// TestCommand_Validate_TextRequired rejects empty text.
func TestCommand_Validate_TextRequired(t *testing.T) {
	cmd := vo.GenerateVoiceoverCommand{
		Locale: "en-US",
	}
	cmd = cmd.Normalize()
	err := cmd.Validate()
	if err == nil {
		t.Fatal("expected error for empty text")
	}
	if !errors.Is(err, vo.ErrTextRequired) {
		t.Errorf("expected ErrTextRequired, got %v", err)
	}
}

// TestCommand_Validate_LocaleRequired rejects empty locale.
func TestCommand_Validate_LocaleRequired(t *testing.T) {
	cmd := vo.GenerateVoiceoverCommand{
		Text: "Some text",
	}
	cmd = cmd.Normalize()
	err := cmd.Validate()
	if err == nil {
		t.Fatal("expected error for empty locale")
	}
	if !errors.Is(err, vo.ErrLocaleRequired) {
		t.Errorf("expected ErrLocaleRequired, got %v", err)
	}
}

// TestCommand_Validate_Valid passes with text + locale.
func TestCommand_Validate_Valid(t *testing.T) {
	cmd := vo.GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "en-US",
	}
	cmd = cmd.Normalize()
	if err := cmd.Validate(); err != nil {
		t.Errorf("expected no error for valid command, got: %v", err)
	}
}

// TestCommand_Normalize_LowersLocale verifies locale is lowercased.
func TestCommand_Normalize_LowersLocale(t *testing.T) {
	cmd := vo.GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "EN-US",
		Voice:  "en-US-RogerNeural",
	}
	normalized := cmd.Normalize()
	if normalized.Locale != "en-us" {
		t.Errorf("expected locale 'en-us', got %q", normalized.Locale)
	}
}

// TestCommand_Normalize_TrimsFields verifies fields are trimmed.
func TestCommand_Normalize_TrimsFields(t *testing.T) {
	cmd := vo.GenerateVoiceoverCommand{
		Text:   "  Hello  ",
		Locale: "  en-US  ",
		Voice:  "  voice  ",
	}
	normalized := cmd.Normalize()
	if normalized.Text != "Hello" {
		t.Errorf("expected trimmed text 'Hello', got %q", normalized.Text)
	}
	if normalized.Locale != "en-us" {
		t.Errorf("expected trimmed+lowercased locale 'en-us', got %q", normalized.Locale)
	}
	if normalized.Voice != "voice" {
		t.Errorf("expected trimmed voice 'voice', got %q", normalized.Voice)
	}
}

// TestResult_AddWarning tests the AddWarning helper.
func TestResult_AddWarning(t *testing.T) {
	r := &vo.Result{}
	r.AddWarning("test warning")
	if len(r.Warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(r.Warnings))
	}
	if r.Warnings[0] != "test warning" {
		t.Errorf("expected 'test warning', got %q", r.Warnings[0])
	}

	// Nil receiver is safe
	var nilResult *vo.Result
	nilResult.AddWarning("should not panic")
}

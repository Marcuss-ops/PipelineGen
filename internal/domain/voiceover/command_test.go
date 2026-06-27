// Package voiceover — command_test.go tests the canonical voiceover domain types.
//
// PR 1 (June 2026): validates every invariant of GenerateVoiceoverCommand,
// DestinationRef, Reference, VoiceProfile, deterministic ID, and filename.
package voiceover

import (
	"strings"
	"testing"
)

// ── GenerateVoiceoverCommand.Validate ────────────────────────────────────

func TestCommand_Valid(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Hello, world!",
		Locale: "en-US",
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("expected valid command, got: %v", err)
	}
}

func TestCommand_EmptyText(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "",
		Locale: "en-US",
	}
	err := cmd.Validate()
	if err == nil {
		t.Fatal("expected error for empty text")
	}
	if !strings.Contains(err.Error(), "text") {
		t.Errorf("error should mention 'text', got: %v", err)
	}
}

func TestCommand_WhitespaceOnlyText(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "   \n\t  ",
		Locale: "en-US",
	}
	err := cmd.Validate()
	if err == nil {
		t.Fatal("expected error for whitespace-only text")
	}
}

func TestCommand_EmptyLocale(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "",
	}
	err := cmd.Validate()
	if err == nil {
		t.Fatal("expected error for empty locale")
	}
	if !strings.Contains(err.Error(), "locale") {
		t.Errorf("error should mention 'locale', got: %v", err)
	}
}

func TestCommand_UnsupportedLocale(t *testing.T) {
	tests := []struct {
		name   string
		locale Locale
	}{
		{"numeric", "123"},
		{"special chars", "en@US"},
		{"just underscore", "en_US"},
		{"with spaces", "en US"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := GenerateVoiceoverCommand{
				Text:   "Hello",
				Locale: tt.locale,
			}
			err := cmd.Validate()
			if err == nil {
				t.Fatalf("expected error for unsupported locale %q", tt.locale)
			}
			if _, ok := err.(*LocaleNotSupportedError); !ok {
				t.Errorf("expected *LocaleNotSupportedError, got %T: %v", err, err)
			}
		})
	}
}

func TestCommand_SupportedLocales(t *testing.T) {
	tests := []Locale{
		"en-US", "en-GB", "it-IT", "pt-BR", "pt-PT", "fr-FR",
		"de-DE", "es-ES", "ja-JP", "ko-KR", "ru-RU", "nl-NL",
		"pl-PL", "tr-TR", "id-ID", "zh-CN", "ar-SA",
		"en", "it", "fr", "de", // bare language codes
	}
	for _, loc := range tests {
		t.Run(string(loc), func(t *testing.T) {
			cmd := GenerateVoiceoverCommand{
				Text:   "Hello",
				Locale: loc,
			}
			if err := cmd.Validate(); err != nil {
				t.Errorf("expected %q to be supported, got: %v", loc, err)
			}
		})
	}
}

func TestCommand_WithVoice(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "en-US",
		Voice:  "en-US-RogerNeural",
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("expected valid command with voice, got: %v", err)
	}
}

func TestCommand_ForceRegenerate(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:            "Hello",
		Locale:          "en-US",
		ForceRegenerate: true,
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("expected valid command with ForceRegenerate, got: %v", err)
	}
	if !cmd.ForceRegenerate {
		t.Error("ForceRegenerate should be true")
	}
}

func TestCommand_WithReference(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "en-US",
		Reference: Reference{
			ScriptID: "script-123",
			SceneID:  "scene-04",
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("expected valid command with reference, got: %v", err)
	}
}

func TestCommand_WithDestination(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "en-US",
		Destination: DestinationRef{
			FolderID: "GOOGLE_DRIVE_FOLDER_ID",
		},
	}
	if err := cmd.Validate(); err != nil {
		t.Fatalf("expected valid command with destination, got: %v", err)
	}
}

// ── Deterministic ID ────────────────────────────────────────────────────

func TestBuildID_Deterministic(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:        "Hello, world!",
		Locale:      "en-US",
		Voice:       "en-US-RogerNeural",
		Destination: DestinationRef{FolderID: "folder_123"},
	}

	id1 := BuildID(cmd)
	id2 := BuildID(cmd)

	if id1 != id2 {
		t.Errorf("BuildID must be deterministic: %q != %q", id1, id2)
	}
	if !strings.HasPrefix(id1, idPrefix) {
		t.Errorf("ID must start with %q, got %q", idPrefix, id1)
	}
	if len(id1) != len(idPrefix)+idLen {
		t.Errorf("ID length: want %d, got %d (%q)", len(idPrefix)+idLen, len(id1), id1)
	}
}

func TestBuildID_DifferentText_ProducesDifferentID(t *testing.T) {
	cmd1 := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US"}
	cmd2 := GenerateVoiceoverCommand{Text: "World", Locale: "en-US"}

	if BuildID(cmd1) == BuildID(cmd2) {
		t.Error("different text must produce different IDs")
	}
}

func TestBuildID_DifferentLocale_ProducesDifferentID(t *testing.T) {
	cmd1 := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US"}
	cmd2 := GenerateVoiceoverCommand{Text: "Hello", Locale: "it-IT"}

	if BuildID(cmd1) == BuildID(cmd2) {
		t.Error("different locale must produce different IDs")
	}
}

func TestBuildID_DifferentVoice_ProducesDifferentID(t *testing.T) {
	cmd1 := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US", Voice: "en-US-RogerNeural"}
	cmd2 := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US", Voice: "en-US-JennyNeural"}

	if BuildID(cmd1) == BuildID(cmd2) {
		t.Error("different voice must produce different IDs")
	}
}

func TestBuildID_DifferentDestination_ProducesDifferentID(t *testing.T) {
	cmd1 := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US", Destination: DestinationRef{FolderID: "folder_a"}}
	cmd2 := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US", Destination: DestinationRef{FolderID: "folder_b"}}

	if BuildID(cmd1) == BuildID(cmd2) {
		t.Error("different destination must produce different IDs")
	}
}

func TestBuildID_NoVoice_NoDestination_Deterministic(t *testing.T) {
	cmd := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US"}

	id1 := BuildID(cmd)
	id2 := BuildID(cmd)

	if id1 != id2 {
		t.Errorf("ID with no voice/dest must be deterministic: %q != %q", id1, id2)
	}
}

func TestBuildID_SameCommand_DifferentInstances_SameID(t *testing.T) {
	// Verify two structs with identical field values produce the same ID.
	cmd1 := GenerateVoiceoverCommand{
		Text:        "The quick brown fox",
		Locale:      "en-GB",
		Voice:       "en-GB-RyanNeural",
		Destination: DestinationRef{FolderID: "abc-123"},
	}
	cmd2 := cmd1 // copy

	if BuildID(cmd1) != BuildID(cmd2) {
		t.Error("struct copies must produce the same ID")
	}
}

// ── Deterministic filename ──────────────────────────────────────────────

func TestBuildFilename_Basic(t *testing.T) {
	cmd := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US"}
	id := BuildID(cmd)
	fn := BuildFilename(cmd, id)

	if !strings.HasPrefix(fn, idPrefix) {
		t.Errorf("filename must start with %q, got %q", idPrefix, fn)
	}
	if !strings.HasSuffix(fn, ".mp3") {
		t.Errorf("filename must end with .mp3, got %q", fn)
	}
	if !strings.Contains(fn, "en-us") {
		t.Errorf("filename must contain locale, got %q", fn)
	}
}

func TestBuildFilename_Deterministic(t *testing.T) {
	cmd := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US"}
	id := BuildID(cmd)

	fn1 := BuildFilename(cmd, id)
	fn2 := BuildFilename(cmd, id)

	if fn1 != fn2 {
		t.Errorf("filename must be deterministic: %q != %q", fn1, fn2)
	}
}

func TestBuildFilename_WithReference(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "en-US",
		Reference: Reference{
			ScriptID: "script-123",
			SceneID:  "scene-04",
		},
	}
	id := BuildID(cmd)
	fn := BuildFilename(cmd, id)

	if !strings.Contains(fn, "script-123") {
		t.Errorf("filename with reference must contain script ID, got %q", fn)
	}
	if !strings.Contains(fn, "scene-04") {
		t.Errorf("filename with reference must contain scene ID, got %q", fn)
	}
}

func TestBuildFilename_NoPathSeparators(t *testing.T) {
	cmd := GenerateVoiceoverCommand{Text: "Hello", Locale: "en-US"}
	id := BuildID(cmd)
	fn := BuildFilename(cmd, id)

	if strings.ContainsAny(fn, "/\\") {
		t.Errorf("filename must not contain path separators, got %q", fn)
	}
}

func TestBuildFilename_WithReference_ScriptIDWithSpecialChars(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Hello",
		Locale: "en-US",
		Reference: Reference{
			ScriptID: "script/../etc/passwd",
			SceneID:  "scene*04?",
		},
	}
	id := BuildID(cmd)
	fn := BuildFilename(cmd, id)

	if strings.ContainsAny(fn, "/\\") {
		t.Errorf("filename must be path-traversal safe, got %q", fn)
	}
	if strings.ContainsAny(fn, "?*") {
		t.Errorf("filename must not contain shell metacharacters, got %q", fn)
	}
}

// ── Text hash ───────────────────────────────────────────────────────────

func TestBuildTextHash_Deterministic(t *testing.T) {
	h1 := BuildTextHash("Hello")
	h2 := BuildTextHash("Hello")

	if h1 != h2 {
		t.Errorf("text hash must be deterministic: %q != %q", h1, h2)
	}
}

func TestBuildTextHash_DifferentText(t *testing.T) {
	h1 := BuildTextHash("Hello")
	h2 := BuildTextHash("World")

	if h1 == h2 {
		t.Error("different texts must produce different hashes")
	}
}

func TestBuildTextHash_WhitespaceNormalized(t *testing.T) {
	h1 := BuildTextHash("  Hello  ")
	h2 := BuildTextHash("Hello")

	if h1 != h2 {
		t.Errorf("text hash must be whitespace-normalized: %q != %q", h1, h2)
	}
}

func TestBuildTextHash_NotEmpty(t *testing.T) {
	h := BuildTextHash("Hello")
	if len(h) == 0 {
		t.Error("text hash must not be empty")
	}
}

// ── Locale normalization ────────────────────────────────────────────────

func TestLocale_Normalize(t *testing.T) {
	tests := []struct {
		input    Locale
		expected Locale
	}{
		{"en-US", "en-us"},
		{"EN-US", "en-us"},
		{"  it-IT  ", "it-it"},
		{"PT-br", "pt-br"},
		{"en", "en"},
		{"EN", "en"},
	}
	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			got := tt.input.Normalize()
			if got != tt.expected {
				t.Errorf("Normalize(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

// ── DestinationRef ──────────────────────────────────────────────────────

func TestDestinationRef_IsZero(t *testing.T) {
	tests := []struct {
		name string
		ref  DestinationRef
		zero bool
	}{
		{"empty", DestinationRef{}, true},
		{"whitespace only", DestinationRef{FolderID: "   "}, true},
		{"has value", DestinationRef{FolderID: "folder_123"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.IsZero(); got != tt.zero {
				t.Errorf("IsZero() = %v, want %v", got, tt.zero)
			}
		})
	}
}

// ── Reference ───────────────────────────────────────────────────────────

func TestReference_IsZero(t *testing.T) {
	tests := []struct {
		name string
		ref  Reference
		zero bool
	}{
		{"empty", Reference{}, true},
		{"script only", Reference{ScriptID: "s1"}, false},
		{"scene only", Reference{SceneID: "sc1"}, false},
		{"both", Reference{ScriptID: "s1", SceneID: "sc1"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.ref.IsZero(); got != tt.zero {
				t.Errorf("IsZero() = %v, want %v", got, tt.zero)
			}
		})
	}
}

// ── VoiceProfile ────────────────────────────────────────────────────────

func TestVoiceProfile_IsZero(t *testing.T) {
	empty := VoiceProfile{}
	if !empty.IsZero() {
		t.Error("empty VoiceProfile.IsZero() must be true")
	}
	filled := VoiceProfile{Locale: "en-US", VoiceName: "Roger", VoiceCode: "en-US-RogerNeural"}
	if filled.IsZero() {
		t.Error("filled VoiceProfile.IsZero() must be false")
	}
}

// ── Full round-trip: command → ID → filename ───────────────────────────

func TestCommand_RoundTrip_Deterministic(t *testing.T) {
	cmd := GenerateVoiceoverCommand{
		Text:   "Welcome to the show!",
		Locale: "en-US",
		Voice:  "en-US-RogerNeural",
		Destination: DestinationRef{
			FolderID: "GOOGLE_DRIVE_FOLDER_X",
		},
		Reference: Reference{
			ScriptID: "script-456",
			SceneID:  "scene-01",
		},
	}

	if err := cmd.Validate(); err != nil {
		t.Fatal(err)
	}

	id := BuildID(cmd)
	fn := BuildFilename(cmd, id)

	// Repeat — same command, same ID, same filename.
	id2 := BuildID(cmd)
	fn2 := BuildFilename(cmd, id2)

	if id != id2 {
		t.Errorf("ID: %q != %q", id, id2)
	}
	if fn != fn2 {
		t.Errorf("filename: %q != %q", fn, fn2)
	}

	// Basic sanity: ID is not empty, filename ends with .mp3.
	if id == "" {
		t.Error("ID must not be empty")
	}
	if !strings.HasSuffix(fn, ".mp3") {
		t.Errorf("filename must end with .mp3, got %q", fn)
	}
}

// Package voiceover — filename.go defines the deterministic ID and filename
// computation for voiceover artifacts.
//
// PR 1 (June 2026): the server owns naming — the client never supplies a
// filename. Collision-resistant, path-traversal-safe, and deterministic:
// the same command always produces the same ID and filename.
//
// ID formula:   "vo_" + SHA256(text + "\x00" + locale + "\x00" + voice + "\x00" + destination)[:16]
// File formula: "vo_" + ID[:12] + "_" + locale + ".mp3"
// Scene-aware:  "vo_" + scriptID + "_" + sceneID + "_" + locale + "_" + ID[:8] + ".mp3"
package voiceover

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// idPrefix is the common prefix for all voiceover asset IDs.
	idPrefix = "vo_"

	// idLen is the number of hex characters taken from the SHA-256 hash.
	idLen = 16
)

// BuildID computes the deterministic voiceover asset ID from the command.
//
//	ID = "vo_" + SHA256(text + "\x00" + locale + "\x00" + voice + "\x00" + destination)[:16]
//
// The NUL separator prevents collisions between e.g. ("ab", "c") and ("a", "bc").
// If voice is empty, it is omitted from the hash input (so a command without an
// explicit voice matches the default-voice result).
func BuildID(cmd GenerateVoiceoverCommand) string {
	locale := string(cmd.Locale.Normalize())
	input := buildHashInput(cmd.Text, locale, cmd.Voice, cmd.Destination.String())
	hash := sha256hex(input)
	return idPrefix + hash[:idLen]
}

// BuildFilename computes the deterministic output filename.
//
//	Without reference:  "vo_" + ID[:12] + "_" + locale + ".mp3"
//	With reference:     "vo_" + scriptID + "_" + sceneID + "_" + locale + "_" + ID[:8] + ".mp3"
//
// The filename is safe for filesystem use: lowercase ASCII, no path separators,
// no shell metacharacters.
func BuildFilename(cmd GenerateVoiceoverCommand, id string) string {
	locale := string(cmd.Locale.Normalize())
	if !cmd.Reference.IsZero() {
		scriptSlug := safeFilenamePart(cmd.Reference.ScriptID)
		sceneSlug := safeFilenamePart(cmd.Reference.SceneID)
	shortID := strings.TrimPrefix(id, idPrefix)
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	return fmt.Sprintf("%s%s_%s_%s_%s.mp3", idPrefix, scriptSlug, sceneSlug, locale, shortID)
	}
	shortID := strings.TrimPrefix(id, idPrefix)
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	return fmt.Sprintf("%s%s_%s.mp3", idPrefix, shortID, locale)
}

// BuildTextHash computes the SHA-256 hash of the input text for dedup.
func BuildTextHash(text string) string {
	return sha256hex(strings.TrimSpace(text))
}

// ── internal helpers ────────────────────────────────────────────────────

// buildHashInput joins the command fields with NUL separators.
func buildHashInput(text, locale, voice, destination string) string {
	parts := []string{strings.TrimSpace(text), locale}
	if voice != "" {
		parts = append(parts, voice)
	}
	if destination != "" {
		parts = append(parts, destination)
	}
	return strings.Join(parts, "\x00")
}

// sha256hex returns the lowercase hex digest of the SHA-256 hash of input.
func sha256hex(input string) string {
	h := sha256.New()
	h.Write([]byte(input))
	return hex.EncodeToString(h.Sum(nil))
}

// safeFilenamePart replaces characters unsafe for filenames with underscores.
func safeFilenamePart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	result := b.String()
	// Prevent empty result (all-special-characters input).
	if result == "" {
		return "x"
	}
	// Prevent starting with '-' (looks like a flag in some tools).
	if result[0] == '-' {
		result = "_" + result[1:]
	}
	return result
}

package voiceover

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	hashutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// FilenameSpec is the canonical input for BuildVoiceoverFilename. It
// captures the minimum data the canonical token substitution needs:
// source text (sluggified), the BCP-47 language, an optional per-batch
// text hash, an optional user template, and an optional Now (zero →
// time.Now() at call). Callers MUST pre-validate Text and Language
// non-empty per the higher-layer Validate() gates; the error return
// on BuildVoiceoverFilename exists for the forward-stability of the
// public surface + tests that drive edge cases outside the validated
// path.
type FilenameSpec struct {
	// Text is the source text (sluggified via textutil.SlugifyWithMax
	// under the {slug} token; max length 30).
	Text string

	// Language is the BCP-47 language code (inserted under the {lang}
	// token; non-empty by Validate() contract). Typed (Language)
	// per PR-VO-TYPED-PRIMITIVES.
	Language Language

	// TextHash is the text fingerprint inserted under the {hash} token,
	// first 8 chars; if shorter than 8 the empty string is inserted —
	// no panic. Raw string (NOT the typed TextHash envelope) because
	// the filename-generation path consumes the canonical 64-char
	// SHA-256 fingerprint and the "first 8 chars" truncation works
	// byte-identically. PR-VO-TEXTHASH-64.
	TextHash string

	// Template is the optional user template (with {slug}/{lang}/{hash}/
	// {time} tokens). Empty string falls back to "{slug}_{lang}.mp3"
	// so the canonical default grammar always applies.
	Template string

	// Now is the optional clock for tests + reproducibility. Zero
	// value → time.Now() at call. Tests should pin Now via a known
	// epoch to assert exact filename strings.
	Now time.Time
}

// BuildVoiceoverFilename returns the canonical voiceover filename per
// the standard {slug}/{lang}/{hash}/{time} token grammar:
//
//	{slug} -> textutil.SlugifyWithMax(Text, 30)
//	{lang} -> Language (verbatim)
//	{hash} -> first 8 chars of TextHash (empty string if shorter)
//	{time} -> Now.Format("150405") (HHMMSS in 24h format)
//
// Returns an error if Text or Language is empty — the higher-layer
// Validate() gates (GenerateVoiceoversCommand.Validate,
// GenerateVoiceoverItemCommand.Validate) already enforce non-empty
// Text + Language, so the error path is unreachable in production.
// The return type exists for forward-stability of the canonical
// surface and for tests that probe edge cases outside the validated
// path.
//
// E4 (June 2026): replaces the three duplicate helpers
// (Service.buildFilename, GenerateVoiceoversUseCase.buildCommandFilenameForItem,
// jobs.buildItemFilename+jobs.slug) with one canonical implementation.
// The {slug}/{lang}/{hash}/{time} token grammar is invariant across
// all call sites — the slug helper is textutil.SlugifyWithMax (the
// canonical pillper, also used elsewhere in the voiceover package);
// the local pkg-imports-free slug() implementation in jobs/fanout.go
// is absorbed because pkg imports here are 1-line and deterministic.
func BuildVoiceoverFilename(spec FilenameSpec) (string, error) {
	if strings.TrimSpace(spec.Text) == "" {
		return "", fmt.Errorf("BuildVoiceoverFilename: Text must be non-empty")
	}
	if strings.TrimSpace(string(spec.Language)) == "" {
		return "", fmt.Errorf("BuildVoiceoverFilename: Language must be non-empty")
	}

	template := spec.Template
	if template == "" {
		template = "{slug}_{lang}.mp3"
	}

	slug := textutil.SlugifyWithMax(spec.Text, 30)
	filename := strings.ReplaceAll(template, "{slug}", slug)
	// PR-VO-TYPED-PRIMITIVES (July 2026): spec.Language is the typed
	// Language envelope; strings.ReplaceAll requires string args.
	filename = strings.ReplaceAll(filename, "{lang}", string(spec.Language))

	hashPrefix := spec.TextHash
	if len(hashPrefix) > 8 {
		hashPrefix = hashPrefix[:8]
	}
	filename = strings.ReplaceAll(filename, "{hash}", hashPrefix)

	now := spec.Now
	if now.IsZero() {
		now = time.Now()
	}
	filename = strings.ReplaceAll(filename, "{time}", now.Format("150405"))

	return filename, nil
}

// buildVoiceoverID hashes (textHash + language + folderID) and returns
// the canonical "vo_<sha256[:16]>" row identifier.
//
// PR-VO-TEXTHASH-64 (August 2026): both call paths now pass the
// full 64-char SHA-256 fingerprint (ComputeTextHash), so the inner
// SHA256 is stable for the same text regardless of call path.
func buildVoiceoverID(textHash string, language Language, folderID string) string {
	data := fmt.Sprintf("%s:%s:%s", textHash, language, folderID)
	return "vo_" + hashutil.SHA256Bytes([]byte(data))[:16]
}

// SanitizeBasename validates and sanitizes a voiceover basename.
// Does NOT add an extension — callers should append .mp3 themselves.
// Rejects path separators (path traversal).
func SanitizeBasename(name string) (string, error) {
	if strings.ContainsAny(name, "/\\") {
		return "", fmt.Errorf("invalid filename: path traversal detected")
	}
	return filepath.Base(textutil.SanitizeFilename(name)), nil
}

// SanitizeFilename validates a filename against path traversal, adds
// .mp3 if missing, and returns a safe output path rooted at outputDir.
func SanitizeFilename(outputDir, filename string) (string, error) {
	if filepath.Ext(filename) == "" {
		filename += ".mp3"
	}

	// Prevent path traversal: reject if filename contains path separators
	if strings.ContainsAny(filename, "/\\") {
		return "", fmt.Errorf("invalid filename: path traversal detected")
	}

	// Sanitize the filename portion
	cleanName := textutil.SanitizeFilename(filename)
	cleanName = filepath.Base(cleanName)

	finalPath := filepath.Join(outputDir, cleanName)

	// If outputDir is set, verify the final path is inside outputDir
	if outputDir != "" {
		if !strings.HasPrefix(finalPath, outputDir+string(filepath.Separator)) && finalPath != outputDir {
			return "", fmt.Errorf("invalid filename: path traversal detected")
		}
	}

	return finalPath, nil
}

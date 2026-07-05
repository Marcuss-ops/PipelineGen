// Package voiceover — filename_builder.go (BLOC5.3 commit-2-child-canonical, June 2026).
//
// DefaultFilenameBuilder is the canonical production concrete for the
// FilenameBuilder narrow port declared in ports.go (BLOC4_ssot_cutover
// micro-commit #6). The port is held by ProcessVoiceoverItemUseCase
// (process_voiceover_item.go) so the per-item caller has a stable
// FilenameBuilder surface for future BACKFILL stages (idempotency,
// namespace derivation). The BLOC5.3 audit pin "Trust item.Filename from
// fanout" (P0.6) means Execute does NOT call BuildFilename — the
// fanout pre-computes the filename at scheduling time and the child
// passes it through. The port is wired for portability, not for the
// current per-item Execute path.
//
// Filename grammar (mirrors legacy Service.buildFilename at
// filename.go + usecase.go::buildCommandFilename):
//
//   - {slug} → textutil.SlugifyWithMax(text, 30)
//   - {lang} → language (verbatim)
//   - {hash} → textHash first 8 chars (or "" when shorter)
//   - {time} → time.Now().Format("150405")
//   - default template (when empty) → "{slug}_{lang}.mp3"
package voiceover

import (
	"strings"
	"time"

	textutil "github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// DefaultFilenameBuilder is the canonical FilenameBuilder concrete.
// Stateless — a nil-safe factory returns one shared instance per call
// (callers don't compare identity). Never constructed via zero-value
// (always via NewDefaultFilenameBuilder so the interface return type
// is non-nil at the composition root).
type DefaultFilenameBuilder struct{}

// NewDefaultFilenameBuilder returns the canonical FilenameBuilder
// implementation. Per AGENTS.md Pattern 0, the port interface is the
// returned type so a future swap (e.g. a slugify-with-collision-suffix
// variant) only changes the constructor return, not the call sites.
func NewDefaultFilenameBuilder() FilenameBuilder {
	return &DefaultFilenameBuilder{}
}

// BuildFilename substitutes the {slug}/{lang}/{hash}/{time} tokens in
// the supplied template (or the default "{slug}_{lang}.mp3" when
// template is empty) and returns the composed filename.
//
// Inputs:
//   - text:     source text (used for slug derivation).
//   - language: BCP-47 lowercase code (verbatim into {lang}).
//   - textHash: stable per-batch SHA-256 first-16-hex prefix (used
//     via {hash} first 8 chars); may be empty.
//   - template: optional FanoutVoiceoverUseCase.FilenameTemplate
//     style template. Empty → default.
func (b *DefaultFilenameBuilder) BuildFilename(text, language, textHash, template string) string {
	if template == "" {
		template = "{slug}_{lang}.mp3"
	}
	slug := textutil.SlugifyWithMax(text, 30)
	hashPrefix := textHash
	if len(hashPrefix) > 8 {
		hashPrefix = hashPrefix[:8]
	}
	filename := strings.ReplaceAll(template, "{slug}", slug)
	filename = strings.ReplaceAll(filename, "{lang}", language)
	filename = strings.ReplaceAll(filename, "{hash}", hashPrefix)
	filename = strings.ReplaceAll(filename, "{time}", time.Now().Format("150405"))
	return filename
}

// Compile-time assertion (AGENTS.md Pattern 0): *DefaultFilenameBuilder
// must structurally satisfy the FilenameBuilder port declared in
// ports.go. Drift between BuildFilename's signature and the port
// contract triggers a compile error here.
var _ FilenameBuilder = (*DefaultFilenameBuilder)(nil)

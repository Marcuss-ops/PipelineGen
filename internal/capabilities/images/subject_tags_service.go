// Package images — subject_tags_service.go is the canonical concrete
// implementation of SubjectTagsService (port in ports.go).
//
// PR C9 of PR-IMAGES-AI-VS-NORMAL-PLAN (July 2026): replaces the
// silent-fake stub `extractSubjectAndTags` (which returned `("", nil)`
// for any description) with a real, typed-error port + concrete.
//
// Subject derivation strategy:
//  1. Extract capitalized words from description (termutil.ExtractLikelyNames).
//  2. Slugify the FIRST capitalized word as the canonical subject.
//     First-position preserves the most-likely entity ordering
//     (e.g. "Einstein's Theory of Relativity" → "einstein").
//  3. Extract full term list (termutil.TermsFromText with default opts).
//  4. Filter out the subject slug from the tag list (de-dup with subject).
//
// Failure modes (godlike/07 typed-error contract):
//   - Empty / whitespace-only description → ErrEmptyDescription
//   - Description with no capitalized words → ErrNoSubjectDerivable
//
// Composition-root injection: the concrete is wired inline in
// service.go::NewService because it has zero external dependencies
// (leaf over pkg/termutil + pkg/textutil only). If a future caller
// needs caching or telemetry, swap in a decorator implementing
// the same SubjectTagsService interface.
package images

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/linguistics"
	"github.com/Marcuss-ops/PipelineGen/pkg/termutil"
	"github.com/Marcuss-ops/PipelineGen/pkg/textutil"
)

// DefaultSubjectTagsService is the canonical concrete SubjectTagsService.
// Zero external dependencies (leaf over pkg/termutil + pkg/textutil).
type DefaultSubjectTagsService struct{}

// NewDefaultSubjectTagsService returns a fresh instance. Construction
// is allocation-only; safe to construct inline at composition root.
func NewDefaultSubjectTagsService() *DefaultSubjectTagsService {
	return &DefaultSubjectTagsService{}
}

// Compile-time assertion: the concrete satisfies the canonical port.
// Drift here (signature change, method removal) breaks the build
// immediately rather than panicking on first call.
var _ SubjectTagsService = (*DefaultSubjectTagsService)(nil)

// ExtractSubjectAndTags implements the SubjectTagsService port.
// See type doc in ports.go for full contract.
func (s *DefaultSubjectTagsService) ExtractSubjectAndTags(_ context.Context, description string) (string, []string, error) {
	if textutil.Slugify(description) == "" {
		return "", nil, ErrEmptyDescription
	}

	names := termutil.ExtractLikelyNames(description)
	if len(names) == 0 {
		return "", nil, ErrNoSubjectDerivable
	}

	subject := textutil.Slugify(names[0])
	if subject == "" {
		// Defensive: ExtractLikelyNames guarantees non-empty capitalized
		// words >2 chars, but Slugify could still collapse exotic unicode.
		// Treat as "no subject" rather than empty-slug ambiguous.
		return "", nil, ErrNoSubjectDerivable
	}

	// Tag extraction with default TermOptions (MinLen=3, lowercase,
	// remove-stops, unique). Mirrors the action plan C9 spec.
	rawTags := termutil.TermsFromText(description, termutil.TermOptions{
		RemoveStops: true,
		StopWords:   linguistics.DefaultStopWords(),
	})

	// De-dup subject slug from the tag list. Slugify each candidate
	// to normalize before comparison (termutil already lowercased).
	tags := make([]string, 0, len(rawTags))
	for _, t := range rawTags {
		if textutil.Slugify(t) == subject {
			continue
		}
		tags = append(tags, t)
	}

	return subject, tags, nil
}

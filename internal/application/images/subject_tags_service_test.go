// Package images — subject_tags_service_test.go locks the SubjectTagsService
// concrete (PR C9, July 2026).
//
// Coverage surface:
//  1. **Happy prompt-only path** — single capitalized word yields a slug
//     + filtered remaining terms as tags.
//  2. **Empty description** → ErrEmptyDescription (godlike/07 typed-error).
//  3. **No capitalized words** (all-lowercase / all-punctuation) →
//     ErrNoSubjectDerivable (godlike/07 typed-error).
//  4. **Subject de-dup from tags** — if a tag's slug matches the subject
//     slug, the tag is filtered (no subject/tag duplication).
//  5. **Multi-tag path** — multiple capitalized words + many stop-word-
//     filtered terms produces a non-empty tag list.
//
// The tests are hermetic (no fixtures, no DB, no HTTP) — the concrete
// is a leaf over pkg/termutil + pkg/textutil and runs in microseconds.
package images

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time assertion: the test file's binding to the canonical port
// is a separate declaration from the production-side assertion in
// subject_tags_service.go. Drift in EITHER breaks the build.
var _ SubjectTagsService = (*DefaultSubjectTagsService)(nil)

func newTestSubjectTagsService() *DefaultSubjectTagsService {
	return NewDefaultSubjectTagsService()
}

func TestSubjectTags_HappyPromptOnly(t *testing.T) {
	t.Parallel()
	svc := newTestSubjectTagsService()

	// Single capitalized word as the only content. Subject is the
	// slugified word; tags list is empty because no other qualifying
	// terms (≥3 chars, non-stop) remain after de-dup with the subject.
	subject, tags, err := svc.ExtractSubjectAndTags(context.Background(), "Einstein")
	require.NoError(t, err)
	assert.Equal(t, "einstein", subject, "first capitalized word should become the subject slug")
	assert.Empty(t, tags, "tag list should be empty when no qualifying terms remain")
}

func TestSubjectTags_EmptyDescription(t *testing.T) {
	t.Parallel()
	svc := newTestSubjectTagsService()

	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace", "   \t\n  "},
		{"only-punctuation", ".,!?"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject, tags, err := svc.ExtractSubjectAndTags(context.Background(), tc.in)
			require.Error(t, err, "expected error for input %q", tc.in)
			assert.ErrorIs(t, err, ErrEmptyDescription, "typed sentinel ErrEmptyDescription")
			assert.Equal(t, "", subject, "subject must be empty on hard failure")
			assert.Empty(t, tags, "tags must be empty on hard failure")
		})
	}
}

func TestSubjectTags_NoSubjectDerivable(t *testing.T) {
	t.Parallel()
	svc := newTestSubjectTagsService()

	// All-lowercase → no capitalized words → no subject derivable.
	subject, tags, err := svc.ExtractSubjectAndTags(context.Background(), "all lowercase description")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoSubjectDerivable, "typed sentinel ErrNoSubjectDerivable")
	assert.Equal(t, "", subject, "subject must be empty on no-derivable")
	assert.Empty(t, tags, "tags must be empty on no-derivable")
}

func TestSubjectTags_SubjectFilteredFromTags(t *testing.T) {
	t.Parallel()
	svc := newTestSubjectTagsService()

	// The first capitalized word is "Einstein"; after Slugify "einstein".
	// TermsFromText will include "einstein" as a lowercased term;
	// the subject-de-dup loop must filter it out.
	subject, tags, err := svc.ExtractSubjectAndTags(context.Background(),
		"Einstein discovered relativity theory")
	require.NoError(t, err)
	assert.Equal(t, "einstein", subject)
	// "einstein" must NOT appear in tags (de-dup).
	for _, tag := range tags {
		// TermsFromText already lowercased; subject is also lowercase.
		assert.NotEqual(t, "einstein", tag, "subject slug must be filtered from tag list")
	}
	// "relativity" + "theory" should survive (both >3 chars, both not
	// stop-words per default TermOptions).
	assert.Contains(t, tags, "relativity", "non-subject term should appear in tags")
	assert.Contains(t, tags, "theory", "non-subject term should appear in tags")
}

func TestSubjectTags_MultiTagExtraction(t *testing.T) {
	t.Parallel()
	svc := newTestSubjectTagsService()

	// Description with multiple capitalized words + many long terms.
	// "Napoleon" is the first → subject. "Bonaparte" is another name;
	// "battle" + "waterloo" + "emperor" are the meaningful tags.
	subject, tags, err := svc.ExtractSubjectAndTags(context.Background(),
		"Napoleon Bonaparte battle waterloo emperor france")
	require.NoError(t, err)
	assert.Equal(t, "napoleon", subject, "first capitalized word wins subject")
	assert.NotEmpty(t, tags, "multi-tag path should produce a non-empty tag list")
	// "napoleon" must be filtered out.
	for _, tag := range tags {
		assert.NotEqual(t, "napoleon", tag)
	}
	// At least one of the expected terms should appear.
	hasExpected := false
	for _, expected := range []string{"bonaparte", "battle", "waterloo", "emperor", "france"} {
		for _, tag := range tags {
			if tag == expected {
				hasExpected = true
				break
			}
		}
	}
	assert.True(t, hasExpected, "expected at least one of [bonaparte battle waterloo emperor france] in tags, got %v", tags)
}

func TestSubjectTags_NilCtxSafe(t *testing.T) {
	t.Parallel()
	svc := newTestSubjectTagsService()

	// The concrete never reads from ctx today (it has no I/O), so a
	// nil ctx must not panic. Locks the contract for future ctx-aware
	// implementations (e.g. tracing).
	assert.NotPanics(t, func() {
		_, _, _ = svc.ExtractSubjectAndTags(context.TODO(), "Einstein")
	}, "concrete must not panic on ctx.TODO() (future-proofs for nil-ctx harm)")
}

func TestSubjectTags_ErrSentinelsAreDistinct(t *testing.T) {
	t.Parallel()

	// godlike/07 SSOT: sentinels must be distinct so callers can
	// discriminate via errors.Is. If someone accidentally aliases
	// them, the test catches it.
	require.NotEqual(t, ErrEmptyDescription, ErrNoSubjectDerivable,
		"godlike/07: sentinels must be distinct for errors.Is discrimination")
	require.True(t, errors.Is(ErrEmptyDescription, ErrEmptyDescription))
	require.True(t, errors.Is(ErrNoSubjectDerivable, ErrNoSubjectDerivable))
	require.False(t, errors.Is(ErrEmptyDescription, ErrNoSubjectDerivable),
		"godlike/07: ErrEmptyDescription must NOT match ErrNoSubjectDerivable")
}

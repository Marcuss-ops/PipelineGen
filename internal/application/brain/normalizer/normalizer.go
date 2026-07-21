// Package normalizer is the canonical home of the phrase
// normalization surface for the Brain capability.
//
// godlike/06 SSOT: two surface-equivalent phrases MUST produce the
// same fingerprint. The canonical algorithm lives here and nowhere
// else. No package in the brain imports Qdrant, SQLite, Drive,
// FFmpeg, or any other nervous-system adapter.
package normalizer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// Version is the canonical version stamp baked into the
// fingerprint. Bump it whenever the normalization algorithm or the
// visual-intent schema changes so old fingerprints are not reused
// across incompatible logic.
const Version = "v1"

// Result is the canonical output of a phrase normalization.
type Result struct {
	Original    string
	Normalized  string
	Fingerprint string
	Version     string
}

// PhraseNormalizer is the canonical port that turns a raw phrase
// into its normalized form and a deterministic fingerprint.
type PhraseNormalizer interface {
	Normalize(ctx context.Context, language, text string) (Result, error)
}

// defaultNormalizer is the canonical pure implementation. It performs
// no IO and has no external dependencies.
type defaultNormalizer struct {
	version string
}

// NewDefaultNormalizer returns the canonical phrase normalizer.
func NewDefaultNormalizer() PhraseNormalizer {
	return &defaultNormalizer{version: Version}
}

// Compile-time assertion: defaultNormalizer satisfies PhraseNormalizer.
var _ PhraseNormalizer = (*defaultNormalizer)(nil)

// Normalize applies the canonical normalization pipeline:
//
//  1. trim surrounding whitespace;
//  2. canonical Unicode NFC;
//  3. lowercase fold (locale-invariant, rune-by-rune);
//  4. collapse runs of whitespace to a single ASCII space;
//  5. strip trailing punctuation so "Maya." matches "Maya";
//  6. compute SHA256(language:normalized:version) fingerprint.
func (n *defaultNormalizer) Normalize(_ context.Context, language, text string) (Result, error) {
	language = strings.TrimSpace(strings.ToLower(language))
	text = strings.TrimSpace(text)

	if text == "" {
		return Result{}, errors.New("normalizer: empty phrase")
	}
	if language == "" {
		return Result{}, errors.New("normalizer: language is required")
	}

	normalized := canonicalize(text)

	return Result{
		Original:    text,
		Normalized:  normalized,
		Fingerprint: n.fingerprint(language, normalized),
		Version:     n.version,
	}, nil
}

func (n *defaultNormalizer) fingerprint(language, normalized string) string {
	sum := sha256.Sum256([]byte(language + ":" + normalized + ":" + n.version))
	return hex.EncodeToString(sum[:])
}

func canonicalize(s string) string {
	s = norm.NFC.String(s)
	s = strings.Map(lowerRune, s)
	s = collapseWhitespace(s)
	s = stripTrailingPunctuation(s)
	return s
}

func lowerRune(r rune) rune {
	if unicode.IsUpper(r) {
		return unicode.To(unicode.LowerCase, r)
	}
	return r
}

func collapseWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !lastSpace {
				b.WriteRune(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return b.String()
}

func stripTrailingPunctuation(s string) string {
	for len(s) > 0 {
		r := rune(s[len(s)-1])
		if unicode.IsPunct(r) {
			s = s[:len(s)-1]
			continue
		}
		return s
	}
	return s
}

package script

import (
	"regexp"
	"strings"
)

// SanitizeForSpeech turns authored Markdown-ish narration into the plain text a
// speech provider must read. It is deliberately pure: the same function runs
// before the TTS text hash is computed and again at the provider boundary, so
// the cached identity and the synthesized words cannot diverge.
//
// The contract covers the syntax that reaches narration in practice:
// emphasis (**bold**, *italic*, __bold__, _italic_), inline code and fenced
// code, ATX headings, Markdown links/images, blockquotes, bullet markers and
// horizontal rules. Inline punctuation is preserved; only formatting tokens are
// removed. Whitespace is collapsed to one line because a TTS provider must not
// read a Markdown line break as a pause.
var (
	markdownFenceRE      = regexp.MustCompile("(?s)```.*?```")
	speechMarkdownLinkRE = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	markdownHTMLTagRE    = regexp.MustCompile(`<[^>]+>`)
	markdownHeadingRE    = regexp.MustCompile(`(?m)^\s*#{1,6}\s*`)
	markdownBulletRE     = regexp.MustCompile(`(?m)^\s*[*+\-]\s+`)
	markdownBlockquoteRE = regexp.MustCompile(`(?m)^\s*>\s?`)
	markdownRuleRE       = regexp.MustCompile(`(?m)^\s*(?:(?:-\s*){3,}|(?:\*\s*){3,}|(?:_\s*){3,})$`)
	markdownEmphasisRE   = regexp.MustCompile(`\*{1,3}|_{1,3}|~{1,2}`)
	markdownCodeTickRE   = regexp.MustCompile("`+")
	whitespaceRE         = regexp.MustCompile(`\s+`)
)

// SanitizeForSpeech returns the spoken form of text. It never returns
// Markdown-only residue: a string that was entirely formatting becomes empty,
// which lets callers skip the TTS item instead of synthesizing punctuation.
func SanitizeForSpeech(text string) string {
	text = markdownFenceRE.ReplaceAllString(text, " ")
	text = speechMarkdownLinkRE.ReplaceAllString(text, "$1")
	// Inline formatting is removed before line-prefix markers: a
	// formatting-only line can leave a heading/bullet marker behind, and
	// an HTML tag must not inject a space before punctuation.
	text = markdownHTMLTagRE.ReplaceAllString(text, "")
	text = markdownCodeTickRE.ReplaceAllString(text, "")
	text = markdownEmphasisRE.ReplaceAllString(text, "")
	text = markdownHeadingRE.ReplaceAllString(text, "")
	text = markdownBulletRE.ReplaceAllString(text, "")
	text = markdownBlockquoteRE.ReplaceAllString(text, "")
	text = markdownRuleRE.ReplaceAllString(text, " ")
	text = strings.ReplaceAll(text, "\\", "")
	text = whitespaceRE.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

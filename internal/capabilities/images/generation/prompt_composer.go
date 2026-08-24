// Package images — prompt_composer.go: controlled composition of the
// single-side prompt sent to the worker (Prompt + Style + Negative) so
// the worker does NOT have to do its own heuristic-driven truncation.
//
// P1.2 (July 2026): the empirical "Google Slides rejects prompts longer
// than ~150 chars" observation (pre-FASE 7) is RETIRED. The heuristic
// was: split on first period, then truncate to 147 + "…" — which
// destroyed semantic intent (a multi-clause scenario prompt like
// "a vintage airport runway at night. dim runway beacons. an
// approaching 747" got cut to "a vintage airport runway at night").
//
// New policy (P1.2, July 2026): the FULL prompt arrives from Go to the
// worker, already composed as
//
//	{prompt} [style: {style}] [negative: do not include {negatives}]
//
// If Google's API rejects a long prompt with a real error code, the
// typed retry policy (chrome_provider.Generate — ClassifyError,
// ErrImageGenPolicy / ErrImageGenTimeout / ErrImageGenPermanent)
// catches it STRUCTURALLY. That is strictly preferable to silent
// truncation because the operator sees the actual rejection (and can
// shorten the prompt deliberately) rather than having the worker
// silently destroy intent.
//
// Composition rules:
//   - empty style    → omit ` [style: ...]`
//   - empty negative → omit ` [negative: ...]`
//   - multi-word negatives: `,` → `;` to avoid English-punctuation ambiguity
//   - WasCompressed is HARDCODED to false (P1.2 policy: we do NOT
//     compress). Forward-pointer: a future push may fire compression
//     if a model-aware compressor lands. Today's contract is
//     "send whole; let the API reject loudly".
//
// Forward-pointer (P1.2): if the empirical Google rejection observation
// returns at a different threshold, the compressor grows to a
// (threshold-aware, model-aware) TTL. Today's contract is sufficient
// because:
//
//	(a) the typed ErrImageGenPolicy/ErrImageGenTimeout classifier
//	    catches real rejections without any pre-emptive truncation;
//	(b) the worker no longer has any heuristic-driven prompt
//	    manipulation, so the wire-level round-trip is deterministic;
//	(c) the smoke test asserts the byte-equal round-trip (composed
//	    length = prompt length + affix length, no `…`, no truncation).
package generation

import "strings"

// ComposeResult contains the composed prompt and metadata about the
// composition. OriginalLen / ComposedLen are byte lengths (Go strings
// are UTF-8 byte slices; visible-char length and byte length match
// for the ASCII subset we care about). WasCompressed is HARDCODED to
// false for P1.2 — the Go-side compressor is a no-op by policy.
//
// StyleAffix / NegativeAffix are the exact suffix strings appended to
// the prompt, exposed for two reasons:
//
//  1. observability: an operator / smoke test can assert that the
//     affixes contain the user-supplied style + negative (no rewrite,
//     no normalisation beyond the documented `,` → `;` swap);
//  2. testing parity: a unit test can verify composed_len =
//     original_len + len(style_affix) + len(negative_affix) without
//     having to re-derive the affixes from non-canonical constants.
type ComposeResult struct {
	Composed      string
	WasCompressed bool // hardcoded to false (P1.2 policy).
	OriginalLen   int
	ComposedLen   int
	StyleAffix    string
	NegativeAffix string
}

// ComposePrompt builds a single presentation prompt from distinct fields.
//
// Format: `{prompt} [style: {style}] [negative: do not include {negatives}]`
//
// Rules:
//   - empty style    → omit ` [style: ...]` (StyleAffix is "")
//   - empty negative → omit ` [negative: ...]` (NegativeAffix is "")
//   - multi-word negatives: replace `,` with `;` so the prompt parser
//     doesn't confuse them with English punctuation
//   - never compresses (P1.2 policy; the empirical 150-char Google
//     rejection observation was retired — see the package doc above
//     and ports.go::ClassifyError for the typed-retry surface that
//     catches real API rejections)
//
// The result is deterministic: the output depends only on the inputs.
// This makes the smoke-test contract "long prompt arrives whole"
// trivially verifiable — a 400-char prompt + style + negative yields
// composed_len = 400 + len(style_affix) + len(negative_affix), no
// `…`, no truncation, no first-period split.
//
// godlike/07 no-fake-availability (P1.2): we deliberately do NOT
// silently truncate OR silently summarise. If the user's prompt + style
// + negative exceeds whatever soft limit the backend has, the API
// will reject it loudly; we surface that as a typed error.
//
// Forward-pointer: the compressor may grow into a (threshold-aware,
// model-aware) layer in a future push. Until then the contract is
// "send whole" — see the package doc for the threshold rationale.
func ComposePrompt(prompt, style, negativePrompt string) ComposeResult {
	r := ComposeResult{
		OriginalLen:   len(prompt),
		WasCompressed: false, // P1.2 policy: never compress.
	}

	// Use strings.Builder for a single allocation across the three
	// non-empty branches. Pre-size to a reasonable estimate so the
	// builder doesn't reallocate on the first append.
	b := strings.Builder{}
	b.Grow(len(prompt) + len(style) + len(negativePrompt) + 64)
	b.WriteString(prompt)

	if style != "" {
		styleAffix := " [style: " + style + "]"
		b.WriteString(styleAffix)
		r.StyleAffix = styleAffix
	}
	if negativePrompt != "" {
		// Defensive: tokenize ONLY on `,` and `;` (the canonical list
		// separators), then TrimSpace each part, then rejoin with `;`.
		// This gives the canonical whitespace-clean form for SEPARATED
		// lists while preserving a single multi-word keyword that
		// happens to contain a space (e.g. "low quality" stays as one
		// token, not split into "low;quality").
		//
		//   "text, watermark, blurry"  → split on `,` → ["text",
		//                                    " watermark", " blurry"]
		//                                  → trim → ["text", "watermark",
		//                                            "blurry"]
		//                                  → join → "text;watermark;blurry"
		//   "low quality"  (single multi-word keyword, no separator)
		//                                → split on `,|;` → ["low quality"]
		//                                  → trim → ["low quality"]
		//                                  → join → "low quality"
		//   "text;watermark;blurry"  → split on `,|;`
		//                                → ["text", "watermark", "blurry"]
		//                                → join → "text;watermark;blurry"
		//
		// The earlier FieldsFunc-with-unicode.IsSpace approach was
		// overzealous: it treated internal whitespace as a separator,
		// which would split "low quality" (a single keyword) into
		// "low;quality" — semantically wrong. The narrow
		// (`,`+`;`)-only split + per-token TrimSpace is the canonical
		// conservative transform the user spec requires
		// ("`,\u00a0rightarrow\u00a0;` to avoid ambiguity").
		negativeParts := strings.FieldsFunc(negativePrompt, func(r rune) bool {
			return r == ',' || r == ';'
		})
		for i, p := range negativeParts {
			negativeParts[i] = strings.TrimSpace(p)
		}
		normalized := strings.Join(negativeParts, ";")
		negativeAffix := " [negative: do not include " + normalized + "]"
		b.WriteString(negativeAffix)
		r.NegativeAffix = negativeAffix
	}

	r.Composed = b.String()
	r.ComposedLen = len(r.Composed)
	return r
}

package scriptgeneration

import (
	"net/url"
	"regexp"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

var explicitDatePattern = regexp.MustCompile(`(?i)\b(?:[0-9]{1,2}\s+(?:gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)\s+[0-9]{4}|[0-9]{4})\b`)
var groundedDateIdentityPattern = regexp.MustCompile(`(?i)^([0-9]{1,2})\s+(gennaio|febbraio|marzo|aprile|maggio|giugno|luglio|agosto|settembre|ottobre|novembre|dicembre)(?:\s+([0-9]{4}))?$`)

// dedupeOverlappingGroundedDates collapses partial mentions of one spoken
// date (for example "17 dicembre", "dicembre 2009" and "17 dicembre 2009")
// into the most complete source-grounded surface. The annotations must overlap
// in certified speech timing and share only date tokens, so separate dates in
// the same scene remain distinct.

func entityImageURL(binding *scriptpkg.EntityImageBinding) string {
	if binding == nil {
		return ""
	}
	// The durableDrive copy is the only location whose bytes are stable for
	// the content address. Provider URLs can return different bytes later,
	// which makes RenderingGen reject the prefetch with HTTP 422.
	if id := strings.TrimSpace(binding.DriveFileID); id != "" {
		return "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
	}
	if id := driveFileID(binding.DriveLink); id != "" {
		return "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(id)
	}
	if preview := strings.TrimSpace(binding.PreviewURL); preview != "" {
		return preview
	}
	return strings.TrimSpace(binding.DriveLink)
}

// PhraseAnchoringDiagnostic reports one important-phrase candidate considered
// by the overlay planner together with whether it anchored to the certified
// speech timing. It exists purely for observability: a phrase that fails to
// anchor is dropped silently, which makes "the phrases are not taken"
// impossible to diagnose from logs alone.

// normalizeOverlayText lowercases and collapses whitespace so a phrase span
// and a heavy sentence match regardless of casing or spacing.

// applyHeavySentenceBoost raises phrase candidate scores with the Rust
// phrase-impact worker's heavy-sentence importance (goal E2b). A phrase whose
// normalized text is contained in (or contains) a heavy sentence inherits
// max(span score, heavy importance), so the heavy summary directly pilots
// the prominent-motion lane behind PlanInput.HeavyPhrasePriority.
// Scores only rise, never fall: admission stays a restriction, never a
// widening. Matching is source-language text; translated overlays keep their
// annotation scores when nothing matches.

// Package scan — percheck_slot_strings_ban.go
//
// Forward-prevention gate that BANS the use of canonical slot
// string literals outside the canonical SSOT file
// internal/kernel/media/slot.go.
//
// Distinctive slot names ("primary_video", "secondary_image",
// "evidence_overlay") are forbidden in production code anywhere
// outside slot.go. Generic slot names ("map", "portrait",
// "document", "background") are only forbidden when the same line
// also contains a slot-related identifier, because the same words
// are overloaded (e.g. "document" as a media type/destination,
// "portrait" as image orientation, "map" as the Go keyword).
//
// Slot kinds MUST be referenced via the typed constants in the
// domain package (media.SlotPrimaryVideo, media.SlotSecondaryImage,
// etc.) so the closed set can evolve in one place.
//
// Exempt zones:
//   - internal/kernel/media/slot.go — the canonical SSOT owner.
//   - **/*_test.go — regression-guard surface.
//   - cmd/archcheck/scan/ — the scanner's own source code.
//
// Matched rule_id: percheck_slot_strings_ban
package governance

import (
	"regexp"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/scan/textban"
)

// slotStringsSSOTRe matches the distinctive canonical slot string literals
// outside the SSOT file. The generic slot names (map, portrait, document,
// background) are also checked, but only when the same line contains a
// slot-related identifier — this avoids flagging overloaded words such as
// "document" (media type / destination key) or "portrait" (image
// orientation) outside of a slot-kind context.
var slotStringsDistinctRe = regexp.MustCompile(`"(primary_video|secondary_image|evidence_overlay)"`)
var slotStringsGenericRe = regexp.MustCompile(`"(map|portrait|document|background)"`)
var slotStringsContextRe = regexp.MustCompile(`(?i)slot`)

const slotStringsSSOTRule = "percheck_slot_strings_ban"

const slotStringsSSOTNote = "forbidden hardcoded slot string literal outside the canonical SSOT file. Slot kinds MUST be referenced via the typed constants in internal/kernel/media/slot.go (e.g. media.SlotPrimaryVideo, media.SlotSecondaryImage). Hardcoded slot strings are a godlike/06 SSOT violation."

var slotStringsSSOTSkipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	"node-scraper": true,
	"examples":     true,
	"archivist":    true,
	"docs":         true,
	"data":         true,
}

var slotStringsSSOTSkipPathPrefixes = []string{
	"cmd/archcheck/scan",
}

// slotStringsSSOTCanonicalPath is the canonical SSOT file for slot
// string literals.
const slotStringsSSOTCanonicalPath = "internal/kernel/media/slot.go"

// slotStringsSSOTScanScope is the prefix the gate applies to.
const slotStringsSSOTScanScope = "internal/"

// ScanSlotStringsBan walks every .go file under <root>/internal/**
// and emits a violation for any production file (NOT _test.go)
// outside the canonical slot.go that contains a hardcoded slot
// string literal.
func ScanSlotStringsBan(root string, _ *policy.Policy, r *report.Report) {
	textban.Scan(root, textban.Spec{
		RuleID:           slotStringsSSOTRule,
		MatchedRule:      "slot_strings_ssot_gate",
		Note:             slotStringsSSOTNote,
		ScanPrefixes:     []string{slotStringsSSOTScanScope},
		SkipDirs:         slotStringsSSOTSkipDirs,
		SkipPathPrefixes: slotStringsSSOTSkipPathPrefixes,
		ExemptPaths:      []string{slotStringsSSOTCanonicalPath},
		Match: func(line string) bool {
			return slotStringsDistinctRe.MatchString(line) ||
				(slotStringsContextRe.MatchString(line) && slotStringsGenericRe.MatchString(line))
		},
	}, r)
}

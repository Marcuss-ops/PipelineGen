// Package scan — percheck_overlay_catalog_parity is the forward-prevention
// gate for the THREE copies of the Chronon overlay/motion catalog.
//
// BACKGROUND. One catalog exists three times across the two repos:
//
//  1. AUTHOR   ChrononTemplate/catalog/motion_catalog.v1.json — the
//     hand-written authoring document (ids, keyframes, selections). It is NOT
//     the runtime catalog by itself.
//  2. EMITTED  ChrononTemplate/catalog/chronontemplate_catalog.v1.json — what
//     the chronontemplate_emit_catalog binary writes (it copies the
//     author-owned sections and ENRICHES `motions` with the built registries).
//  3. EMBEDDED RenderingGen/renderinggen/internal/motion/catalog/
//     chronontemplate_catalog.v1.json — the go:embed artifact the worker
//     resolves motion/preset ids from.
//
// THE REAL CONTRACT (discovered by running this gate against the tree, and
// matching what scripts/sync_motion_catalog.sh --check already asserts):
//
//   - AUTHOR.overlay_presets == EMITTED.overlay_presets exactly. These are the
//     semantic preset ids the producer selects, so a preset that exists in one
//     copy and not the other is a producer/worker mismatch.
//   - EMITTED == EMBEDDED for the whole document. The embedded copy is a byte
//     copy of the emitter output; ANY difference means the worker is running a
//     catalog the emitter did not produce.
//   - AUTHOR.motions is a SUBSET of EMITTED.motions (by id). The emitter
//     legitimately ADDS motions from the compiled registries, so equality would
//     be a false positive — but a motion that vanishes between the two is
//     drift, because the author believed it was shipped.
//
// It is deliberately NOT "all three copies are identical": that assertion is
// false today (the emitter enriches `motions` by 102 ids) and a gate that
// cannot pass is not a gate.
//
// WHY THIS MATTERS. Before this gate the only protection was a shell helper
// (RenderingGen/scripts/sync_motion_catalog.sh) that no CI leg ran, so an
// author editing one JSON produced a mismatch discovered at render time. The
// shell helper also needs a C++ toolchain to emit; this gate needs only the
// three files on disk, so it runs on every archcheck invocation.
//
// NO-OP CONTRACT. The AUTHOR and EMITTED copies live in the sibling
// ChrononTemplate repo and the EMBEDDED copy in the sibling RenderingGen repo
// (the layout CONFORMANCE.md documents). When neither sibling is present the
// gate is a no-op, so a bare PipelineGen CI checkout never reports a fabricated
// violation. If ANY copy is present, all three must be.
//
// matched rule_id: `percheck_overlay_catalog_parity`.
package boundaries

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/policy"
	"github.com/Marcuss-ops/PipelineGen/cmd/archcheck/report"
)

// overlayCatalogParityRule is the rule-family id the scanner emits.
const overlayCatalogParityRule = "percheck_overlay_catalog_parity"

const overlayCatalogParityNote = "overlay/motion catalog drift between AUTHOR, EMITTED and EMBEDDED copies"

// overlayCatalogCopy is one of the three catalog copies: a repo-relative path
// plus the human label used in violations.
type overlayCatalogCopy struct {
	label string
	path  string
}

// overlayCatalogCopies resolves the three copies relative to a repo root.
func overlayCatalogCopies(root string) []overlayCatalogCopy {
	return []overlayCatalogCopy{
		{label: "author", path: filepath.Join(root, "..", "ChrononTemplate", "catalog", "motion_catalog.v1.json")},
		{label: "emitted", path: filepath.Join(root, "..", "ChrononTemplate", "catalog", "chronontemplate_catalog.v1.json")},
		{label: "embedded", path: filepath.Join(root, "..", "RenderingGen", "renderinggen", "internal", "motion", "catalog", "chronontemplate_catalog.v1.json")},
	}
}

// overlayCatalogParityCopy is one loaded copy.
type overlayCatalogParityCopy struct {
	overlayCatalogCopy
	doc map[string]any
}

// overlayCatalogMotionIDs extracts the `motions[].id` set. A motion entry
// without a string id contributes no id (it is reported separately as
// malformed only when the author/emitted comparison needs it).
func overlayCatalogMotionIDs(doc map[string]any) (map[string]struct{}, bool) {
	raw, ok := doc["motions"].([]any)
	if !ok {
		return nil, false
	}
	ids := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		id, ok := m["id"].(string)
		if !ok || strings.TrimSpace(id) == "" {
			continue
		}
		ids[id] = struct{}{}
	}
	return ids, true
}

// ScanOverlayCatalogParity asserts the three catalog copies agree on the
// cross-repo-critical contract. Missing sibling checkouts are a no-op; a
// present-but-unreadable or unparseable copy is itself a violation (fail
// closed), because an unreadable catalog means the parity claim cannot be
// evaluated.
func ScanOverlayCatalogParity(root string, _ *policy.Policy, r *report.Report) {
	copies := overlayCatalogCopies(root)
	loaded := make([]overlayCatalogParityCopy, 0, len(copies))
	for _, copy := range copies {
		abs, err := filepath.Abs(copy.path)
		if err != nil {
			r.Violations = append(r.Violations, report.Violation{
				File: copy.path, Rule: overlayCatalogParityRule,
				Severity: string(report.SeverityError), MatchedRule: "catalog_path_unresolvable",
				Note: overlayCatalogParityNote + " | copy: " + copy.label + " | " + err.Error(),
			})
			return
		}
		raw, err := os.ReadFile(abs)
		if err != nil {
			if os.IsNotExist(err) {
				// Absent sibling checkout: nothing to compare for this copy.
				continue
			}
			r.Violations = append(r.Violations, report.Violation{
				File: copy.path, Rule: overlayCatalogParityRule,
				Severity: string(report.SeverityError), MatchedRule: "catalog_unreadable",
				Note: overlayCatalogParityNote + " | copy: " + copy.label + " | " + err.Error(),
			})
			return
		}
		doc := map[string]any{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			r.Violations = append(r.Violations, report.Violation{
				File: copy.path, Rule: overlayCatalogParityRule,
				Severity: string(report.SeverityError), MatchedRule: "catalog_unparseable",
				Note: overlayCatalogParityNote + " | copy: " + copy.label + " | " + err.Error(),
			})
			return
		}
		loaded = append(loaded, overlayCatalogParityCopy{overlayCatalogCopy: copy, doc: doc})
	}
	if len(loaded) == 0 {
		// No sibling checkouts: nothing to check.
		return
	}
	if len(loaded) != len(copies) {
		present := map[string]bool{}
		for _, c := range loaded {
			present[c.label] = true
		}
		var missing []string
		for _, c := range copies {
			if !present[c.label] {
				missing = append(missing, c.label+" ("+c.path+")")
			}
		}
		r.Violations = append(r.Violations, report.Violation{
			File: "ChrononTemplate/catalog", Rule: overlayCatalogParityRule,
			Severity: string(report.SeverityError), MatchedRule: "catalog_copy_missing",
			Note: overlayCatalogParityNote + " | present copies exist but these are missing: " + strings.Join(missing, ", "),
		})
		return
	}

	byLabel := map[string]overlayCatalogParityCopy{}
	for _, c := range loaded {
		byLabel[c.label] = c
	}

	// Rule 1 — AUTHOR.overlay_presets == EMITTED.overlay_presets.
	author, emitted := byLabel["author"], byLabel["emitted"]
	if !overlayCatalogSectionPresent(author.doc, "overlay_presets") || !overlayCatalogSectionPresent(emitted.doc, "overlay_presets") {
		r.Violations = append(r.Violations, report.Violation{
			File: author.path, Rule: overlayCatalogParityRule,
			Severity: string(report.SeverityError), MatchedRule: "catalog_section_missing",
			Note: overlayCatalogParityNote + ` | section "overlay_presets" is missing from the author or emitted copy`,
		})
	} else if !reflect.DeepEqual(author.doc["overlay_presets"], emitted.doc["overlay_presets"]) {
		r.Violations = append(r.Violations, report.Violation{
			File: author.path, Rule: overlayCatalogParityRule,
			Severity: string(report.SeverityError), MatchedRule: "catalog_presets_drift",
			Note: overlayCatalogParityNote + ` | overlay_presets differs between author and emitted — the producer would select a preset the emitter never shipped`,
		})
	}

	// Rule 2 — EMITTED == EMBEDDED for the whole document.
	embedded := byLabel["embedded"]
	if !reflect.DeepEqual(emitted.doc, embedded.doc) {
		r.Violations = append(r.Violations, report.Violation{
			File: embedded.path, Rule: overlayCatalogParityRule,
			Severity: string(report.SeverityError), MatchedRule: "catalog_embedded_stale",
			Note: overlayCatalogParityNote + " | the embedded catalog differs from the emitted one — regenerate it with RenderingGen/scripts/sync_motion_catalog.sh",
		})
	}

	// Rule 3 — AUTHOR.motions ⊆ EMITTED.motions (by id).
	authorMotions, authorOK := overlayCatalogMotionIDs(author.doc)
	emittedMotions, emittedOK := overlayCatalogMotionIDs(emitted.doc)
	if !authorOK || !emittedOK {
		r.Violations = append(r.Violations, report.Violation{
			File: author.path, Rule: overlayCatalogParityRule,
			Severity: string(report.SeverityError), MatchedRule: "catalog_motions_missing",
			Note: overlayCatalogParityNote + ` | section "motions" is missing from the author or emitted copy`,
		})
	} else {
		var vanished []string
		for id := range authorMotions {
			if _, ok := emittedMotions[id]; !ok {
				vanished = append(vanished, id)
			}
		}
		if len(vanished) > 0 {
			sort.Strings(vanished)
			r.Violations = append(r.Violations, report.Violation{
				File: author.path, Rule: overlayCatalogParityRule,
				Severity: string(report.SeverityError), MatchedRule: "catalog_motion_vanished",
				Note: fmt.Sprintf("%s | %d authored motion id(s) missing from the emitted catalog: %s",
					overlayCatalogParityNote, len(vanished), strings.Join(truncateIDs(vanished, 8), ", ")),
			})
		}
	}
}

// overlayCatalogSectionPresent reports whether a top-level section exists in a
// decoded catalog document.
func overlayCatalogSectionPresent(doc map[string]any, section string) bool {
	_, ok := doc[section]
	return ok
}

// truncateIDs bounds a violation note while keeping it actionable.
func truncateIDs(ids []string, max int) []string {
	if len(ids) <= max {
		return ids
	}
	return append(append([]string{}, ids[:max]...), fmt.Sprintf("… +%d more", len(ids)-max))
}

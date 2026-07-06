//go:build ignore

// Package fixture — fixture for Check 55 (forbid legacy Template/TimelineJSON
// writes outside canonical allowlist).
//
// This file exists ONLY so that `bash scripts/ci-architectural-checks.sh
// --self-check` can verify that the regexes `Template:\s` and `TimelineJSON:\s`
// catch the forbidden struct-literal field assignments. It is NOT executed at
// runtime and is excluded from the production build by the gate's own walker
// (which never sees this file — self-check mode reads each fixture directly).
//
// The lines below contain `Template: "some_value"` and `TimelineJSON: "some_json"`
// which the regex MUST match. If a future contributor edits the Check 55 regex
// and accidentally regresses its precision, the self-check loop will detect it
// (rg returns nothing on this fixture → self-check exit 1).
//
// godlike/06 SSOT: PersistenceProcessor is the SOLE canonical writer of
// Template + TimelineJSON (set to empty "" under PR 6). The translators in
// repository.go (toSQLiteScriptRecord / fromSQLiteScriptRecord) are the
// canonical READ-path owners. Every other production-code struct literal
// that assigns Template: or TimelineJSON: outside those two files is a
// SSOT regression — the fields are legacy columns intentionally left empty
// for newly-inserted rows per the PR 6 migration strategy.
package fixture

// BadTemplateLiteral is the synthetic production-shape pattern the gate
// MUST catch. Struct-literal field `Template: "legacy"` outside the
// canonical PersistenceProcessor or repository translator is a SSOT
// regression per godlike/06 (one-owner-per-fact).
type BadTemplateLiteral struct {
	Template string
}

func NewBadTemplateLiteral() *BadTemplateLiteral {
	return &BadTemplateLiteral{Template: "legacy"}
}

// BadTimelineJSONLiteral is the synthetic production-shape pattern the gate
// MUST catch. Struct-literal field `TimelineJSON: "{\"v\":1}"` outside the
// canonical PersistenceProcessor or repository translator is a SSOT
// regression per godlike/06.
type BadTimelineJSONLiteral struct {
	TimelineJSON string
}

func NewBadTimelineJSONLiteral() *BadTimelineJSONLiteral {
	return &BadTimelineJSONLiteral{TimelineJSON: "{\"v\":1}"}
}

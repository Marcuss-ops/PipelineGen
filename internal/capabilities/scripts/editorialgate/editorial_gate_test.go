package editorialgate

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
)

func card(id, kind, template, scene string, startMS, endMS int64, params map[string]any) capmodel.OverlayItem {
	return capmodel.OverlayItem{
		ID:         id,
		Kind:       kind,
		TemplateID: template,
		SceneID:    scene,
		StartMs:    startMS,
		EndMs:      endMS,
		Params:     params,
	}
}

func phraseCard(id string, startMS, endMS int64) capmodel.OverlayItem {
	return card(id, "text_phrase", "IMPORTANT_PHRASE", "scene-1", startMS, endMS, nil)
}

// A plan that meets every criterion must pass, and strict must not fail it.
func TestGatePassesACompliantPlan(t *testing.T) {
	items := []capmodel.OverlayItem{
		phraseCard("p1", 10_000, 16_000),
		phraseCard("p2", 20_000, 26_000),
		phraseCard("p3", 30_000, 36_000),
		phraseCard("p4", 40_000, 46_000),
		phraseCard("p5", 50_000, 56_000),
		phraseCard("p6", 60_000, 66_000),
		phraseCard("p7", 70_000, 76_000),
		phraseCard("p8", 80_000, 86_000),
	}
	report := Evaluate(Config{
		Policy:            PolicyStrict,
		DurationMS:        100_000,
		Items:             items,
		EnabledCategories: map[string]string{"phrases": "enabled"},
		SourceTexts:       []string{"Ilary Blasi parla della sua storia."},
		MediaContract:     "overlay-v1-noalpha",
	})
	if !report.OK() {
		t.Fatalf("report = %+v, want pass", report)
	}
	if err := report.Enforce(); err != nil {
		t.Fatalf("enforce on a compliant plan: %v", err)
	}
	if report.CoveragePercent <= 25 {
		t.Fatalf("coverage = %v, want the measured value", report.CoveragePercent)
	}
	if report.MaxGapMS > DefaultMaxGapMS {
		t.Fatalf("max gap = %d, want <= %d", report.MaxGapMS, DefaultMaxGapMS)
	}
}

// The r78 baseline: 14.64% coverage and a 59.64 s hole. The gate must report
// both numbers instead of accepting the plan.
func TestGateFailsCoverageAndGap(t *testing.T) {
	items := []capmodel.OverlayItem{phraseCard("p1", 5_000, 11_000)}
	report := Evaluate(Config{Policy: PolicyWarn, DurationMS: 100_000, Items: items})
	if report.OK() {
		t.Fatalf("report = %+v, want coverage/gap failures", report)
	}
	if report.CoveragePercent != 6 {
		t.Fatalf("coverage = %v, want 6", report.CoveragePercent)
	}
	if report.MaxGapMS != 89_000 {
		t.Fatalf("max gap = %d, want 89000", report.MaxGapMS)
	}
	if len(report.Violations) < 2 {
		t.Fatalf("violations = %v, want coverage and max_gap", report.Violations)
	}
}

func TestGateFailsOnOverlap(t *testing.T) {
	items := []capmodel.OverlayItem{
		phraseCard("p1", 0, 6_000),
		phraseCard("p2", 3_000, 9_000),
		phraseCard("p3", 20_000, 26_000),
		phraseCard("p4", 40_000, 46_000),
		phraseCard("p5", 60_000, 66_000),
		phraseCard("p6", 80_000, 86_000),
	}
	report := Evaluate(Config{DurationMS: 100_000, Items: items})
	if report.OK() {
		t.Fatalf("report = %+v, want overlap failure", report)
	}
	if report.OverlapCount != 1 {
		t.Fatalf("overlap count = %d, want 1", report.OverlapCount)
	}
}

// A 125 ms numeric card is a flash, not a readable stat.
func TestGateFailsOnShortCard(t *testing.T) {
	items := []capmodel.OverlayItem{
		card("due", "number", "METRIC_STAT_CARD", "scene-1", 6_125, 6_250, nil),
		phraseCard("p1", 20_000, 26_000),
		phraseCard("p2", 40_000, 46_000),
		phraseCard("p3", 60_000, 66_000),
		phraseCard("p4", 80_000, 86_000),
	}
	report := Evaluate(Config{DurationMS: 100_000, Items: items})
	if report.OK() {
		t.Fatalf("report = %+v, want card_duration failure", report)
	}
	found := false
	for _, violation := range report.Violations {
		if len(violation) > 13 && violation[:13] == "card_duration" {
			found = true
		}
	}
	if !found {
		t.Fatalf("violations = %v, want card_duration", report.Violations)
	}
}

// Three categories were enabled in r38 and produced zero items without a word.
func TestGateFailsOnSilentEnabledCategory(t *testing.T) {
	items := []capmodel.OverlayItem{
		phraseCard("p1", 20_000, 26_000),
		phraseCard("p2", 40_000, 46_000),
		phraseCard("p3", 60_000, 66_000),
		phraseCard("p4", 80_000, 86_000),
	}
	report := Evaluate(Config{
		DurationMS:        100_000,
		Items:             items,
		EnabledCategories: map[string]string{"phrases": "enabled", "bullets": "enabled", "quotes": "enabled"},
	})
	if report.OK() {
		t.Fatalf("report = %+v, want silent-category failure", report)
	}
	joined := ""
	for _, violation := range report.Violations {
		joined += violation + "\n"
	}
	if !strings.Contains(joined, "bullets") || !strings.Contains(joined, "quotes") {
		t.Fatalf("violations = %v, want bullets and quotes", report.Violations)
	}
}

// r80 forced transparency on `overlay-v1-noalpha`; the opaque windows turned
// into black artifacts.
func TestGateFailsOnTransparencyWithoutAlpha(t *testing.T) {
	items := []capmodel.OverlayItem{
		card("p1", "text_phrase", "IMPORTANT_PHRASE", "scene-1", 20_000, 26_000, map[string]any{"transparent": true}),
		phraseCard("p2", 40_000, 46_000),
		phraseCard("p3", 60_000, 66_000),
		phraseCard("p4", 80_000, 86_000),
	}
	report := Evaluate(Config{
		DurationMS:    100_000,
		Items:         items,
		MediaContract: capmodel.DefaultOverlayContractNoAlpha.ID,
	})
	if report.OK() {
		t.Fatalf("report = %+v, want alpha_contract failure", report)
	}
	if !strings.Contains(joinedViolations(report), "alpha_contract") {
		t.Fatalf("violations = %v, want alpha_contract", report.Violations)
	}

	// The same item on the alpha contract is legal.
	allowed := Evaluate(Config{
		DurationMS:    100_000,
		Items:         items,
		MediaContract: capmodel.DefaultOverlayContractV1.ID,
	})
	for _, violation := range allowed.Violations {
		if strings.Contains(violation, "alpha_contract") {
			t.Fatalf("alpha-bearing contract must accept transparency: %v", allowed.Violations)
		}
	}
}

// The Ilary/Totti r1 leak pronounced «Ricostruisci …» and «Apri con …».
func TestGateFailsOnImperativeSourceText(t *testing.T) {
	items := []capmodel.OverlayItem{
		phraseCard("p1", 20_000, 26_000),
		phraseCard("p2", 40_000, 46_000),
		phraseCard("p3", 60_000, 66_000),
		phraseCard("p4", 80_000, 86_000),
	}
	report := Evaluate(Config{
		DurationMS:  100_000,
		Items:       items,
		SourceTexts: []string{"Ricostruisci Francesco Totti a partire dalle clip del 2026."},
	})
	if report.OK() {
		t.Fatalf("report = %+v, want source_directives failure", report)
	}
	if !strings.Contains(joinedViolations(report), "Ricostruisci") {
		t.Fatalf("violations = %v, want the directive clause", report.Violations)
	}
}

func TestStrictPolicyFailsClosedAndWarnDoesNot(t *testing.T) {
	items := []capmodel.OverlayItem{phraseCard("p1", 5_000, 11_000)}
	strict := Evaluate(Config{Policy: PolicyStrict, DurationMS: 100_000, Items: items})
	if err := strict.Enforce(); !errors.Is(err, ErrEditorialQualityGate) {
		t.Fatalf("strict err = %v, want ErrEditorialQualityGate", err)
	}
	warn := Evaluate(Config{Policy: PolicyWarn, DurationMS: 100_000, Items: items})
	if err := warn.Enforce(); err != nil {
		t.Fatalf("warn err = %v, want nil", err)
	}
	if warn.OK() {
		t.Fatal("warn must still report the failure")
	}
}

func TestGateReportIsDeterministic(t *testing.T) {
	items := []capmodel.OverlayItem{
		card("b", "text_phrase", "IMPORTANT_PHRASE", "scene-1", 5_000, 5_125, nil),
		card("a", "text_phrase", "IMPORTANT_PHRASE", "scene-1", 5_000, 5_125, nil),
	}
	first := Evaluate(Config{DurationMS: 100_000, Items: items})
	second := Evaluate(Config{DurationMS: 100_000, Items: items})
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("gate is not deterministic:\n%+v\n%+v", first, second)
	}
}

func joinedViolations(report Report) string {
	joined := ""
	for _, violation := range report.Violations {
		joined += violation + "\n"
	}
	return joined
}

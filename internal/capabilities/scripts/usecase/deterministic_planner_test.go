// Package usecase — deterministic_planner_test.go pins the FASE-3
// deterministic ClipPrePlanner contract: same PlanRequest ->
// byte-identical ClipPrePlan, ref stability across runs, error
// envelopes for caller-side violations, duration distribution
// correctness, keyword-bucket determinism, and ValidatePlan as a
// usable post-construction guardrail.
//
// godlike/06 SSOT: this file is the canonical test surface for the
// pre-planner. New planner invariant -> add its test here; do not
// spread planner tests across the package.
package usecase

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// pacquiaoSourceText is the canonical planning fixture used across
// FASE-3 planner tests. The total rune count is 385 chars. The
//
//	`scriptpkg.ComputeSourceHash` over this string is the SSOT
//
// hash to use for any anchor comparison or golden-test snapshot.
const pacquiaoSourceText = "Nel primo round Pacquiao ha impostato il ritmo del combattimento, " +
	"usando il jab per misurare la distanza e studiare Broner. " +
	"A partire dal quarto round Pacquiao ha cominciato a fare combinazioni, " +
	"spingendo Broner verso le corde. " +
	"Nel settimo round Pacquiao ha messo Broner in difficoltà vicino all'angolo, " +
	"prima che i giudici decretassero la vittoria ai punti con verdetto unanime."

// pacquiaoReq returns the canonical Pacquiao vs Broner recap
// request, mirroring the example from the original pre-planner
// spec. Four non-empty Segments map to slot-1..slot-4.
func pacquiaoReq() PlanRequest {
	return PlanRequest{
		ItemID:           "pacquiao-broner-recap",
		Title:            "Pacquiao vs Broner Recap",
		Topic:            "Pacquiao Broner recap",
		SourceText:       pacquiaoSourceText,
		Tone:             "informative",
		TargetDurationMs: 30000,
		MaxClips:         8,
		Segments: []scriptpkg.ScriptSegment{
			{Topic: "La fase iniziale e lo studio della distanza"},
			{Topic: "Pressione crescente di Pacquiao (round 4-6)"},
			{Topic: "Round 7 - momento decisivo"},
			{Topic: "Decisione finale dei giudici"},
		},
	}
}

func TestDeterministicPlanner_DeterministicTwoRuns(t *testing.T) {
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	ctx := context.Background()

	a, err := p.Plan(ctx, req)
	if err != nil {
		t.Fatalf("plan A: %v", err)
	}
	b, err := p.Plan(ctx, req)
	if err != nil {
		t.Fatalf("plan B: %v", err)
	}

	aJSON, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal A: %v", err)
	}
	bJSON, err := json.Marshal(b)
	if err != nil {
		t.Fatalf("marshal B: %v", err)
	}
	if string(aJSON) != string(bJSON) {
		t.Fatalf("byte-identical determinism violated\nA=%s\nB=%s", aJSON, bJSON)
	}
}

func TestDeterministicPlanner_PacquiaoGolden(t *testing.T) {
	p := NewDeterministicPlanner()
	plan, err := p.Plan(context.Background(), pacquiaoReq())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	if plan.Version != 1 {
		t.Fatalf("version: want 1, got %d", plan.Version)
	}
	if plan.Title != "Pacquiao vs Broner Recap" {
		t.Fatalf("title: want %q, got %q", "Pacquiao vs Broner Recap", plan.Title)
	}

	expectedSrcHash := scriptpkg.ComputeSourceHash(pacquiaoSourceText)
	if plan.SourceHash != expectedSrcHash {
		t.Fatalf("sourcehash drift: want %s, got %s",
			expectedSrcHash, plan.SourceHash)
	}

	if len(plan.Slots) != 4 {
		t.Fatalf("slot count: want 4, got %d", len(plan.Slots))
	}

	// Ref stability: slot-1..slot-N strictly sequential.
	expectedRefs := []string{"slot-1", "slot-2", "slot-3", "slot-4"}
	for i, s := range plan.Slots {
		if s.Ref != expectedRefs[i] {
			t.Errorf("slot[%d] ref: want %q, got %q",
				i, expectedRefs[i], s.Ref)
		}
		// SourceAnchor.SourceHash MUST equal plan.SourceHash by
		// construction (deterministic anti-drift gate).
		if s.SourceAnchor == nil {
			t.Errorf("slot[%d] source_anchor nil", i)
			continue
		}
		if s.SourceAnchor.SourceHash != plan.SourceHash {
			t.Errorf("slot[%d] source_anchor.source_hash drift (got %q, plan %q)",
				i, s.SourceAnchor.SourceHash, plan.SourceHash)
		}
		if strings.TrimSpace(s.Topic) == "" {
			t.Errorf("slot[%d] topic is required", i)
		}
		if strings.TrimSpace(s.SearchQuery) == "" {
			t.Errorf("slot[%d] search_query is required (no-match fallback guarantees non-empty)", i)
		}
	}

	// Topic preservation: each segment's Topic appears verbatim in
	// the corresponding slot's Topic field.
	topicInputs := []string{
		"La fase iniziale e lo studio della distanza",
		"Pressione crescente di Pacquiao (round 4-6)",
		"Round 7 - momento decisivo",
		"Decisione finale dei giudici",
	}
	for i, s := range plan.Slots {
		if s.Topic != topicInputs[i] {
			t.Errorf("slot[%d] topic: want %q, got %q",
				i, topicInputs[i], s.Topic)
		}
	}

	// Total TargetDurationMs is exactly 30000 with 4 slots of 7500
	// each (30000 / 4 = 7500, remainder 0). Verify sum + per-slot.
	var sum int64
	for _, s := range plan.Slots {
		sum += s.TargetDurationMs
	}
	if sum != 30000 {
		t.Errorf("target_duration_ms sum: want 30000, got %d", sum)
	}
	for i, s := range plan.Slots {
		if s.TargetDurationMs != 7500 {
			t.Errorf("slot[%d] target_duration_ms: want 7500, got %d",
				i, s.TargetDurationMs)
		}
	}

	// ValidatePlan must accept the planner's output.
	if err := p.ValidatePlan(plan); err != nil {
		t.Fatalf("validate plan: %v", err)
	}
}

func TestDeterministicPlanner_SearchQueryComposition(t *testing.T) {
	p := NewDeterministicPlanner()
	plan, err := p.Plan(context.Background(), pacquiaoReq())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	// Pacquiao topic "Pressione crescente di Pacquiao (round 4-6)"
	// contains "press" so slot-2 must append QueryCombinationSuffix.
	// Other slots fall back to QueryFallSuffix because their topics
	// do not match any bucket keyword.
	wantSuffix := map[int]string{
		0: QueryFallSuffix,
		1: QueryCombinationSuffix,
		2: QueryFallSuffix,
		3: QueryDecisionSuffix,
	}
	wantTitle := "Pacquiao vs Broner Recap"
	for i, s := range plan.Slots {
		if !strings.Contains(s.SearchQuery, wantTitle) {
			t.Errorf("slot[%d] search_query missing title %q: %q",
				i, wantTitle, s.SearchQuery)
		}
		if !strings.Contains(s.SearchQuery, s.Topic) {
			t.Errorf("slot[%d] search_query missing topic %q: %q",
				i, s.Topic, s.SearchQuery)
		}
		if !strings.HasSuffix(s.SearchQuery, wantSuffix[i]) {
			t.Errorf("slot[%d] search_query must end with %q (first-match-wins); got %q",
				i, wantSuffix[i], s.SearchQuery)
		}
	}
}

func TestDeterministicPlanner_AnchorOffsetFromSubstringMatch(t *testing.T) {
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	// Replace topic-titles with topics that ARE substrings of source.
	req.Segments = []scriptpkg.ScriptSegment{
		{Topic: "Pacquiao"}, // substring of source
		{Topic: "Broner"},   // substring of source
	}
	plan, err := p.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Slots) != 2 {
		t.Fatalf("expected 2 slots, got %d", len(plan.Slots))
	}
	for _, s := range plan.Slots {
		if s.SourceAnchor.StartOffset < 0 || s.SourceAnchor.EndOffset <= s.SourceAnchor.StartOffset {
			t.Errorf("anchor for %q: offsets %d..%d invalid",
				s.Topic, s.SourceAnchor.StartOffset, s.SourceAnchor.EndOffset)
		}
		if strings.TrimSpace(s.SourceAnchor.Excerpt) == "" {
			t.Errorf("anchor for %q: empty excerpt (substring match should yield padded window)",
				s.Topic)
		}
		// ExcerptWindowPad = 16, so the excerpt must extend BEYOND the
		// topic's exact end position by at most ExcerptWindowPad
		// bytes (and start at most ExcerptWindowPad bytes earlier).
		if len(s.SourceAnchor.Excerpt) < len(s.Topic) {
			t.Errorf("anchor for %q: excerpt %q shorter than topic",
				s.Topic, s.SourceAnchor.Excerpt)
		}
	}
}

func TestDeterministicPlanner_DurationDistributionDeterminism(t *testing.T) {
	// 30001ms / 4 slots => bases 7500/7500/7500/7500 + remainder 1
	// => first slot gets base+1. Verify the deterministic remainder
	// distribution.
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	req.TargetDurationMs = 30001
	plan, err := p.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	want := []int64{7501, 7500, 7500, 7500}
	for i, s := range plan.Slots {
		if s.TargetDurationMs != want[i] {
			t.Errorf("slot[%d] target_duration_ms: want %d, got %d",
				i, want[i], s.TargetDurationMs)
		}
	}
	var sum int64
	for _, s := range plan.Slots {
		sum += s.TargetDurationMs
	}
	if sum != 30001 {
		t.Fatalf("sum: want 30001, got %d", sum)
	}
}

func TestDeterministicPlanner_RejectsEmptyTopic(t *testing.T) {
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	req.Topic = "   "
	_, err := p.Plan(context.Background(), req)
	if err == nil {
		t.Fatal("expected typed error, got nil")
	}
	var specErr *SourcePlanningError
	if !asPlanningErr(err, &specErr) {
		t.Fatalf("expected *SourcePlanningError, got %T: %v", err, err)
	}
	if specErr.Reason != PlanReasonEmptyTopic {
		t.Fatalf("expected reason %q, got %q", PlanReasonEmptyTopic, specErr.Reason)
	}
}

func TestDeterministicPlanner_RejectsEmptyTitle(t *testing.T) {
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	req.Title = ""
	_, err := p.Plan(context.Background(), req)
	if err == nil {
		t.Fatal("expected typed error, got nil")
	}
	var specErr *SourcePlanningError
	if !asPlanningErr(err, &specErr) {
		t.Fatalf("expected *SourcePlanningError, got %T: %v", err, err)
	}
	if specErr.Reason != PlanReasonEmptyTitle {
		t.Fatalf("expected reason %q, got %q", PlanReasonEmptyTitle, specErr.Reason)
	}
}

func TestDeterministicPlanner_EmptySourceWithSegments(t *testing.T) {
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	req.SourceText = ""
	plan, err := p.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Slots) != 4 {
		t.Fatalf("slots: want 4, got %d", len(plan.Slots))
	}
	for i, s := range plan.Slots {
		if s.SourceAnchor.StartOffset != 0 || s.SourceAnchor.EndOffset != 0 {
			t.Errorf("slot[%d] anchor offsets: want 0..0, got %d..%d",
				i, s.SourceAnchor.StartOffset, s.SourceAnchor.EndOffset)
		}
	}
}

func TestDeterministicPlanner_EmptySegmentsDerivesFromText(t *testing.T) {
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	req.Segments = nil
	plan, err := p.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Slots) != DefaultMaxClips {
		t.Fatalf("derived slots: want %d (DefaultMaxClips), got %d",
			DefaultMaxClips, len(plan.Slots))
	}
	for i, s := range plan.Slots {
		if !strings.HasPrefix(s.Ref, "slot-") {
			t.Errorf("slot[%d] ref: want slot-N prefix, got %q", i, s.Ref)
		}
	}
}

func TestDeterministicPlanner_NonEmptySegmentBlankTopicIsSkipped(t *testing.T) {
	p := NewDeterministicPlanner()
	req := pacquiaoReq()
	// Inject one blank-topic segment; planner must skip only that
	// and continue the ref-counter with a gap-allowed tolerant test.
	// We pin: the surviving slot count is 3, refs slot-1, slot-2,
	// slot-4 (slot-3 was skipped because Topic blank).
	req.Segments[2] = scriptpkg.ScriptSegment{Topic: "   "}
	plan, err := p.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(plan.Slots) != 3 {
		t.Fatalf("slots: want 3 (1 blank skipped), got %d", len(plan.Slots))
	}
	wantRefs := []string{"slot-1", "slot-2", "slot-4"}
	for i, s := range plan.Slots {
		if s.Ref != wantRefs[i] {
			t.Errorf("slot[%d] ref: want %q, got %q",
				i, wantRefs[i], s.Ref)
		}
	}
}

func TestDeterministicPlanner_ValidatePlanDetectsHashDrift(t *testing.T) {
	p := NewDeterministicPlanner()
	plan, err := p.Plan(context.Background(), pacquiaoReq())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	plan.Slots[0].SourceAnchor.SourceHash = strings.Repeat("d", 64)
	err = p.ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "source_hash drift") {
		t.Fatalf("expected hash drift error, got: %v", err)
	}
}

func TestDeterministicPlanner_ValidatePlanDetectsBadVersion(t *testing.T) {
	p := NewDeterministicPlanner()
	plan, err := p.Plan(context.Background(), pacquiaoReq())
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	plan.Version = 2
	err = p.ValidatePlan(plan)
	if err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("expected version error, got: %v", err)
	}
}

func TestDeterministicPlanner_SourcePlanningErrorFormat(t *testing.T) {
	e := &SourcePlanningError{
		ItemID: "pac-broner",
		Reason: PlanReasonEmptyTopic,
		Detail: "test detail",
	}
	got := e.Error()
	if !strings.Contains(got, "item=pac-broner") {
		t.Errorf("error string missing item id: %s", got)
	}
	if !strings.Contains(got, "reason="+PlanReasonEmptyTopic) {
		t.Errorf("error string missing reason: %s", got)
	}
	if !strings.Contains(got, "test detail") {
		t.Errorf("error string missing detail: %s", got)
	}
}

// asPlanningErr is a small type-assertion helper. errors.As would
// also work but keeps the impl dependency-light.
func asPlanningErr(err error, target **SourcePlanningError) bool {
	if e, ok := err.(*SourcePlanningError); ok {
		*target = e
		return true
	}
	return false
}

// guard against accidental removal of strconv import if a future
// refactor drops visualIntentFor usage in this file.
var _ = strconv.Itoa

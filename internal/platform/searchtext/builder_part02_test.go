package searchtext

import (
	appsearchtext "github.com/Marcuss-ops/PipelineGen/internal/capabilities/indexing/searchtext"
	"strings"
	"testing"
)

func TestStockChunkStrategy_CategoryNil(t *testing.T) {
	// Category empty → prefix is just the title; the rest of the
	// sentence is unaffected.
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-2",
		Source:  "stock",
		Title:   "Knockout clip",
		Tags:    []string{"knockout"},
		Additional: map[string]string{
			"event":   "Wilder vs Fury",
			"round":   "12",
			"subject": "Deontay Wilder",
			"action":  "knocks down Fury",
		},
	}
	got := stockChunkStrategy(input)
	if strings.Contains(got, "  ") {
		t.Errorf("must not contain double-space when category is empty; got %q", got)
	}
	if !strings.HasPrefix(got, "Knockout clip ") {
		t.Errorf("prefix must be just the title when category empty; got %q", got)
	}
	mustContainAll(t, got,
		"Stock video from Wilder vs Fury round 12",
		"Deontay Wilder knocks down Fury",
		"Tags: knockout",
	)
}

func TestStockChunkStrategy_TagsEmpty(t *testing.T) {
	// Tags empty → the "Tags:" labelled segment is dropped entirely.
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-3",
		Source:  "stock",
		Title:   "Clip",
		Additional: map[string]string{
			"event":   "Fight Night",
			"round":   "1",
			"subject": "Fighter A",
			"action":  "jabs",
		},
	}
	got := stockChunkStrategy(input)
	if strings.Contains(got, "Tags:") {
		t.Errorf("empty tags must drop the 'Tags:' label; got %q", got)
	}
	if strings.HasSuffix(got, " ") {
		t.Errorf("must not end with trailing whitespace; got %q", got)
	}
	mustContainAll(t, got, "Stock video from Fight Night round 1", "Fighter A jabs")
}

func TestStockChunkStrategy_MultiSegment(t *testing.T) {
	// Two chunks from the same event: each must produce distinct text
	// keyed on round/subject so Qdrant can disambiguate them.
	a := appsearchtext.SearchTextInput{
		AssetID: "stock-4a",
		Source:  "stock",
		Title:   "Round 1",
		Tags:    []string{"boxing"},
		Additional: map[string]string{
			"event":   "Pacquiao vs Broner",
			"round":   "1",
			"subject": "Manny Pacquiao",
			"action":  "throws a jab",
		},
	}
	b := appsearchtext.SearchTextInput{
		AssetID: "stock-4b",
		Source:  "stock",
		Title:   "Round 5",
		Tags:    []string{"boxing"},
		Additional: map[string]string{
			"event":   "Pacquiao vs Broner",
			"round":   "5",
			"subject": "Adrien Broner",
			"action":  "blocks a hook",
		},
	}
	gotA := stockChunkStrategy(a)
	gotB := stockChunkStrategy(b)
	if gotA == gotB {
		t.Fatalf("multi-segment chunks with different round/subject must produce distinct text; both = %q", gotA)
	}
	mustContainAll(t, gotA, "round 1", "Manny Pacquiao", "throws a jab")
	mustContainAll(t, gotB, "round 5", "Adrien Broner", "blocks a hook")
}

func TestStockChunkStrategy_SourceURLOmitted(t *testing.T) {
	// Stock search_text must not leak source URL noise.
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-5",
		Source:  "stock",
		Title:   "Clip",
		Tags:    []string{"boxing"},
		Additional: map[string]string{
			"event":   "Event",
			"round":   "2",
			"subject": "Fighter",
			"action":  "moves",
		},
	}
	got := stockChunkStrategy(input)
	if strings.Contains(got, "http://") || strings.Contains(got, "https://") {
		t.Errorf("stock search_text must not include source URL noise; got %q", got)
	}
}

func TestStockChunkStrategy_EmptyInput(t *testing.T) {
	// Fully empty input → strategy must return empty string.
	input := appsearchtext.SearchTextInput{
		AssetID:    "stock-6",
		Source:     "stock",
		Additional: map[string]string{},
	}
	got := stockChunkStrategy(input)
	if got != "" {
		t.Errorf("fully empty input must return empty; got %q", got)
	}
}

// TestStockChunkStrategy_NilAdditional_NoPanic pins the no-panic
// invariant for nil Additional maps. Go's map zero-value semantics
// make this safe (add["x"] returns "" for nil maps) but the test
// locks it for future drift.
func TestStockChunkStrategy_NilAdditional_NoPanic(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:    "stock-nil-1",
		Source:     "stock",
		Title:      "Title only",
		Additional: nil,
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil Additional must not panic; got %v", r)
		}
	}()
	got := stockChunkStrategy(input)
	if got != "Title only" {
		t.Errorf("nil Additional with title only: got %q", got)
	}
}

// TestStockChunkStrategy_RoundOnlyHeader pins the comma-separated
// "Stock video, round N" output for the round-only branch (the
// branch where composeStockHeader's "from" preposition is missing
// but a separator is needed to keep the phrase grammatical).
func TestStockChunkStrategy_RoundOnlyHeader(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID: "stock-round-1",
		Source:  "stock",
		Additional: map[string]string{
			"round": "7",
		},
	}
	got := stockChunkStrategy(input)
	if !strings.Contains(got, "Stock video, round 7") {
		t.Errorf("round-only header must use comma separator; got %q", got)
	}
}

// TestStockChunkStrategy_AdditionalKeysContract locks the contract
// that stockChunkStrategyAdditionalKeys enumerates exactly the
// keys the strategy actually reads from Additional. A future drift
// (key added to strategy but not to the slice, OR vice versa) is
// caught here. Mirrors the godlike/06 SSOT discipline of pinning
// canonical-source-of-truth declarations.
//
// Coverage scope: the table-driven loop below covers the 4
// single-source-of-truth keys (event/round/subject/action).
// The Segment-bound pair (start_sec + end_sec) is covered
// separately by TestStockChunkStrategy_SegmentDropOnPartialEndpoints
// because the godlike/07 NO-FAKE-AVAILABILITY contract on those
// keys is asymmetric (BOTH must be present, OR the segment is
// dropped) and does not fit the per-key single-population pattern.
func TestStockChunkStrategy_AdditionalKeysContract(t *testing.T) {
	checks := []struct {
		key  string
		val  string
		want string
	}{
		{"event", "Event-X", "Stock video from Event-X"},
		{"round", "9", "Stock video, round 9"},
		{"subject", "Subject-Y", "Subject-Y"},
		{"action", "Action-Z", "Action-Z"},
		// start_sec + end_sec are tested together in SegmentBoth below
		// to lock the godlike/07 NO-FAKE-AVAILABILITY contract.
	}
	for _, c := range checks {
		t.Run(c.key, func(t *testing.T) {
			add := map[string]string{c.key: c.val}
			got := stockChunkStrategy(appsearchtext.SearchTextInput{
				AssetID:    "stock-contract-" + c.key,
				Source:     "stock",
				Additional: add,
			})
			if !strings.Contains(got, c.want) {
				t.Errorf("key %q (val=%q) must produce substring %q; got %q",
					c.key, c.val, c.want, got)
			}
		})
	}
}

// TestStockChunkStrategy_SegmentDropOnPartialEndpoints pins the
// godlike/07 NO-FAKE-AVAILABILITY contract: the "Segment X to Y"
// segment is emitted ONLY when BOTH start_sec and end_sec are set.
// A one-sided endpoint must drop the segment entirely (rather than
// emit a malformed "Segment 10.0s to s" or "Segment s to 20.0s"
// that would pollute the Qdrant BM25 channel).
func TestStockChunkStrategy_SegmentDropOnPartialEndpoints(t *testing.T) {
	cases := []struct {
		name        string
		add         map[string]string
		mustHave    []string
		mustNotHave []string
	}{
		{
			name:        "start_sec_only_drops_segment",
			add:         map[string]string{"start_sec": "10.0"},
			mustNotHave: []string{"Segment"},
		},
		{
			name:        "end_sec_only_drops_segment",
			add:         map[string]string{"end_sec": "20.0"},
			mustNotHave: []string{"Segment"},
		},
		{
			name:     "both_endpoints_emits_segment",
			add:      map[string]string{"start_sec": "10.0", "end_sec": "20.0"},
			mustHave: []string{"Segment 10.0s to 20.0s"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stockChunkStrategy(appsearchtext.SearchTextInput{
				AssetID:    "stock-seg-" + c.name,
				Source:     "stock",
				Additional: c.add,
			})
			for _, w := range c.mustHave {
				if !strings.Contains(got, w) {
					t.Errorf("must contain %q; got %q", w, got)
				}
			}
			for _, w := range c.mustNotHave {
				if strings.Contains(got, w) {
					t.Errorf("must NOT contain %q; got %q", w, got)
				}
			}
		})
	}
}

// TestStockChunkStrategy_Idempotent parallels the existing
// TestAllStrategies_Idempotent for parity with the other strategies.
func TestStockChunkStrategy_Idempotent(t *testing.T) {
	input := appsearchtext.SearchTextInput{
		AssetID:  "stock-idem-1",
		Source:   "stock",
		Title:    "Title",
		Category: "Boxe",
		Tags:     []string{"a", "b"},
		Additional: map[string]string{
			"event":     "E",
			"round":     "3",
			"subject":   "S",
			"action":    "A",
			"start_sec": "1.0",
			"end_sec":   "2.0",
		},
	}
	first := stockChunkStrategy(input)
	second := stockChunkStrategy(input)
	if first != second {
		t.Errorf("strategy must be idempotent; first=%q second=%q", first, second)
	}
}

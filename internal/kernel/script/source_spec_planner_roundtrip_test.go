// Package script — source_spec_planner_roundtrip_test.go pins the
// deterministic round-trip + Validate contract for ClipPrePlan and
// the slot-level types added in FASE 1.
//
// godlike/06 SSOT: this file is the canonical test surface for the
// planner type family. New planner type -> add its round-trip /
// invariant test here; do not spread planner tests across the
// package.
package script

import (
	"encoding/json"
	"strings"
	"testing"
)

// pacquiaoSourceText is the canonical planning fixture used across
// the pre-planner tests. Same logical text -> same SourceHash on
// every host (NFC + LF canonicalization). Total rune count is 385;
// the planner fixture anchors below map each slot to ~one quarter
// of the text so all end-offsets are in bounds.
const pacquiaoSourceText = "Nel primo round Pacquiao ha impostato il ritmo del combattimento, " +
	"usando il jab per misurare la distanza e studiare Broner. " +
	"A partire dal quarto round Pacquiao ha cominciato a fare combinazioni, " +
	"spingendo Broner verso le corde. " +
	"Nel settimo round Pacquiao ha messo Broner in difficoltà vicino all'angolo, " +
	"prima che i giudici decretassero la vittoria ai punti con verdetto unanime."

// anchor constructs a SourceAnchor with the planner's canonical
// shape. Used by the test fixtures so each test reads as a single
// block.
func anchor(hash string, start, end int, excerpt string) *SourceAnchor {
	return &SourceAnchor{
		SourceHash:  hash,
		StartOffset: start,
		EndOffset:   end,
		Excerpt:     excerpt,
	}
}

// pacquiaoPlan returns a fully-valid 4-slot ClipPrePlan on the
// Pacquiao fixture for round-trip / Validate tests. Each anchor
// offsets into pacquiaoSourceText (385 chars) so the test catches
// offset drift if the canonicalization layer changes or if the
// planner regresses to over-extending offsets past the text end.
func pacquiaoPlan() *ClipPrePlan {
	srcHash := ComputeSourceHash(pacquiaoSourceText)
	return &ClipPrePlan{
		Version:    1,
		SourceHash: srcHash,
		Title:      "Pacquiao vs Broner Recap",
		Slots: []ClipSearchSlot{
			{
				Ref:              "slot-1",
				Topic:            "La fase iniziale e lo studio della distanza",
				SourceAnchor:     anchor(srcHash, 0, 96, "Nel primo round Pacquiao ha impostato il ritmo..."),
				SearchQuery:      "Pacquiao Broner round 1 footwork jab",
				VisualIntent:     "Pacquiao mobile, Broner fermo a osservare.",
				TargetDurationMs: 6000,
				Required:         true,
			},
			{
				Ref:              "slot-2",
				Topic:            "Pressione crescente di Pacquiao (round 4-6)",
				SourceAnchor:     anchor(srcHash, 96, 192, "usando il jab per misurare la distanza..."),
				SearchQuery:      "Pacquiao Broner mid rounds combinations pressure",
				VisualIntent:     "Pacquiao combina jab e diretto verso le corde.",
				TargetDurationMs: 7500,
				Required:         true,
			},
			{
				Ref:              "slot-3",
				Topic:            "Round 7 - momento decisivo",
				SourceAnchor:     anchor(srcHash, 192, 288, "Nel settimo round Pacquiao ha messo Broner..."),
				SearchQuery:      "Pacquiao Broner round 7 corner combination",
				VisualIntent:     "Pacquiao spinge Broner verso l'angolo.",
				TargetDurationMs: 8000,
				Required:         true,
			},
			{
				Ref:              "slot-4",
				Topic:            "Decisione finale dei giudici",
				SourceAnchor:     anchor(srcHash, 288, len(pacquiaoSourceText), "prima che i giudici decretassero..."),
				SearchQuery:      "Pacquiao Broner unanimous decision announcement",
				VisualIntent:     "Giudici leggono il verdetto unanime.",
				TargetDurationMs: 5000,
				Required:         false,
			},
		},
	}
}

func TestComputeSourceHash_Deterministic(t *testing.T) {
	h1 := ComputeSourceHash(pacquiaoSourceText)
	h2 := ComputeSourceHash(pacquiaoSourceText)
	if h1 != h2 {
		t.Fatalf("non-deterministic hash: %s vs %s", h1, h2)
	}
	crlf := strings.ReplaceAll(pacquiaoSourceText, "\n", "\r\n")
	if got := ComputeSourceHash(crlf); got != h1 {
		t.Fatalf("CRLF normalization should NOT change hash; got %s vs %s",
			got, h1)
	}
	if len(h1) != 64 {
		t.Fatalf("hash must be 64 hex chars (SHA-256), got %d (%s)",
			len(h1), h1)
	}
}

func TestCanonicalizeSourceText(t *testing.T) {
	if got := CanonicalizeSourceText("a\r\nb\r\nc"); got != "a\nb\nc" {
		t.Fatalf("CRLF not normalized: %q", got)
	}
	if got := CanonicalizeSourceText("a\nb\nc"); got != "a\nb\nc" {
		t.Fatalf("LF should pass through: %q", got)
	}
	if got := CanonicalizeSourceText(""); got != "" {
		t.Fatalf("empty should stay empty: %q", got)
	}
}

func TestClipPrePlan_RoundTripJSON(t *testing.T) {
	plan := pacquiaoPlan()
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan invalid pre-RT: %v", err)
	}
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt ClipPrePlan
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("unmarshal: %v\njson: %s", err, b)
	}
	if err := rt.Validate(); err != nil {
		t.Fatalf("plan invalid post-RT: %v", err)
	}
	if rt.Version != plan.Version ||
		rt.SourceHash != plan.SourceHash ||
		rt.Title != plan.Title ||
		len(rt.Slots) != len(plan.Slots) {
		t.Fatalf("plan top-level fields mismatch after RT")
	}
	for i := range plan.Slots {
		if rt.Slots[i].Ref != plan.Slots[i].Ref {
			t.Fatalf("slot[%d] ref mismatch: %q vs %q",
				i, rt.Slots[i].Ref, plan.Slots[i].Ref)
		}
		if rt.Slots[i].Required != plan.Slots[i].Required {
			t.Fatalf("slot[%d] required mismatch: %v vs %v "+
				"(omitempty would drop false)",
				i, rt.Slots[i].Required, plan.Slots[i].Required)
		}
		if rt.Slots[i].SearchQuery != plan.Slots[i].SearchQuery {
			t.Fatalf("slot[%d] search_query mismatch", i)
		}
		if rt.Slots[i].TargetDurationMs != plan.Slots[i].TargetDurationMs {
			t.Fatalf("slot[%d] target_duration_ms mismatch", i)
		}
		if rt.Slots[i].SourceAnchor == nil ||
			rt.Slots[i].SourceAnchor.SourceHash !=
				plan.Slots[i].SourceAnchor.SourceHash {
			t.Fatalf("slot[%d] source_anchor hash drift on RT", i)
		}
		if rt.Slots[i].SourceAnchor.EndOffset !=
			plan.Slots[i].SourceAnchor.EndOffset {
			t.Fatalf("slot[%d] anchor end_offset drift on RT", i)
		}
	}
}

func TestClipPrePlan_RejectsAnchorHashMismatch(t *testing.T) {
	plan := pacquiaoPlan()
	plan.Slots[0].SourceAnchor.SourceHash = strings.Repeat("d", 64)
	err := plan.Validate()
	if err == nil || !strings.Contains(err.Error(), "source_hash mismatch") {
		t.Fatalf("expected source_hash mismatch error, got: %v", err)
	}
}

func TestClipPrePlan_RejectsRefGap(t *testing.T) {
	plan := pacquiaoPlan()
	plan.Slots[1].Ref = "slot-3"
	err := plan.Validate()
	if err == nil || !strings.Contains(err.Error(), "does not match expected") {
		t.Fatalf("expected ref-gap error, got: %v", err)
	}
}

func TestClipPrePlan_RejectsDuplicateRef(t *testing.T) {
	plan := pacquiaoPlan()
	plan.Slots[3].Ref = "slot-1" // duplicate of slot[0]
	err := plan.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate ref") {
		t.Fatalf("expected duplicate ref error, got: %v", err)
	}
}

func TestClipPrePlan_RejectsBadVersion(t *testing.T) {
	plan := pacquiaoPlan()
	plan.Version = 2
	err := plan.Validate()
	if err == nil || !strings.Contains(err.Error(), "unsupported version") {
		t.Fatalf("expected version error, got: %v", err)
	}
}

func TestClipPrePlan_RejectsMissingTitle(t *testing.T) {
	plan := pacquiaoPlan()
	plan.Title = "   "
	err := plan.Validate()
	if err == nil || !strings.Contains(err.Error(), "title is required") {
		t.Fatalf("expected title error, got: %v", err)
	}
}

func TestClipPrePlan_EmptySlotsIsValid(t *testing.T) {
	plan := &ClipPrePlan{
		Version:    1,
		SourceHash: ComputeSourceHash("fixture source"),
		Title:      "Empty Slots Plan",
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("empty slots should be valid (current contract), got: %v", err)
	}
}

func TestNarrativeClipView_NoInfrastructureIDs(t *testing.T) {
	view := NarrativeClipView{
		SlotRef:       "slot-1",
		Description:   "Pacquiao si muove.",
		VisualSummary: "Mobile",
		Transcript:    "Pacquiao is faster on his feet.",
		DurationMs:    6000,
	}
	b, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{
		"clip_id", "asset_id", "drive_link", "local_path",
		"source_url", "speaker", "commentator", "raw_metadata",
	} {
		if strings.Contains(strings.ToLower(string(b)), forbidden) {
			t.Fatalf("NarrativeClipView leaks %q in JSON: %s",
				forbidden, b)
		}
	}
}

func TestNarrativeClipView_RequiresSlotRef(t *testing.T) {
	view := NarrativeClipView{
		Description: "no slot ref here",
	}
	if err := view.Validate(); err == nil ||
		!strings.Contains(err.Error(), "slot_ref is required") {
		t.Fatalf("expected slot_ref required error, got: %v", err)
	}
}

func TestResolvedClipSlot_RoundTripJSON(t *testing.T) {
	srcHash := ComputeSourceHash(pacquiaoSourceText)
	slot := ResolvedClipSlot{
		Ref:            "slot-2",
		Topic:          "Pressione crescente di Pacquiao (round 4-6)",
		SourceAnchor:   anchor(srcHash, 96, 192, "usando il jab per misurare la distanza..."),
		ChosenAssetRef: "internal::asset/pac-bro-r4-uuid",
		SemanticScore:  0.92,
		VisualScore:    0.88,
		Narrative: &NarrativeClipView{
			SlotRef:       "slot-2",
			Description:   "Pacquiao spinge con combinazioni.",
			VisualSummary: "Jab + diretto verso le corde.",
			Transcript:    "Pacquiao corners Broner in round 5.",
			DurationMs:    7500,
		},
		Binding: &SlotClipBinding{
			SlotRef:      "slot-2",
			ClipID:       "yt_RRJvrDKunyA_240_255_v1",
			ClipTitle:    "Round 5 pressure highlight",
			DriveLink:    "https://drive.google.com/file/d/abc",
			StartMs:      240000,
			EndMs:        255000,
			SourceAnchor: anchor(srcHash, 96, 192, "usando il jab per misurare la distanza..."),
		},
	}
	if err := slot.Validate(srcHash); err != nil {
		t.Fatalf("slot invalid pre-RT: %v", err)
	}
	b, err := json.Marshal(slot)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt ResolvedClipSlot
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("unmarshal: %v\njson: %s", err, b)
	}
	if err := rt.Validate(srcHash); err != nil {
		t.Fatalf("slot invalid post-RT: %v", err)
	}
	if rt.Ref != slot.Ref ||
		rt.ChosenAssetRef != slot.ChosenAssetRef ||
		rt.SemanticScore != slot.SemanticScore ||
		rt.Narrative.DurationMs != slot.Narrative.DurationMs {
		t.Fatalf("RT mismatch on top-level slot fields")
	}
	if rt.Binding == nil || rt.Binding.ClipID != slot.Binding.ClipID {
		t.Fatalf("binding RT mismatch")
	}
	if rt.Binding.SourceAnchor == nil ||
		rt.Binding.SourceAnchor.SourceHash != srcHash {
		t.Fatalf("binding source_anchor hash drift on RT")
	}
}

func TestClipCandidate_RoundTripJSON(t *testing.T) {
	c := ClipCandidate{
		SlotRef:           "slot-1",
		AssetRef:          "internal::asset/pac-bro-r1-uuid",
		SemanticScore:     0.92,
		VisualScore:       0.88,
		QualityScore:      0.81,
		DurationMs:        6500,
		TranscriptSnippet: "Pacquiao comes out firing combinations...",
		Language:          "en",
		DriveLinkEmpty:    false,
		WitnessedAtMs:     1718000000000,
		PerSlotScoreBreakdown: map[string]float64{
			"semantic": 0.92,
			"visual":   0.88,
		},
	}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt ClipCandidate
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("unmarshal: %v\njson: %s", err, b)
	}
	if rt.SlotRef != c.SlotRef || rt.AssetRef != c.AssetRef {
		t.Fatalf("RT mismatch on identity fields")
	}
	if rt.PerSlotScoreBreakdown["semantic"] !=
		c.PerSlotScoreBreakdown["semantic"] {
		t.Fatalf("per_slot_score_breakdown RT mismatch")
	}
	if rt.WitnessedAtMs != c.WitnessedAtMs {
		t.Fatalf("witnessed_at_ms RT mismatch")
	}
}

func TestSlotClipBinding_RoundTripJSON(t *testing.T) {
	srcHash := ComputeSourceHash(pacquiaoSourceText)
	binding := SlotClipBinding{
		SlotRef:      "slot-1",
		ClipID:       "yt_RRJvrDKunyA_12_18_v1",
		ClipTitle:    "Round 1 footwork highlight",
		DriveLink:    "https://drive.google.com/file/d/xyz",
		StartMs:      12000,
		EndMs:        18500,
		SourceAnchor: anchor(srcHash, 0, 96, "Pacquiao iniziò..."),
	}
	b, err := json.Marshal(binding)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var rt SlotClipBinding
	if err := json.Unmarshal(b, &rt); err != nil {
		t.Fatalf("unmarshal: %v\njson: %s", err, b)
	}
	if rt.SlotRef != binding.SlotRef ||
		rt.ClipID != binding.ClipID ||
		rt.StartMs != binding.StartMs ||
		rt.EndMs != binding.EndMs {
		t.Fatalf("RT mismatch on SlotClipBinding top-level")
	}
	if rt.SourceAnchor == nil ||
		rt.SourceAnchor.SourceHash != srcHash {
		t.Fatalf("SourceAnchor RT mismatch")
	}
}

// Package usecase — pacquiao_broner_pre_planner_e2e_test.go drives the
// 7-stage operational pipeline end-to-end against the canonical
// Pacquiao fixture (4 narrative segments, no explicit clip_ids).
//
// Subtests:
//
//	(a) the deterministic planner emits 4 slots, refs slot-1..slot-4,
//	    source_anchor.source_hash == plan.source_hash, source_excerpt
//	    non-empty + offsets valid; golden snapshot of plan JSON;
//	(b) the sampler picks 4 distinct clip ids (one per slot);
//	(c) the model envelope carries ONLY ref+text — clip_id, drive_link,
//	    start_ms, end_ms are forbidden by struct shape and dropped on
//	    JSON round-trip even when smuggled;
//	(d) the post-voiceover composer resolves each ref to a different
//	    real clip_id through the canonical RefBindingResolver port.
//
// godlike/07 NO-FAKE-AVAILABILITY: Planner, sampler port contract,
// NarrativeClipView projection, PostVoiceoverComposer, and
// StaticRefBindingResolver are the REAL production pieces. We fake
// only what lives OUT-OF-PROCESS:
//
//   - the slot search port (semantic Qdrant search), replaced by an
//     in-test fixture pool;
//   - the LLM (mock returns the same {Ref, Text} envelope the model
//     is contracted to emit);
//   - delivery.Publisher (records calls without touching Drive).
//
// godlike/06 SSOT: this file lives in the same package as the
// planner, the sampler, and the composer so it can reuse
// `pacquiaoReq()` and `pacquiaoSourceText` directly without
// duplicating the canonical fixture.
//
// Regenerate the golden snapshot with:
//
//	UPDATE_GOLDEN=1 go test ./internal/application/scripts/usecase \
//	    -run TestClipPrePlanner_PacquiaoBroner_E2E -v
package usecase

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/application/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// pacquiaoClipFixture is the canonical fixture row for one
// Pacquiao/Broner round clip. The ID, drive_link, start_ms and
// end_ms mirror the canonical Pacquiao recap fixture at
// tests/fixtures/clip_batch_rrjvrdkunyA/payload.json.
type pacquiaoClipFixture struct {
	roundTag   string
	clipID     string
	driveLink  string
	startMs    int64
	endMs      int64
	transcript string
}

func pacquiaoClipFixtures() []pacquiaoClipFixture {
	return []pacquiaoClipFixture{
		{
			roundTag:   "round-1",
			clipID:     "yt_RRJvrDKunyA_32_37_v1",
			driveLink:  "https://drive.google.com/file/d/pacquiao-r1",
			startMs:    32000,
			endMs:      37000,
			transcript: "Inizio del match: Pacquiao mostra subito mobilita e rapidita di gambe, lavorando con il jab da mancino per prendere le misure.",
		},
		{
			roundTag:   "round-5",
			clipID:     "yt_RRJvrDKunyA_628_633_v1",
			driveLink:  "https://drive.google.com/file/d/pacquiao-r5",
			startMs:    628000,
			endMs:      633000,
			transcript: "Broner trova continuita con il diretto destro e colpisce il mento di Pacquiao; Pacquiao replica con gancio sinistro al corpo e riprende il ritmo.",
		},
		{
			roundTag:   "round-7",
			clipID:     "yt_RRJvrDKunyA_993_998_v1",
			driveLink:  "https://drive.google.com/file/d/pacquiao-r7",
			startMs:    993000,
			endMs:      998000,
			transcript: "Pacquiao mette a segno una serie di colpi durissimi, tra cui un montante e un sinistro che scuotono visibilmente Broner vicino all'angolo.",
		},
		{
			roundTag:   "round-12",
			clipID:     "yt_RRJvrDKunyA_1657_1662_v1",
			driveLink:  "https://drive.google.com/file/d/pacquiao-r12",
			startMs:    1657000,
			endMs:      1662000,
			transcript: "Negli ultimi 30 secondi Broner non mostra urgenza di recuperare e Pacquiao controlla agevolmente fino alla campana finale.",
		},
	}
}

// distinctPickSampler is a contract-only ports.ClipSampler
// implementation that emits the first non-duplicate candidates up
// to req.Limit. It deliberately does NOT run the 10-gate audit
// pipeline; gate correctness has its own canonical SSOT test
// surface (clip_sampler_impl_test.go + clip_sampler_gates_test.go).
//
// This E2E test exercises the WIRING: that the sampler port is
// called with the right request shape and that its output feeds
// the next stage without surprise duplicates.
type distinctPickSampler struct{}

func (distinctPickSampler) Select(req ports.ClipSamplerRequest, candidates []ports.ClipSamplerCandidate) (ports.ClipSamplerResult, error) {
	seen := make(map[string]struct{}, req.Limit)
	ids := make([]string, 0, req.Limit)
	items := make([]scriptpkg.SearchResultItem, 0, req.Limit)
	for _, c := range candidates {
		c.ClipID = strings.TrimSpace(c.ClipID)
		if c.ClipID == "" {
			continue
		}
		if _, dup := seen[c.ClipID]; dup {
			continue
		}
		seen[c.ClipID] = struct{}{}
		ids = append(ids, c.ClipID)
		items = append(items, scriptpkg.SearchResultItem{
			ClipID: c.ClipID,
			Name:   c.Name,
			Score:  c.Score,
			Source: c.Source,
		})
		if len(ids) >= req.Limit {
			break
		}
	}
	return ports.ClipSamplerResult{
		ClipIDs:     ids,
		SearchItems: items,
	}, nil
}

// fakeDeliveryPublisher records every Publish / ResolveFolder call
// without touching Drive. godlike/07 NO-FAKE-AVAILABILITY: we never
// silently no-op Drive — the fake satisfies the same interface so
// the composer cannot tell it is not production.
type fakeDeliveryPublisher struct {
	publishes      []delivery.PublishRequest
	resolves       []delivery.PublishRequest
	resolveFolders []string
}

func (f *fakeDeliveryPublisher) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	f.publishes = append(f.publishes, req)
	return &delivery.PublishResult{FileID: "fake-file-id-" + req.AssetID}, nil
}

func (f *fakeDeliveryPublisher) ResolveFolder(_ context.Context, req delivery.PublishRequest) (string, error) {
	f.resolves = append(f.resolves, req)
	folder := "fake-folder-id"
	if req.Group != "" {
		folder = folder + "-" + req.Group
	}
	f.resolveFolders = append(f.resolveFolders, folder)
	return folder, nil
}

// TestClipPrePlanner_PacquiaoBroner_E2E drives the seven operational
// stages end to end against the canonical Pacquiao fixture. The
// test variable `pickedByRef` threads sampler output (b) into the
// composer (d).
func TestClipPrePlanner_PacquiaoBroner_E2E(t *testing.T) {
	ctx := context.Background()
	fixtures := pacquiaoClipFixtures()
	pickedByRef := make(map[string]pacquiaoClipFixture, 4)
	refOrder := make([]string, 0, 4)

	// ── (a) PLANNER: 4 slots, slot-1..slot-4, source_excerpt OK ─
	t.Run("a_planner_4_slots_with_slot_refs_and_source_excerpt", func(t *testing.T) {
		p := NewDeterministicPlanner()
		plan, err := p.Plan(ctx, pacquiaoReq())
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if plan.Version != 1 {
			t.Fatalf("plan.version: want 1, got %d", plan.Version)
		}
		const wantTitle = "Pacquiao vs Broner Recap"
		if plan.Title != wantTitle {
			t.Fatalf("plan.title: want %q, got %q", wantTitle, plan.Title)
		}
		wantSrcHash := scriptpkg.ComputeSourceHash(pacquiaoSourceText)
		if plan.SourceHash != wantSrcHash {
			t.Fatalf("plan.source_hash drift: want %s, got %s",
				wantSrcHash, plan.SourceHash)
		}
		wantRefs := []string{"slot-1", "slot-2", "slot-3", "slot-4"}
		if len(plan.Slots) != 4 {
			t.Fatalf("plan.slots count: want 4, got %d", len(plan.Slots))
		}
		for i, s := range plan.Slots {
			if s.Ref != wantRefs[i] {
				t.Errorf("slots[%d].ref: want %q, got %q",
					i, wantRefs[i], s.Ref)
			}
			if s.SourceAnchor == nil {
				t.Errorf("slots[%d].source_anchor nil", i)
				continue
			}
			if got := strings.TrimSpace(s.SourceAnchor.Excerpt); got == "" {
				t.Errorf("slots[%d].source_anchor.excerpt empty", i)
			}
			if s.SourceAnchor.SourceHash != plan.SourceHash {
				t.Errorf("slots[%d].source_anchor.source_hash drift (got %q, plan %q)",
					i, s.SourceAnchor.SourceHash, plan.SourceHash)
			}
			if s.SourceAnchor.StartOffset < 0 ||
				s.SourceAnchor.EndOffset <= s.SourceAnchor.StartOffset {
				t.Errorf("slots[%d].source_anchor offsets %d..%d invalid",
					i, s.SourceAnchor.StartOffset, s.SourceAnchor.EndOffset)
			}
		}

		// (a) Golden snapshot: full plan JSON for drift detection.
		// Drive via UPDATE_GOLDEN=1.
		compareOrWritePrePlannerGolden(t, plan)
	})

	// ── (b) SAMPLER PORT: 4 distinct clip_ids ─────────────────
	t.Run("b_sampler_picks_four_distinct_clips", func(t *testing.T) {
		p := NewDeterministicPlanner()
		plan, err := p.Plan(ctx, pacquiaoReq())
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		sampler := distinctPickSampler{}
		for i, slot := range plan.Slots {
			fix := fixtures[i]
			cands := []ports.ClipSamplerCandidate{
				{
					ClipID:        fix.clipID,
					Name:          "Round " + fix.roundTag,
					Score:         0.95 - float64(i)*0.01,
					Source:        "semantic",
					MediaType:     "video",
					Transcript:    fix.transcript,
					VisualSummary: "boxing action — " + fix.roundTag,
				},
				{
					ClipID:        "neighbor-low-" + fix.roundTag,
					Name:          "Neighbor Low",
					Score:         0.05,
					Source:        "semantic",
					MediaType:     "video",
					Transcript:    "noise",
					VisualSummary: "noise",
				},
			}
			// Slot is mapped to the canonical domain shape
			// (scriptpkg.ClipSearchSlot) because
			// ports.ClipSamplerRequest.Slot is typed in the
			// domain. The mapping is field-by-field; the
			// distinctPickSampler doesn't actually read
			// req.Slot, so the conversion is purely structural.
			// godlike/06 SSOT forward-pointer (Wave 2.0): the
			// planner-local SourceAnchor (defined in this
			// package) and the scriptpkg.SourceAnchor (consumed
			// by ports.ClipSamplerRequest) are intentionally
			// separate types today. Both carry the same
			// SourceHash / StartOffset / EndOffset / Excerpt
			// quartet — explicit field copy keeps the Wire shape
			// stable until FASE-2 collapses them via type alias
			// (godlike/06 SSOT).
			anchorCopy := &scriptpkg.SourceAnchor{
				SourceHash:  slot.SourceAnchor.SourceHash,
				StartOffset: slot.SourceAnchor.StartOffset,
				EndOffset:   slot.SourceAnchor.EndOffset,
				Excerpt:     slot.SourceAnchor.Excerpt,
			}
			req := ports.ClipSamplerRequest{
				SlotRef: slot.Ref,
				Slot: scriptpkg.ClipSearchSlot{
					Ref:              slot.Ref,
					Topic:            slot.Topic,
					SourceAnchor:     anchorCopy,
					SearchQuery:      slot.SearchQuery,
					VisualIntent:     slot.VisualIntent,
					TargetDurationMs: slot.TargetDurationMs,
					Required:         slot.Required,
				},
				Query:       slot.SearchQuery,
				Limit:       1,
				MinCoverage: 0,
				MinScore:    0,
			}
			res, err := sampler.Select(req, cands)
			if err != nil {
				t.Fatalf("slot[%d] select: %v", i, err)
			}
			if len(res.ClipIDs) != 1 {
				t.Fatalf("slot[%d] picked %d clips, want 1",
					i, len(res.ClipIDs))
			}
			if res.ClipIDs[0] != fix.clipID {
				t.Fatalf("slot[%d] picked %q, want %q",
					i, res.ClipIDs[0], fix.clipID)
			}
			pickedByRef[slot.Ref] = fix
			refOrder = append(refOrder, slot.Ref)
		}
		// Verify pickedByRef covers all four slots with 4 distinct clip_ids.
		if len(pickedByRef) != 4 {
			t.Fatalf("pickedByRef distinct: want 4, got %d", len(pickedByRef))
		}
		seen := make(map[string]struct{}, 4)
		for ref, fix := range pickedByRef {
			if _, dup := seen[fix.clipID]; dup {
				t.Errorf("slot ref %q picked duplicate clip_id %q",
					ref, fix.clipID)
			}
			seen[fix.clipID] = struct{}{}
		}
	})

	// ── (c) MODEL ENVELOPE: ref+text only; smuggled infra ids dropped ─
	t.Run("c_model_envelope_is_ref_plus_text_only", func(t *testing.T) {
		// Compile-time + runtime discipline: the strict model envelope
		// CANNOT carry ClipID / DriveLink / StartMs / EndMs by struct
		// shape. The pre-voiceover canonical projection
		// (scriptpkg.NarrativeClipView) is the same way.
		checkEnvHasOnlyFields(t,
			reflect.TypeOf(scriptpkg.ModelOutputSegment{}),
			"Ref", "Text",
		)
		checkEnvHasOnlyFields(t,
			reflect.TypeOf(scriptpkg.NarrativeClipView{}),
			"SlotRef", "Description", "VisualSummary", "Transcript", "DurationMs",
		)

		mo := scriptpkg.ModelOutput{
			Segments: []scriptpkg.ModelOutputSegment{
				{Ref: "slot-1", Text: "Pacquiao apre con footwork rapido."},
				{Ref: "slot-2", Text: "Pressione crescente di Pacquiao."},
				{Ref: "slot-3", Text: "Broner barcolla vicino all'angolo."},
				{Ref: "slot-4", Text: "I giudici decretano la vittoria ai punti."},
			},
		}
		raw, err := json.Marshal(mo)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var rt scriptpkg.ModelOutput
		if err := json.Unmarshal(raw, &rt); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(rt.Segments) != 4 {
			t.Fatalf("round-trip segments: want 4, got %d", len(rt.Segments))
		}
		for i, seg := range rt.Segments {
			if seg.Ref == "" {
				t.Errorf("segments[%d].ref empty after round-trip", i)
			}
			if seg.Text == "" {
				t.Errorf("segments[%d].text empty after round-trip", i)
			}
		}

		// Smuggle attempt: a JSON object that pretends to be the
		// model's response. The strict envelope SILENTLY DROPS the
		// smuggled infra fields because the struct has no such fields.
		smuggled := `{"Segments":[{"Ref":"slot-1","Text":"x","ClipID":"INJECTED","DriveLink":"INJECTED","StartMs":1,"EndMs":2}]}`
		var bad scriptpkg.ModelOutput
		if err := json.Unmarshal([]byte(smuggled), &bad); err != nil {
			t.Fatalf("smuggled unmarshal: %v", err)
		}
		if len(bad.Segments) != 1 {
			t.Fatalf("smuggled survivor: want 1 segment, got %d", len(bad.Segments))
		}
		if bad.Segments[0].Ref != "slot-1" {
			t.Errorf("smuggled ref: want slot-1, got %q", bad.Segments[0].Ref)
		}
		if bad.Segments[0].Text != "x" {
			t.Errorf("smuggled text: want x, got %q", bad.Segments[0].Text)
		}
		// Re-check struct shape after smuggling attempt — invariants
		// must hold.
		checkEnvHasOnlyFields(t,
			reflect.TypeOf(bad.Segments[0]),
			"Ref", "Text",
		)
	})

	// ── (d) COMPOSER: each ref → a different real clip_id ─────
	t.Run("d_backend_resolves_each_ref_to_a_different_clip_id", func(t *testing.T) {
		if len(refOrder) != 4 {
			t.Fatalf("refOrder from (b): want 4 entries, got %d", len(refOrder))
		}
		// Build BindingTable keyed by ref → real clip_id pulled
		// from the (b) sampler picks. The composer hydrates the
		// manifest with these clip_ids.
		table := BindingTable{}
		mo := scriptpkg.ModelOutput{
			Segments: make([]scriptpkg.ModelOutputSegment, len(refOrder)),
		}
		for i, ref := range refOrder {
			fix := pickedByRef[ref]
			mo.Segments[i] = scriptpkg.ModelOutputSegment{
				Ref:  ref,
				Text: "shōt narrative for " + ref,
			}
			table[ref] = RefBinding{
				ClipID:    fix.clipID,
				ClipTitle: "Round " + fix.roundTag,
				DriveLink: fix.driveLink,
				StartMs:   fix.startMs,
				EndMs:     fix.endMs,
			}
		}

		pub := &fakeDeliveryPublisher{}
		composer, err := NewPostVoiceoverComposer(pub, &StaticRefBindingResolver{Table: table})
		if err != nil {
			t.Fatalf("composer: %v", err)
		}
		manifest, _, err := composer.ComposeAndPublish(
			ctx,
			mo,
			delivery.DestinationKey("pacquiao-broner-recap"),
			"boxing",
			"pacquiao-broner",
			"pacquiao-broner-recap",
		)
		if err != nil {
			t.Fatalf("compose: %v", err)
		}

		if len(manifest.Scenes) != 4 {
			t.Fatalf("manifest.scenes: want 4, got %d", len(manifest.Scenes))
		}
		distinctClipIDs := make(map[string]struct{}, 4)
		for i, sc := range manifest.Scenes {
			if sc.Clip.ClipID == "" {
				t.Errorf("scenes[%d].clip.clip_id empty", i)
			}
			if sc.Clip.DriveLink == "" {
				t.Errorf("scenes[%d].clip.drive_link empty", i)
			}
			if sc.Clip.StartMs >= sc.Clip.EndMs {
				t.Errorf("scenes[%d].clip time range invalid: start=%d end=%d",
					i, sc.Clip.StartMs, sc.Clip.EndMs)
			}
			if _, dup := distinctClipIDs[sc.Clip.ClipID]; dup {
				t.Errorf("scenes[%d].clip.clip_id %q DUPLICATES an earlier scene",
					i, sc.Clip.ClipID)
			}
			distinctClipIDs[sc.Clip.ClipID] = struct{}{}
		}
		if got := len(distinctClipIDs); got != 4 {
			t.Fatalf("distinct clip_ids in manifest: want 4, got %d",
				got)
		}
		if got := len(pub.publishes); got != 1 {
			t.Errorf("publisher.Publish calls: want 1, got %d", got)
		}
		if got := len(pub.resolves); got != 1 {
			t.Errorf("publisher.ResolveFolder calls: want 1, got %d", got)
		}
		// godlike/08 forward-prevention: the canonical
		// PublishRequest shape (Destination / LocalPath /
		// Filename / Description / AssetID / ProjectID / Group /
		// Subject) has NO RootFolderOverride field by
		// construction. The absence of that field is the strongest
		// enforcement layer — the compiler refuses callers
		// outside internal/infrastructure/ and cmd/admin/ that
		// try to set it. What remains to assert at runtime: the
		// canonical SSOT routing field is populated, so the
		// published request is traceable to its typed destination.
		for i, pr := range pub.publishes {
			if pr.Destination == "" {
				t.Errorf("publish[%d] has empty Destination (godlike/08 SSOT routing)",
					i)
			}
			if pr.AssetID != "pacquiao-broner-recap" {
				t.Errorf("publish[%d] asset_id drift (got %q, want %q)",
					i, pr.AssetID, "pacquiao-broner-recap")
			}
		}
	})
}

// checkEnvHasOnlyFields asserts that rt has exactly the named
// fields, and that none of the banned fields is present. Used to
// pin the strict-model-envelope + NarrativeClipView compile-time
// discipline from drift smuggling.
func checkEnvHasOnlyFields(t *testing.T, rt reflect.Type, fields ...string) {
	t.Helper()
	want := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		want[f] = struct{}{}
	}
	got := make(map[string]struct{}, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		got[rt.Field(i).Name] = struct{}{}
	}
	for f := range want {
		if _, ok := got[f]; !ok {
			t.Errorf("%s missing required field %q", rt.String(), f)
		}
	}
	for f := range got {
		if _, ok := want[f]; !ok {
			t.Errorf("%s has FORBIDDEN field %q (must be absent per strict envelope)",
				rt.String(), f)
		}
	}
}

// compareOrWritePrePlannerGolden checks the deterministic planner
// output against the golden snapshot. Setting UPDATE_GOLDEN=1
// regenerates the golden file in-place.
//
// The golden is the EXACT JSON the planner emits (plan.Version +
// plan.SourceHash + plan.Title + per-slot refs/anchors). Drift in
// the planner's contract therefore fails the test cleanly.
//
// The planner returns the LOCAL PORT type (*ClipPrePlan, defined
// in `package usecase`); we marshal it directly so the snapshot
// captures the canonical wire shape produced for downstream
// consumers.
func compareOrWritePrePlannerGolden(t *testing.T, plan *ClipPrePlan) {
	t.Helper()
	got, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", "pacquiao_broner_pre_planner_e2e_golden.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("regenerated golden: %s (%d bytes)", golden, len(got))
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with UPDATE_GOLDEN=1 to generate)",
			golden, err)
	}
	if string(want) != string(got) {
		// Print first differing line for fast triage.
		wl := strings.Split(string(want), "\n")
		gl := strings.Split(string(got), "\n")
		max := len(wl)
		if len(gl) > max {
			max = len(gl)
		}
		for i := 0; i < max; i++ {
			var w, g string
			if i < len(wl) {
				w = wl[i]
			}
			if i < len(gl) {
				g = gl[i]
			}
			if w != g {
				t.Fatalf("golden drift at line %d:\nwant: %q\ngot:  %q\n(run with UPDATE_GOLDEN=1 to regenerate)",
					i+1, w, g)
			}
		}
	}
}

// Package script — handler_legacy_adapters_test.go covers the
// translation logic and handler-level contracts for the deprecated
// /api/script/generate-from-clips adapter.
//
//   - PR 1 (June 2026) pins the clip-source precedence chain.
//   - PR 2 (June 2026) extends coverage to the full union + dedup
//     semantics AND the derived-from-clips audit count consumed by
//     the audit-log call.
//   - PR 3 (June 2026) adds the handler-level 400 guard via an
//     httptest-driven integration test on LegacyGenerateFromClips,
//     plus the whitespace-trimming behaviour of deriveClipIDs and
//     the audit-log emission itself captured via zaptest/observer.
package script

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	domainScript "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// TestLegacyGenerateFromClipsRequest_ToEnvelope_ClipPrecedence
// pins the PR 1 + PR 2 clip-source resolution chain.
//
// Final order: clip_ids first (in arrival order), then clips[]
// (in arrival order, deduplicated against the running set).
// Empty (post-trim) IDs are silently dropped on either side.
//
// toEnvelope continues to fall through to SourceText when both
// inputs are empty (the adapter layer faithfully preserves that
// fallback for non-clip-only endpoints like the curate path).
// PR 3's 400 guard lives at the HANDLER layer and rejects the
// payload BEFORE reaching this adapter — see
// TestLegacyGenerateFromClips_RejectsEmptyClipSelection below.
func TestLegacyGenerateFromClipsRequest_ToEnvelope_ClipPrecedence(t *testing.T) {
	t.Run("mixed_clip_ids_then_clips_union_dedup_in_arrival_order", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			ClipIDs: []string{"internal-A", "internal-B"},
			Clips: []LegacyClipInput{
				{ClipID: "drive-X"},
				{ClipID: "drive-Y"},
			},
		}
		env := req.toEnvelope()
		if env.Items[0].Source.Type != domainScript.SourceClips {
			t.Fatalf("Source.Type = %q, want %q",
				env.Items[0].Source.Type, domainScript.SourceClips)
		}
		want := []string{"internal-A", "internal-B", "drive-X", "drive-Y"}
		if !reflect.DeepEqual(env.Items[0].Source.ClipIDs, want) {
			t.Fatalf("ClipIDs = %v, want %v "+
				"(PR 2: union + dedup; clip_ids precede clips[])",
				env.Items[0].Source.ClipIDs, want)
		}
	})

	t.Run("clips_array_used_when_clip_ids_empty_in_arrival_order", func(t *testing.T) {
		// Mirrors the documented Jackie Chan payload: a clips[]
		// array of objects carrying Drive file IDs.
		req := &LegacyGenerateFromClipsRequest{
			Topic: "Jackie Chan kung fu philosophy",
			Clips: []LegacyClipInput{
				{ClipID: "1HJX8AiYk-BlhkKqly51GNtSyd8ttf3oG", Title: "clip 1"},
				{ClipID: "14AxeNGtrlzgHtz3gx5vECjmmluRbtd2R", URL: "https://example.com/2"},
			},
		}
		env := req.toEnvelope()
		if env.Items[0].Source.Type != domainScript.SourceClips {
			t.Fatalf("Source.Type = %q, want %q "+
				"(documented clips[] payload must select SourceClips)",
				env.Items[0].Source.Type, domainScript.SourceClips)
		}
		want := []string{
			"1HJX8AiYk-BlhkKqly51GNtSyd8ttf3oG",
			"14AxeNGtrlzgHtz3gx5vECjmmluRbtd2R",
		}
		if !reflect.DeepEqual(env.Items[0].Source.ClipIDs, want) {
			t.Fatalf("ClipIDs = %v, want %v "+
				"(clips[]-only: arrival order preserved)",
				env.Items[0].Source.ClipIDs, want)
		}
	})

	t.Run("empty_clip_id_entries_in_clips_are_silently_skipped", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			Clips: []LegacyClipInput{
				{ClipID: ""},
				{ClipID: "first"},
				{ClipID: ""},
				{ClipID: "second"},
			},
		}
		env := req.toEnvelope()
		if env.Items[0].Source.Type != domainScript.SourceClips {
			t.Fatalf("Source.Type = %q, want %q",
				env.Items[0].Source.Type, domainScript.SourceClips)
		}
		if !reflect.DeepEqual(env.Items[0].Source.ClipIDs, []string{"first", "second"}) {
			t.Fatalf("ClipIDs = %v, want [first second]",
				env.Items[0].Source.ClipIDs)
		}
	})

	// PR 3 (June 2026): this case is now rejected at the HANDLER
	// layer (see LegacyGenerateFromClips_RejectsEmptyClipSelection),
	// not at the toEnvelope adapter layer. The adapter still
	// faithfully falls through to SourceText for non-clip-only
	// endpoints that reuse the same toEnvelope helper.
	t.Run("empty_clips_array_falls_through_to_source_text_at_adapter", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			Topic:      "Jackie Chan kung fu philosophy",
			SourceText: "raw notes",
			Clips:      []LegacyClipInput{},
		}
		env := req.toEnvelope()
		if env.Items[0].Source.Type != domainScript.SourceText {
			t.Fatalf("Source.Type = %q, want %q "+
				"(toEnvelope adapter layer keeps SourceText fallback for non-clip-only consumers; PR 3 400 lives in the handler)",
				env.Items[0].Source.Type, domainScript.SourceText)
		}
		if len(env.Items[0].Source.ClipIDs) != 0 {
			t.Fatalf("ClipIDs = %v, want []", env.Items[0].Source.ClipIDs)
		}
	})

	t.Run("nil_clips_array_falls_through_to_source_text", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			Topic: "Jackie Chan kung fu philosophy",
		}
		env := req.toEnvelope()
		if env.Items[0].Source.Type != domainScript.SourceText {
			t.Fatalf("Source.Type = %q, want %q",
				env.Items[0].Source.Type, domainScript.SourceText)
		}
		if len(env.Items[0].Source.ClipIDs) != 0 {
			t.Fatalf("ClipIDs = %v, want []", env.Items[0].Source.ClipIDs)
		}
	})

	t.Run("clips_dedup_against_existing_clip_ids", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			ClipIDs: []string{"A", "B"},
			Clips: []LegacyClipInput{
				{ClipID: "B"}, // dup of already-in-clip_ids
				{ClipID: "C"},
				{ClipID: "D"},
			},
		}
		env := req.toEnvelope()
		want := []string{"A", "B", "C", "D"}
		if !reflect.DeepEqual(env.Items[0].Source.ClipIDs, want) {
			t.Fatalf("ClipIDs = %v, want %v "+
				"(B in clips[] must not duplicate B already in clip_ids)",
				env.Items[0].Source.ClipIDs, want)
		}
	})

	t.Run("clips_internal_duplicates_collapsed", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			Clips: []LegacyClipInput{
				{ClipID: "A"},
				{ClipID: "A"},
				{ClipID: "B"},
				{ClipID: "C"},
			},
		}
		env := req.toEnvelope()
		want := []string{"A", "B", "C"}
		if !reflect.DeepEqual(env.Items[0].Source.ClipIDs, want) {
			t.Fatalf("ClipIDs = %v, want %v "+
				"(duplicates within clips[] must collapse)",
				env.Items[0].Source.ClipIDs, want)
		}
	})

	t.Run("clip_ids_internal_duplicates_collapsed", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			ClipIDs: []string{"A", "A", "B"},
			Clips:   []LegacyClipInput{{ClipID: "C"}},
		}
		env := req.toEnvelope()
		want := []string{"A", "B", "C"}
		if !reflect.DeepEqual(env.Items[0].Source.ClipIDs, want) {
			t.Fatalf("ClipIDs = %v, want %v "+
				"(duplicates within clip_ids must collapse before clips[] is processed)",
				env.Items[0].Source.ClipIDs, want)
		}
	})
}

// TestLegacyGenerateFromClipsRequest_DeriveClipIDs_DerivedCount pins
// the audit-log count returned by deriveClipIDs. The handler uses
// this counter to emit
// `legacy_adapter: derived N clip_ids from clips array` only when the
// legacy `clips[]` array actually contributed new IDs.
func TestLegacyGenerateFromClipsRequest_DeriveClipIDs_DerivedCount(t *testing.T) {
	type tc struct {
		name        string
		clipIDs     []string
		clips       []LegacyClipInput
		wantIDs     []string
		wantDerived int
	}
	cases := []tc{
		{
			name:        "clip_ids_only_yields_zero_derived",
			clipIDs:     []string{"A", "B"},
			clips:       nil,
			wantIDs:     []string{"A", "B"},
			wantDerived: 0,
		},
		{
			name:    "clips_only_counts_every_successful_append",
			clipIDs: nil,
			clips: []LegacyClipInput{
				{ClipID: "A"}, {ClipID: "B"},
			},
			wantIDs:     []string{"A", "B"},
			wantDerived: 2,
		},
		{
			name:    "duplicates_within_clips_do_not_double_count_derived",
			clipIDs: nil,
			clips: []LegacyClipInput{
				{ClipID: "A"}, {ClipID: "A"}, {ClipID: "B"},
			},
			wantIDs:     []string{"A", "B"},
			wantDerived: 2,
		},
		{
			name:    "clips_overlap_with_clip_ids_partial_derived",
			clipIDs: []string{"A", "B"},
			clips: []LegacyClipInput{
				{ClipID: "A"},
				{ClipID: "C"},
			},
			wantIDs:     []string{"A", "B", "C"},
			wantDerived: 1,
		},
		{
			name:        "empty_inputs_give_zero",
			clipIDs:     nil,
			clips:       nil,
			wantIDs:     nil,
			wantDerived: 0,
		},
		{
			name:    "empty_clips_entries_drop_before_counting",
			clipIDs: nil,
			clips: []LegacyClipInput{
				{ClipID: ""}, {ClipID: ""}, {ClipID: "X"},
			},
			wantIDs:     []string{"X"},
			wantDerived: 1,
		},
		// PR 3-followup: all-whitespace inputs bypass the
		// pre-loop nil guard (total > 0) but the post-loop
		// nil guard recognises all-trimmed-empty and returns
		// nil — matching PR 2's reviewer-intent asymmetry
		// ("prefer nil over an empty non-nil slice").
		{
			name:        "all_whitespace_clip_ids_return_nil",
			clipIDs:     []string{" ", "\t", "\n", "  "},
			clips:       nil,
			wantIDs:     nil,
			wantDerived: 0,
		},
		{
			name:    "all_whitespace_clips_array_returns_nil",
			clipIDs: nil,
			clips: []LegacyClipInput{
				{ClipID: " "}, {ClipID: "\t"}, {ClipID: "\n"},
			},
			wantIDs:     nil,
			wantDerived: 0,
		},
		{
			name:    "all_whitespace_split_between_inputs_returns_nil",
			clipIDs: []string{" ", "\t"},
			clips: []LegacyClipInput{
				{ClipID: "\n"}, {ClipID: "  "},
			},
			wantIDs:     nil,
			wantDerived: 0,
		},
		// PR 3 (June 2026): whitespace-only IDs from PR 2's
		// loophole are now treated as effectively empty by
		// deriveClipIDs (see handler_legacy_adapters.go).
		{
			name:    "whitespace_only_clips_drop_before_counting",
			clipIDs: nil,
			clips: []LegacyClipInput{
				{ClipID: " "}, {ClipID: "\t"}, {ClipID: "\n"}, {ClipID: "X"},
			},
			wantIDs:     []string{"X"},
			wantDerived: 1, // only "X" survives the trim+empty check
		},
		{
			name:    "clips_overlap_with_other_whitespace_forms_partial_derived",
			clipIDs: []string{"A"},
			clips: []LegacyClipInput{
				{ClipID: "  A  "}, // trim == "A" → dedup against clip_ids
				{ClipID: "\tB\t"}, // trim == "B" → contributes
			},
			wantIDs:     []string{"A", "B"},
			wantDerived: 1,
		},
		{
			name:        "id_with_surrounding_whitespace_is_trimmed",
			clipIDs:     []string{"  valid-id  ", "\tspaced\t"},
			clips:       nil,
			wantIDs:     []string{"valid-id", "spaced"},
			wantDerived: 0,
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			req := &LegacyGenerateFromClipsRequest{
				ClipIDs: c.clipIDs,
				Clips:   c.clips,
			}
			gotIDs, gotDerived := req.deriveClipIDs()
			if !reflect.DeepEqual(gotIDs, c.wantIDs) {
				t.Fatalf("ClipIDs = %v, want %v", gotIDs, c.wantIDs)
			}
			if gotDerived != c.wantDerived {
				t.Fatalf("derived = %d, want %d "+
					"(audit-log count must attribute clip-id provenance)",
					gotDerived, c.wantDerived)
			}
		})
	}
}

// TestLegacyGenerateFromClips_RejectsEmptyClipSelection pins the
// PR 3 handler-level 400 contract. Drives the handler via
// httptest.NewRecorder + a Gin engine so the test exercises the
// real BindJSON → deriveClipIDs → JSON-response path.
//
// ScriptFlowHandler is constructed with only `log: zap.NewNop()`;
// jobsSvc is intentionally nil. The 400 path bypasses
// enqueueDeprecated entirely, so nil jobsSvc is safe. The
// "valid payload proceeds past the guard" assertion uses the
// 503 jobsSvc-not-initialized response from enqueueDeprecated to
// PROVE the 400 guard did not fire — that is the observable
// signal that the guard let a valid payload through.
//
// PR 3 followup: whitespace-only IDs (from PR 2's `id == ""`
// loophole) are added as a separate subtest so PR 3's 400 fires
// on payloads that consist entirely of whitespace IDs.
func TestLegacyGenerateFromClips_RejectsEmptyClipSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	h := &ScriptFlowHandler{log: zap.NewNop()}
	router.POST("/legacy-clips", h.LegacyGenerateFromClips)

	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/legacy-clips",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}

	// wantClipRequiredMsg is the canonical PR 3 message substring
	// that must appear in every 400-from-PR3 body.
	const wantClipRequiredMsg = "requires at least one clip_id"

	t.Run("topic_only_payload_returns_400_with_clip_required_message", func(t *testing.T) {
		w := post(`{"topic":"Jackie Chan kung fu philosophy"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s",
				w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), wantClipRequiredMsg) {
			t.Fatalf("body = %q, want contains %q "+
				"(PR 3: 400 must surface the clip-required message)",
				w.Body.String(), wantClipRequiredMsg)
		}
	})

	t.Run("empty_clip_ids_and_clips_returns_400", func(t *testing.T) {
		w := post(`{"topic":"X","clip_ids":[],"clips":[]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s",
				w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), wantClipRequiredMsg) {
			t.Fatalf("body = %q, want contains %q",
				w.Body.String(), wantClipRequiredMsg)
		}
	})

	t.Run("only_empty_id_strings_returns_400", func(t *testing.T) {
		// clip_ids=["",""]  → deriveClipIDs skips both empties
		// → returns nil      → PR 3 fires
		// clips=[{title:"no-id"}] → no ClipID → skipped
		w := post(`{"topic":"X","clip_ids":["",""],"clips":[{"title":"no-id"}]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s",
				w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), wantClipRequiredMsg) {
			t.Fatalf("body = %q, want contains %q",
				w.Body.String(), wantClipRequiredMsg)
		}
	})

	// PR 3 followup: whitespace-only IDs must also drive the 400
	// guard. Without the whitespace trim in deriveClipIDs, the
	// strict `len(clipIDs) == 0` check would NOT fire on
	// `clip_ids: [" "]` because the slice would still have one
	// element. This subtest pins the post-followup behaviour so
	// any future "remove the trim to fix something else" change
	// fails CI.
	t.Run("only_whitespace_id_strings_returns_400", func(t *testing.T) {
		w := post(`{"topic":"X","clip_ids":[" ","\t","\n"],"clips":[{"clip_id":" "}]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s",
				w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), wantClipRequiredMsg) {
			t.Fatalf("body = %q, want contains %q",
				w.Body.String(), wantClipRequiredMsg)
		}
	})

	t.Run("valid_clips_payload_passes_guard_to_enqueue_layer", func(t *testing.T) {
		// jobsSvc is nil → enqueueDeprecated returns 503. The
		// observable status code being NOT 400 is the proof
		// that PR 3's guard let the payload through.
		w := post(`{"clips":[{"clip_id":"1HJX8AiYk-BlhkKqly51GNtSyd8ttf3oG"}]}`)
		if w.Code == http.StatusBadRequest {
			t.Fatalf("status = %d, want NOT 400 (PR 3 guard fired); body=%s",
				w.Code, w.Body.String())
		}
		// Sanity: the response reached the enqueue layer, so
		// the 503 should carry the canonical jobs-not-initialized
		// marker.
		if !strings.Contains(w.Body.String(), "jobs service not initialized") {
			t.Fatalf("body = %q, want contains 'jobs service not initialized' "+
				"(proof the handler reached enqueueDeprecated past the PR 3 guard)",
				w.Body.String())
		}
	})

	t.Run("x_deprecated_header_set_even_on_400", func(t *testing.T) {
		// The X-Deprecated header must be added BEFORE BindJSON
		// (via addDeprecationHeader at the top of the handler)
		// so the audit marker survives a payload rejected by
		// PR 3's guard. Clients probing the deprecated endpoint
		// with bad payloads still surface in the audit log.
		w := post(`{"topic":"X"}`)
		if got := w.Header().Get("X-Deprecated"); got != "true" {
			t.Fatalf("X-Deprecated = %q, want 'true'", got)
		}
	})

	t.Run("bindjson_invalid_returns_400_with_invalid_payload_marker", func(t *testing.T) {
		// Malformed JSON must fail in BindJSON (separate 400
		// path, NOT PR 3's clip-required message), to keep the
		// two rejection modes distinguishable.
		w := post(`{ not json`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}
		if !strings.Contains(w.Body.String(), "invalid payload") {
			t.Fatalf("body = %q, want contains 'invalid payload' "+
				"(BindJSON-invariance: malformed JSON must surface in 'invalid payload' path, not PR 3's clip-required path)",
				w.Body.String())
		}
		if strings.Contains(w.Body.String(), wantClipRequiredMsg) {
			t.Fatalf("body = %q must NOT contain %q "+
				"(BindJSON failure must NOT also fire PR 3's guard)",
				w.Body.String(), wantClipRequiredMsg)
		}
	})
}

// TestLegacyGenerateFromClips_AuditLogEmitted pins the PR 2 audit-log
// emission contract directly. Previously the audit log was only
// indirectly exercised by the deriveClipIDs unit test (which checks
// the COUNT, not the actual `h.log.Info` call). Without this test,
// a future "quiet cleanup" PR could delete the audit block and no
// test would fail. Using zaptest/observer lets us capture log
// entries and inspect both the message and the typed fields.
func TestLegacyGenerateFromClips_AuditLogEmitted(t *testing.T) {
	// wantAuditMessage is the canonical message prefix that all
	// clipped-from-clips requests MUST produce in the audit log.
	const wantAuditMessage = "legacy_adapter: derived clip_ids from clips array"

	newRouterWithObservedLogger := func() (*gin.Engine, *observer.ObservedLogs) {
		core, recorded := observer.New(zap.InfoLevel)
		log := zap.New(core)
		router := gin.New()
		h := &ScriptFlowHandler{log: log}
		router.POST("/legacy-clips", h.LegacyGenerateFromClips)
		return router, recorded
	}

	post := func(router *gin.Engine, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/legacy-clips",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}

	t.Run("mixed_clip_ids_and_clips_fires_audit_log_with_derived_total_fields", func(t *testing.T) {
		router, recorded := newRouterWithObservedLogger()

		// clip_ids=[internal-A] + clips=[drive-X,drive-Y] →
		// derived=2 (both clips[] entries contribute),
		// total=3 (1 from clip_ids + 2 from clips[]).
		w := post(router,
			`{"clip_ids":["internal-A"],"clips":[{"clip_id":"drive-X"},{"clip_id":"drive-Y"}]}`)

		// jobsSvc is nil → 503 from enqueue layer is OK; the
		// important thing is the status is NOT 400 (proof the
		// guard passed and the audit block was reached).
		if w.Code == http.StatusBadRequest {
			t.Fatalf("status = %d, want NOT 400 (guard fired); body=%s",
				w.Code, w.Body.String())
		}

		entries := recorded.TakeAll()
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1; entries=%v",
				len(entries), entries)
		}
		entry := entries[0]
		if entry.Level != zap.InfoLevel {
			t.Fatalf("Level = %v, want InfoLevel", entry.Level)
		}
		if !strings.Contains(entry.Message, wantAuditMessage) {
			t.Fatalf("Message = %q, want contains %q",
				entry.Message, wantAuditMessage)
		}

		// Assert the typed fields — derived=2, total=3.
		var derivedFound, totalFound bool
		for _, f := range entry.Context {
			switch f.Key {
			case "derived":
				if f.Integer != 2 {
					t.Fatalf("derived = %d, want 2", f.Integer)
				}
				derivedFound = true
			case "total":
				if f.Integer != 3 {
					t.Fatalf("total = %d, want 3", f.Integer)
				}
				totalFound = true
			}
		}
		if !derivedFound {
			t.Fatalf("derived field missing from fields=%v", entry.Context)
		}
		if !totalFound {
			t.Fatalf("total field missing from fields=%v", entry.Context)
		}
	})

	t.Run("clip_ids_only_does_not_emit_audit_log", func(t *testing.T) {
		router, recorded := newRouterWithObservedLogger()

		// clip_ids=[internal-A, internal-B], no clips[] →
		// derived=0 → audit log MUST NOT fire.
		// jobsSvc is nil → handler reaches enqueueDeprecated,
		// which returns 503 jobs-not-initialized. We assert
		// the body content (not the exact status code) so a
		// future enqueue-layer refactor (e.g. 503 with a
		// different message) doesn't break this audit-log
		// contract test; the actual contract under test is
		// `len(entries) == 0`.
		w := post(router,
			`{"clip_ids":["internal-A","internal-B"]}`)
		if w.Code == http.StatusBadRequest {
			t.Fatalf("status = %d, want NOT 400 (PR 3 guard fired); body=%s",
				w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "jobs service not initialized") {
			t.Fatalf("body = %q, want contains 'jobs service not initialized' "+
				"(proof handler reached enqueueDeprecated past PR 3 guard + audit block)",
				w.Body.String())
		}

		entries := recorded.TakeAll()
		if len(entries) != 0 {
			t.Fatalf("entries = %d, want 0 (clip_ids-only path must NOT emit audit log); entries=%v",
				len(entries), entries)
		}
	})

	t.Run("pr3_400_short_circuits_before_audit_log", func(t *testing.T) {
		router, recorded := newRouterWithObservedLogger()

		// PR 3's 400 must NOT emit the audit log — the
		// short-circuit must run BEFORE the audit block.
		w := post(router, `{"topic":"X"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", w.Code)
		}

		entries := recorded.TakeAll()
		if len(entries) != 0 {
			t.Fatalf("entries = %d, want 0 (PR 3 400 must NOT emit audit log); entries=%v",
				len(entries), entries)
		}
	})
}

// TestLegacyGenerateFromClipsRequest_ResolveAliases pins the PR 4
// alias-resolution helper in isolation (no handler involvement).
// The helper mutates r.GenerateSceneImages only when the canonical
// field is false AND the alias is true; pass-through fields
// (SentencesPerImage, MinQualityScore) are reported by name but
// not mutated, since they have no collision on this request type.
//
// The returned alias-name list drives the handler-level
// `legacy_alias_used` warn emission (see
// TestLegacyGenerateFromClips_LegacyAliasWarnEmitted below).
func TestLegacyGenerateFromClipsRequest_ResolveAliases(t *testing.T) {
	t.Run("empty_payload_returns_nil", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{}
		got := req.resolveAliases()
		if got != nil {
			t.Fatalf("resolveAliases() = %v, want nil (no aliases present)", got)
		}
	})

	t.Run("enable_scene_images_alone_sets_canonical_and_reports_alias", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{EnableSceneImages: true}
		got := req.resolveAliases()
		if !req.GenerateSceneImages {
			t.Fatalf("GenerateSceneImages = false; resolveAliases must set it true " +
				"when only the alias is present and canonical was false")
		}
		// Mutating the SAME struct that the test holds
		// here is intentional — resolveAliases mutates in
		// place by design (the handler calls it before
		// toEnvelope so both consume the same struct).
		want := []string{"enable_scene_images"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("aliases = %v, want %v (alias name list "+
				"drives the warn emission; order is canonical "+
				"enable→sentences→min per the resolveAliases "+
				"precedence chain)", got, want)
		}
	})

	t.Run("enable_plus_canonical_keeps_canonical_and_still_reports_alias", func(t *testing.T) {
		// Both alias AND canonical are true. Canonical
		// wins (no flip) but the alias name is STILL
		// reported so operators see adoption of the
		// documented alias shape even on correctly-shaped
		// payloads.
		req := &LegacyGenerateFromClipsRequest{
			EnableSceneImages:   true,
			GenerateSceneImages: true,
		}
		got := req.resolveAliases()
		if !req.GenerateSceneImages {
			t.Fatalf("GenerateSceneImages flipped to false; canonical must win")
		}
		if !reflect.DeepEqual(got, []string{"enable_scene_images"}) {
			t.Fatalf("aliases = %v, want [enable_scene_images] "+
				"(alias name is reported regardless of canonical value)",
				got)
		}
	})

	t.Run("sentences_per_image_reports_alias_without_mutation", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{SentencesPerImage: 8}
		got := req.resolveAliases()
		// SentencesPerImage is pass-through: toEnvelope
		// reads it later, resolveAliases doesn't mutate.
		if req.SentencesPerImage != 8 {
			t.Fatalf("SentencesPerImage mutated from 8 to %d "+
				"(resolveAliases MUST leave pass-through fields alone)",
				req.SentencesPerImage)
		}
		if !reflect.DeepEqual(got, []string{"sentences_per_image"}) {
			t.Fatalf("aliases = %v, want [sentences_per_image]", got)
		}
	})

	t.Run("min_quality_score_reports_alias_without_mutation", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{MinQualityScore: 0.7}
		got := req.resolveAliases()
		if req.MinQualityScore != 0.7 {
			t.Fatalf("MinQualityScore mutated from 0.7 to %f "+
				"(resolveAliases MUST leave pass-through fields alone)",
				req.MinQualityScore)
		}
		if !reflect.DeepEqual(got, []string{"min_quality_score"}) {
			t.Fatalf("aliases = %v, want [min_quality_score]", got)
		}
	})

	t.Run("all_three_aliases_returns_names_in_precedence_order", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			EnableSceneImages: true,
			SentencesPerImage: 12,
			MinQualityScore:   0.8,
		}
		got := req.resolveAliases()
		want := []string{"enable_scene_images", "sentences_per_image", "min_quality_score"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("aliases = %v, want %v (precedence order: "+
				"enable → sentences → min, matching resolveAliases append order)",
				got, want)
		}
		// Recompute the canonical: GenerateSceneImages was
		// false, alias was true → must be true now.
		if !req.GenerateSceneImages {
			t.Fatalf("GenerateSceneImages = false; alias should have flipped it to true")
		}
	})

	t.Run("zero_min_quality_score_does_not_report_alias", func(t *testing.T) {
		// omitempty boundary: a 0/zero float is indistinguishable
		// from absent on the wire. resolveAliases must respect
		// that boundary (only report non-zero pass-throughs).
		req := &LegacyGenerateFromClipsRequest{MinQualityScore: 0}
		got := req.resolveAliases()
		if got != nil {
			t.Fatalf("aliases = %v, want nil (zero-treated-as-absent: 0 → not reported)",
				got)
		}
	})

	t.Run("zero_sentences_per_image_does_not_report_alias", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{SentencesPerImage: 0}
		got := req.resolveAliases()
		if got != nil {
			t.Fatalf("aliases = %v, want nil (zero-treated-as-absent: 0 → not reported)",
				got)
		}
	})
}

// TestLegacyGenerateFromClipsRequest_ToEnvelope_AliasPassThrough pins
// the toEnvelope side of PR 4: the alias-resolved values must end
// up on the canonical envelope slots. Verifies:
//
//   - enable_scene_images       → Output.GenerateSceneImages
//     (mutation happens inside
//     resolveAliases; toEnvelope
//     just reads the canonical)
//   - sentences_per_image        → ScriptSpec.SentencesPerImage
//     (zero is faithfully passed through)
//   - min_quality_score          → SourceSpec.MinQualityScore
//     (typed *float64, nil on zero)
//
// resolveAliases is NOT called here (the test focuses on
// toEnvelope's own mapping). The mutation effect on
// GenerateSceneImages is exercised separately in
// TestLegacyGenerateFromClipsRequest_ResolveAliases above.
func TestLegacyGenerateFromClipsRequest_ToEnvelope_AliasPassThrough(t *testing.T) {
	t.Run("enable_scene_images_propagates_to_GenerateSceneImages", func(t *testing.T) {
		// Simulate the handler's resolveAliases call so the
		// canonical field is set, mirroring production order.
		req := &LegacyGenerateFromClipsRequest{
			EnableSceneImages: true,
		}
		_ = req.resolveAliases()
		env := req.toEnvelope()
		if !env.Items[0].Output.GenerateSceneImages {
			t.Fatalf("Output.GenerateSceneImages = false, want true "+
				"(enable_scene_images alias must drive the canonical slot): got %v",
				env.Items[0].Output.GenerateSceneImages)
		}
	})

	t.Run("sentences_per_image_propagates_to_ScriptSpec_SentencesPerImage", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			SentencesPerImage: 7,
		}
		env := req.toEnvelope()
		if env.Items[0].ScriptParams.SentencesPerImage != 7 {
			t.Fatalf("ScriptParams.SentencesPerImage = %d, want 7 "+
				"(sentences_per_image alias must pass through to the typed ScriptSpec slot)",
				env.Items[0].ScriptParams.SentencesPerImage)
		}
	})

	t.Run("sentences_per_image_zero_faithfully_passes_through_as_zero", func(t *testing.T) {
		// A zero SentencesPerImage is a legitimate
		// "use the normalizer default" signal — toEnvelope
		// must propagate it as-is (the normalizer's
		// precedence chain caller > preset > config > safety
		// handles 0 → config default elsewhere).
		req := &LegacyGenerateFromClipsRequest{
			SentencesPerImage: 0,
		}
		env := req.toEnvelope()
		if env.Items[0].ScriptParams.SentencesPerImage != 0 {
			t.Fatalf("ScriptParams.SentencesPerImage = %d, want 0 "+
				"(zero values must pass through unmodified)",
				env.Items[0].ScriptParams.SentencesPerImage)
		}
	})

	t.Run("min_quality_score_propagates_to_typed_float64_on_SourceSpec", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			MinQualityScore: 0.6,
		}
		env := req.toEnvelope()
		if env.Items[0].Source.MinQualityScore == nil {
			t.Fatalf("Source.MinQualityScore = nil, want &0.6 " +
				"(min_quality_score alias must populate the typed *float64 slot)")
		}
		if *env.Items[0].Source.MinQualityScore != 0.6 {
			t.Fatalf("Source.MinQualityScore = %f, want 0.6",
				*env.Items[0].Source.MinQualityScore)
		}
	})

	t.Run("min_quality_score_zero_is_omitted_from_SourceSpec", func(t *testing.T) {
		// Zero treated as absent: the typed *float64 stays nil
		// so downstream quality filter is unconstrained.
		req := &LegacyGenerateFromClipsRequest{
			MinQualityScore: 0,
		}
		env := req.toEnvelope()
		if env.Items[0].Source.MinQualityScore != nil {
			t.Fatalf("Source.MinQualityScore = %f, want nil "+
				"(zero is treated as absent and the slot stays nil to leave "+
				"the quality filter unconstrained)",
				*env.Items[0].Source.MinQualityScore)
		}
	})

	t.Run("all_three_aliases_pass_through_together", func(t *testing.T) {
		req := &LegacyGenerateFromClipsRequest{
			EnableSceneImages: true,
			SentencesPerImage: 6,
			MinQualityScore:   0.5,
		}
		_ = req.resolveAliases()
		env := req.toEnvelope()

		if !env.Items[0].Output.GenerateSceneImages {
			t.Fatalf("Output.GenerateSceneImages = false, want true (enable_scene_images)")
		}
		if env.Items[0].ScriptParams.SentencesPerImage != 6 {
			t.Fatalf("ScriptParams.SentencesPerImage = %d, want 6",
				env.Items[0].ScriptParams.SentencesPerImage)
		}
		if env.Items[0].Source.MinQualityScore == nil ||
			*env.Items[0].Source.MinQualityScore != 0.5 {
			var got interface{}
			if env.Items[0].Source.MinQualityScore != nil {
				got = *env.Items[0].Source.MinQualityScore
			}
			t.Fatalf("Source.MinQualityScore = %v, want 0.5", got)
		}
	})
}

func TestLegacyGenerateWithImagesRequest_ToEnvelope_GeneratesDocumentByDefault(t *testing.T) {
	t.Run("default_enables_generate_document", func(t *testing.T) {
		req := &LegacyGenerateWithImagesRequest{
			Title:    "with-images-doc-default",
			Topic:    "doc default",
			Language: "en",
			Tone:     "informative",
			Model:    "gemma2:2b",
		}
		env := req.toEnvelope()
		if env.Preset != domainScript.PresetWithImages {
			t.Fatalf("preset = %q, want %q", env.Preset, domainScript.PresetWithImages)
		}
		if !env.Items[0].Output.GenerateDocument {
			t.Fatal("generate_document should default to true for generate-with-images")
		}
		if !env.Items[0].Output.GenerateSceneImages {
			t.Fatal("generate_scene_images should remain true")
		}
	})

	t.Run("explicit_false_is_respected", func(t *testing.T) {
		falseVal := false
		req := &LegacyGenerateWithImagesRequest{
			Title:            "with-images-doc-off",
			Topic:            "doc override",
			Language:         "en",
			Tone:             "informative",
			Model:            "gemma2:2b",
			GenerateDocument: &falseVal,
		}
		env := req.toEnvelope()
		if env.Items[0].Output.GenerateDocument {
			t.Fatal("generate_document=false should be respected when explicitly provided")
		}
	})
}

// TestLegacyGenerateFromClips_LegacyAliasWarnEmitted pins the PR 4
// warn-emission contract end-to-end through the HTTP handler. A
// payload carrying any of the three documented alias fields MUST
// trigger a `legacy_alias_used` warn entry on the request logger,
// one entry per recognised alias, carrying the alias name and the
// endpoint name as typed fields. PR 4 is a temporary compatibility
// layer; this warn is the operator-facing signal that drives the
// cutover PR (#9) once adoption trends go to zero.
//
// Like the PR 2 audit log test, zaptest/observer captures entry
// details directly so any future "quiet cleanup" PR that drops
// the warn call (or its fields) fails CI.
func TestLegacyGenerateFromClips_LegacyAliasWarnEmitted(t *testing.T) {
	const wantWarnMessage = "legacy_alias_used"
	const wantEndpoint = "generate-from-clips"

	newRouterWithObservedLogger := func(level zapcore.LevelEnabler) (*gin.Engine, *observer.ObservedLogs) {
		core, recorded := observer.New(level)
		log := zap.New(core)
		router := gin.New()
		h := &ScriptFlowHandler{log: log}
		router.POST("/legacy-clips", h.LegacyGenerateFromClips)
		return router, recorded
	}

	post := func(router *gin.Engine, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/legacy-clips",
			strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		return w
	}

	// aliasFields returns the (alias, endpoint) pairs of typed
	// fields attached to a single observed log entry.
	aliasFields := func(entry observer.LoggedEntry) (alias, endpoint string, ok bool) {
		for _, f := range entry.Context {
			if f.Key == "alias" {
				alias = f.String
			}
			if f.Key == "endpoint" {
				endpoint = f.String
			}
		}
		ok = alias != "" && endpoint != ""
		return
	}

	t.Run("enable_scene_images_alone_fires_one_warn_with_alias_and_endpoint_fields", func(t *testing.T) {
		router, recorded := newRouterWithObservedLogger(zap.WarnLevel)
		w := post(router,
			`{"clip_ids":["X"],"enable_scene_images":true}`)
		if w.Code == http.StatusBadRequest {
			t.Fatalf("status = %d, want NOT 400 (guard fired); body=%s",
				w.Code, w.Body.String())
		}

		entries := recorded.FilterMessageSnippet(wantWarnMessage).TakeAll()
		if len(entries) != 1 {
			t.Fatalf("entries = %d, want 1; entries=%v",
				len(entries), entries)
		}
		entry := entries[0]
		if entry.Level != zap.WarnLevel {
			t.Fatalf("Level = %v, want WarnLevel",
				entry.Level)
		}
		alias, ep, ok := aliasFields(entry)
		if !ok {
			t.Fatalf("entry missing alias/endpoint fields; fields=%v",
				entry.Context)
		}
		if alias != "enable_scene_images" {
			t.Fatalf("alias = %q, want 'enable_scene_images'", alias)
		}
		if ep != wantEndpoint {
			t.Fatalf("endpoint = %q, want %q", ep, wantEndpoint)
		}
	})

	t.Run("all_three_aliases_fire_three_warns_in_precedence_order", func(t *testing.T) {
		router, recorded := newRouterWithObservedLogger(zap.WarnLevel)
		w := post(router,
			`{"clip_ids":["X"],"enable_scene_images":true,"sentences_per_image":7,"min_quality_score":0.6}`)
		if w.Code == http.StatusBadRequest {
			t.Fatalf("status = %d, want NOT 400; body=%s",
				w.Code, w.Body.String())
		}

		entries := recorded.FilterMessageSnippet(wantWarnMessage).TakeAll()
		if len(entries) != 3 {
			t.Fatalf("entries = %d, want 3 (one per recognised alias); entries=%v",
				len(entries), entries)
		}

		want := []string{
			"enable_scene_images",
			"sentences_per_image",
			"min_quality_score",
		}
		for i, entry := range entries {
			alias, ep, ok := aliasFields(entry)
			if !ok {
				t.Fatalf("entry[%d] missing alias/endpoint fields; fields=%v",
					i, entry.Context)
			}
			if alias != want[i] {
				t.Fatalf("entry[%d].alias = %q, want %q (precedence order)",
					i, alias, want[i])
			}
			if ep != wantEndpoint {
				t.Fatalf("entry[%d].endpoint = %q, want %q",
					i, ep, wantEndpoint)
			}
		}
	})

	t.Run("no_aliases_does_not_fire_warn", func(t *testing.T) {
		router, recorded := newRouterWithObservedLogger(zap.WarnLevel)
		w := post(router,
			`{"clip_ids":["X"]}`)
		if w.Code == http.StatusBadRequest {
			t.Fatalf("status = %d, want NOT 400; body=%s",
				w.Code, w.Body.String())
		}
		// Audit log may fire (PR 2 + PR 3 wiring); only one
		// alias-warn entry is the contract under test.
		warns := recorded.FilterMessageSnippet(wantWarnMessage).TakeAll()
		if len(warns) != 0 {
			t.Fatalf("warn entries = %d, want 0 (no aliases present); entries=%v",
				len(warns), warns)
		}
	})

	t.Run("pr3_400_does_not_fire_alias_warn", func(t *testing.T) {
		// PR 3's 400 short-circuits BEFORE resolveAliases +
		// alias warn; even if the payload carries alias
		// fields, they must NOT log warns. This keeps
		// adoption-tracker counts clean of rejected payloads.
		router, recorded := newRouterWithObservedLogger(zap.WarnLevel)
		w := post(router,
			`{"topic":"X","enable_scene_images":true,"sentences_per_image":7,"min_quality_score":0.6}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (PR 3 guard); body=%s",
				w.Code, w.Body.String())
		}
		warns := recorded.FilterMessageSnippet(wantWarnMessage).TakeAll()
		if len(warns) != 0 {
			t.Fatalf("warn entries = %d, want 0 (PR 3 400 must NOT fire alias warns); "+
				"rejected payloads must not pollute adoption stats; entries=%v",
				len(warns), warns)
		}
	})

	t.Run("zero_value_pass_through_aliases_do_not_fire_warn", func(t *testing.T) {
		// Aliases with zero are treated as absent (omitempty
		// boundary). resolveAliases returns nil → no warns.
		router, recorded := newRouterWithObservedLogger(zap.WarnLevel)
		w := post(router,
			`{"clip_ids":["X"],"sentences_per_image":0,"min_quality_score":0}`)
		if w.Code == http.StatusBadRequest {
			t.Fatalf("status = %d, want NOT 400; body=%s",
				w.Code, w.Body.String())
		}
		warns := recorded.FilterMessageSnippet(wantWarnMessage).TakeAll()
		if len(warns) != 0 {
			t.Fatalf("warn entries = %d, want 0 (zero-value aliases are absent); entries=%v",
				len(warns), warns)
		}
	})
}

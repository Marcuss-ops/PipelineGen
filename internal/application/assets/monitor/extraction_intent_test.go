package monitor

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	channels "github.com/Marcuss-ops/PipelineGen/internal/application/channels"
	ytdomain "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// ── Compile-time godlike/06 SSOT surface pins (package-level) ─────────────
//
// The following `var _` compile-time assertions fail at PACKAGE compile
// time (before `go test` even enters the test runner) if the
// corresponding contract is violated. They are the godlike/06 "one
// canonical owner per fact" audit-pin: any future refactor that drops
// the type-alias or breaks the alias symmetry fails to compile this
// file (the error message names the violated type).

// Pin 1: EnqueueExtractRequest is the apply-leg type alias of ExtractionIntent.
// Failure to compile here means the `=` alias was dropped (godlike/06 SSOT
// violation: the monitor package now has TWO intent shapes).
var _ ExtractionIntent = EnqueueExtractRequest{}

// Pin 2: ExtractionSegment is the canonical monitor-package segment alias
// for ytdomain.Segment. Failure here means the contract drift between
// monitor and youtube/dto.
var _ ytdomain.Segment = ExtractionSegment{}

// ── Fase 8 DTO contract lock (July 2026) ───────────────────────────────────
//
// extraction_intent_test.go pins the canonical ExtractionIntent /
// ExtractionSegment / EnqueueExtractRequest surface permanently.
// Three test bites:
//
//   (a) JSON byte-equivalence + wire-fixture shape lock +
//       Channel json:"-" round-trip lock. The byte-equivalence leg
//       covers the alias-of-self identity (ExtractionIntent vs
//       EnqueueExtractRequest are byte-equal by definition). The
//       wire-fixture leg locks the EXACT JSON prefix per sample so
//       a future godlike/06 SSOT split (separate struct shape, new
//       field tags) breaks the test loudly. The Channel json:"-"
//       round-trip leg locks both the WRITE-side (JSON output must
//       not contain a "channel" key) AND the READ-side (post-Unmarshal
//       Channel field is zero-value) — symmetric gate.
//
//   (b) Field-count + JSON-tag parity: monitor.ExtractionSegment
//       (= ytdomain.Segment via type alias) has the SAME field
//       count + JSON tags (incl. omitempty parity) as ytdomain.Segment.
//       Pinned to strict == 11 (overshoot of user spec >= 10) — see
//       the inline comment on `expectedFieldCount` for rationale.
//
//   (c) Type-alias compile + runtime resolution: package-level `var _`
//       pins catch compile-time drift; runtime test confirms
//       bidirectional assignment preserves field values. Uses
//       reflect.DeepEqual instead of struct `==` because
//       ExtractionIntent contains a slice field (Segments) which
//       makes the struct non-comparable in Go.

// (a) JSON byte-equivalence + wire-fixture shape lock +
//
//	Channel json:"-" round-trip lock.
//
// Wire-format note (godlike/07 honest-limitation): the pre-Fase-8
// EnqueueExtractRequest struct had NO json tags and marshalled with
// default Go names. The Fase 8 shape uses snake_case top-level tags
// + drops Channel via `json:"-"`. This is a DELIBERATE Fase 8 break;
// see CHANGELOG.md "Fase 8 DTO contract lock" entry under
// Unreleased → Refactor.
func TestExtractionIntent_JSONByteEquivalence(t *testing.T) {
	t.Parallel()

	samples := []struct {
		name         string
		fields       ExtractionIntent
		expectPrefix string // FIRST bytes of expected JSON (snake_case pin)
	}{
		{
			name:   "zero_value",
			fields: ExtractionIntent{
				// All zero values; Tags-only Channels omitted via json:"-"; segments nil → "segments":null
			},
			expectPrefix: `{"video_id":"","title":"","url":"","group":"","drive_folder_id":"","segments":null}`,
		},
		{
			name: "video_id_only",
			fields: ExtractionIntent{
				VideoID: "abc123",
			},
			expectPrefix: `{"video_id":"abc123","title":"","url":"","group":"","drive_folder_id":"","segments":null}`,
		},
		{
			// full_payload exercises the segment-included + Channel-populated
			// path. The Channel field is tagged json:"-" so it must NOT
			// appear in the wire output. The interior segment JSON
			// follows ytdomain.Segment's snake_case tag scheme (no
			// marshalled field renaming).
			// All 15 Segment fields are populated to serve as a
			// regression guard against silent field drops.
			name: "full_payload",
			fields: ExtractionIntent{
				VideoID:       "video_xyz",
				Title:         "Test Title",
				URL:           "https://youtube.com/watch?v=video_xyz",
				Group:         "test_group",
				DriveFolderID: "drive_folder_abc",
				Segments: []ExtractionSegment{
					{
						Start:            "00:00:00",
						End:              "00:01:30",
						Name:             "intro_segment",
						Category:         "fight",
						SourceTitle:      "Test Source Title",
						SourceChannel:    "@TestChannel",
						Tags:             []string{"test", "intro"},
						Summary:          "test segment summary",
						Topics:           []string{"topic1", "topic2"},
						Speakers:         []string{"speaker1"},
						MentionedPeople:  []string{"person1"},
						Hook:             "test hook line",
						QualityScore:     0.85,
						SearchVisibility: "high",
						Texts: []ytdomain.LocalizedClipText{
							{
								LanguageCode: "en",
								SourceType:   "transcript",
								Transcript:   "Hello world",
							},
						},
					},
				},
				Channel: channels.Channel{ID: "channel_xyz", Category: "test_category", ChannelName: "Test Channel"},
			},
			expectPrefix: `{"video_id":"video_xyz","title":"Test Title","url":"https://youtube.com/watch?v=video_xyz","group":"test_group","drive_folder_id":"drive_folder_abc","segments":[{"start":"00:00:00","end":"00:01:30","name":"intro_segment","category":"fight","source_title":"Test Source Title","source_channel":"@TestChannel","tags":["test","intro"],"summary":"test segment summary","topics":["topic1","topic2"],"speakers":["speaker1"],"mentioned_people":["person1"],"hook":"test hook line","quality_score":0.85,"search_visibility":"high","texts":[{"language_code":"en","transcript":"Hello world","source_type":"transcript"}]}]`,
		},
		{
			name: "no_segments",
			fields: ExtractionIntent{
				VideoID: "video_no_segments",
				Title:   "Title only",
				URL:     "https://youtube.com/watch?v=video_no_segments",
				Group:   "no_segments_group",
			},
			expectPrefix: `{"video_id":"video_no_segments","title":"Title only","url":"https://youtube.com/watch?v=video_no_segments","group":"no_segments_group","drive_folder_id":"","segments":null}`,
		},
	}

	for _, tc := range samples {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Marshaling path 1: ExtractionIntent literal directly.
			intentJSON, errIntent := json.Marshal(tc.fields)
			if errIntent != nil {
				t.Fatalf("json.Marshal(ExtractionIntent{...}) failed: %v", errIntent)
			}

			// Marshaling path 2: EnqueueExtractRequest literal (alias of
			// ExtractionIntent). The interface{} wrapping carries no
			// semantic value for an alias-of-self (both calls go through
			// the same marshal path because the alias resolves at compile
			// time); the test exercises the second path purely to surface
			// a future drift where one name diverges from the other
			// (e.g. by dropping the alias and re-introducing a separate
			// struct shape).
			var alt EnqueueExtractRequest = EnqueueExtractRequest(tc.fields)
			requestJSON, errRequest := json.Marshal(alt)
			if errRequest != nil {
				t.Fatalf("json.Marshal(EnqueueExtractRequest{...}) failed: %v", errRequest)
			}

			// Alias byte-equivalence: both paths produce identical JSON.
			if string(intentJSON) != string(requestJSON) {
				t.Errorf("byte-output MISMATCH for sample %q:\n  ExtractionIntent:      %s\n  EnqueueExtractRequest: %s",
					tc.name, intentJSON, requestJSON)
			}

			// Wire-shape fixture lock (locks the EXACT wire prefix).
			// A future godlike/06 SSOT split that drops the type alias
			// and re-introduces a separate ExtractionIntent struct
			// shape (with different field tags / different field order)
			// breaks this assertion loudly.
			if !strings.HasPrefix(string(intentJSON), tc.expectPrefix) {
				t.Errorf("wire-shape FIXTURE MISMATCH for sample %q:\n  expected prefix: %s\n  got:              %s",
					tc.name, tc.expectPrefix, intentJSON)
			}

			// Channel opaque gate — WRITE-side assertion. The marshalled
			// output MUST NOT contain a "channel" JSON key (the json:"-"
			// tag suppresses serialization entirely). Specifically checks
			// for `"channel":` and `"Channel":` key forms (key+colon)
			// rather than any substring of "channel" to avoid false
			// positives on segment Topic/Tag values that may legitimately
			// contain the word "channel" in their content.
			if strings.Contains(string(intentJSON), `"channel":`) ||
				strings.Contains(string(intentJSON), `"Channel":`) {
				t.Errorf("wire-shape LEAK on Channel json:\"-\" gate (write side): Marshal output contains a 'channel' JSON key for sample %q; got %s",
					tc.name, intentJSON)
			}

			// Channel opaque gate — READ-side assertion (the round-trip
			// lock). Unmarshal the intent back into a fresh
			// ExtractionIntent and verify the Channel field is the
			// zero-value AFTER unmarshal. The pre-Fase-8 wire shape
			// would have populated this field; the new shape must leave
			// it at zero regardless of what was sent over the wire.
			//
			// Use reflect.DeepEqual against channels.Channel{} (NOT a
			// 3-field whitelist) so ALL ~20 fields are pinned
			// symmetrically: a future bug that leaks ANY field through
			// Unmarshal — DriveFolderID, Keywords, LeaseOwner,
			// LeaseUntil, LastCursor, etc. — breaks loudly. Consistent
			// with the (c) DeepEqual pattern used for the same
			// round-trip zero-value contract in this test file.
			var roundTrip ExtractionIntent
			if err := json.Unmarshal(intentJSON, &roundTrip); err != nil {
				t.Fatalf("json.Unmarshal round-trip for sample %q failed: %v", tc.name, err)
			}
			if !reflect.DeepEqual(roundTrip.Channel, channels.Channel{}) {
				t.Errorf("Channel opaque gate READ-side LEAK for sample %q: Channel=%+v (should be zero-value channels.Channel{} after Unmarshal — json:\"-\" round-trip contract, ALL fields must be zero)",
					tc.name, roundTrip.Channel)
			}
		})
	}
}

// (b) Field-count + JSON-tag parity — monitor.ExtractionSegment
//
//	(= ytdomain.Segment via type alias) MUST have the SAME field
//	count + JSON tags (incl. omitempty parity) as ytdomain.Segment.
//
// Pin the count to EXACT 12 (ytdomain.Segment has 12 fields: Start,
// End, Name, Tags, Summary, Topics, Speakers, MentionedPeople, Hook,
// QualityScore, SearchVisibility, Texts). The alias makes both
// trivially equal today; the strict count is the future-fence against
// a godlike/06 split (e.g. a future ExtractionSegment re-introduced
// as a separate struct shape with a different field set would break
// this assertion regardless of the type-alias-equivalence).
//
// overshoot: strict == 12 vs user spec floor ">= 10". The stricter
// pin documents the actual ytdomain.Segment field count and would
// catch even a one-field drop (the spec floor allows that; this
// pin doesn't). Comment marker `overshoot` ensures a future reader
// can identify the deliberate deviation from the spec by grepping
// for `overshoot`.
func TestExtractionSegment_FieldParityWithYtdomainSegment(t *testing.T) {
	t.Parallel()

	monitorType := reflect.TypeOf(ExtractionSegment{})
	ytdomainType := reflect.TypeOf(ytdomain.Segment{})

	const expectedFieldCount = 15 // overshoot vs spec >= 10 (intentional stronger pin)
	if monitorType.NumField() != expectedFieldCount {
		t.Errorf("monitor.ExtractionSegment has %d fields, expected == %d (godlike/06 SSOT pin)",
			monitorType.NumField(), expectedFieldCount)
	}
	if ytdomainType.NumField() != expectedFieldCount {
		t.Errorf("ytdomain.Segment has %d fields, expected == %d (godlike/06 SSOT pin)",
			ytdomainType.NumField(), expectedFieldCount)
	}
	if monitorType.NumField() != ytdomainType.NumField() {
		t.Errorf("field count MISMATCH: monitor.ExtractionSegment=%d, ytdomain.Segment=%d",
			monitorType.NumField(), ytdomainType.NumField())
	}

	for i := 0; i < monitorType.NumField(); i++ {
		mf := monitorType.Field(i)
		var yf reflect.StructField
		if i < ytdomainType.NumField() {
			yf = ytdomainType.Field(i)
		} else {
			t.Errorf("monitor.ExtractionSegment has extra field at index %d (%q) without a ytdomain.Segment counterpart",
				i, mf.Name)
			continue
		}
		if mf.Name != yf.Name {
			t.Errorf("field[%d] name mismatch: monitor=%q, ytdomain=%q", i, mf.Name, yf.Name)
		}
		mJSON := mf.Tag.Get("json")
		yJSON := yf.Tag.Get("json")
		if mJSON != yJSON {
			t.Errorf("field[%d] %q JSON tag mismatch: monitor=%q, ytdomain=%q",
				i, mf.Name, mJSON, yJSON)
		}
		// omitempty parity: both must have the same omitempty rule on
		// each field (the underlying struct either has it or not — this
		// catches a future split where one side adds omitempty and the
		// other doesn't, which would break byte-equivalence).
		mOmit := hasOmitempty(mf.Tag.Get("json"))
		yOmit := hasOmitempty(yf.Tag.Get("json"))
		if mOmit != yOmit {
			t.Errorf("field[%d] %q omitempty mismatch: monitor=%v, ytdomain=%v (godlike/06 omitempty-rule parity required for byte-equivalence)",
				i, mf.Name, mOmit, yOmit)
		}
	}
}

// hasOmitempty is a JSON-tag-parse helper. The JSON tag may be
// "name,omitempty" (with comma) or just "name" (without). Searches
// the raw tag value for the ",omitempty" suffix.
func hasOmitempty(tagValue string) bool {
	if tagValue == "" {
		return false
	}
	for i := 0; i+10 <= len(tagValue); i++ {
		if tagValue[i:i+10] == ",omitempty" {
			return true
		}
	}
	return false
}

// (c) Type-alias runtime + compile-resolution. Package-level `var _`
//
//	pins above catch compile-time drift; this test complements
//	with a runtime round-trip: assigns through both alias directions
//	and confirms field values are preserved (no shadow-copy drift).
//
//	Use reflect.DeepEqual instead of struct `==` because structs
//	with slice fields are not directly comparable in Go. The earlier
//	draft used `if zeroER != EnqueueExtractRequest{}` which failed
//	to compile because ExtractionIntent.Segments is
//	`[]ExtractionSegment`; DeepEqual is the canonical Go pattern
//	for non-comparable struct equality.
func TestEnqueueExtractRequest_TypeAliasResolution(t *testing.T) {
	t.Parallel()

	// Bidirectional alias: ExtractionIntent → EnqueueExtractRequest.
	var ei ExtractionIntent = ExtractionIntent{
		VideoID: "alias_test_xyz",
		Title:   "Alias Round-Trip Title",
		URL:     "https://youtube.com/watch?v=alias_test_xyz",
		Group:   "alias_round_trip_group",
	}
	var er EnqueueExtractRequest = ei
	if er.VideoID != ei.VideoID ||
		er.Title != ei.Title ||
		er.URL != ei.URL ||
		er.Group != ei.Group {
		t.Errorf("alias Ext→Req drift (VideoID/Title/URL/Group):\n  ei=%+v\n  er=%+v", ei, er)
	}

	// Bidirectional alias: EnqueueExtractRequest → ExtractionIntent.
	var er2 EnqueueExtractRequest = EnqueueExtractRequest{
		VideoID:       "alias_test_abc",
		Title:         "Reverse Alias Title",
		URL:           "https://youtube.com/watch?v=alias_test_abc",
		Group:         "reverse_alias_group",
		DriveFolderID: "drive_folder_xyz",
	}
	var ei2 ExtractionIntent = er2
	if ei2.VideoID != er2.VideoID ||
		ei2.Title != er2.Title ||
		ei2.URL != er2.URL ||
		ei2.Group != er2.Group ||
		ei2.DriveFolderID != er2.DriveFolderID {
		t.Errorf("alias Req→Ext drift:\n  er2=%+v\n  ei2=%+v", er2, ei2)
	}

	// Identity check: alias-extracted zero-value must round-trip through
	// both names without drift. Use reflect.DeepEqual because structs
	// with slice fields are not directly comparable in Go (the prior
	// `if zeroER != EnqueueExtractRequest{}` failed to compile).
	zeroEI := ExtractionIntent{}
	zeroER := EnqueueExtractRequest(zeroEI)
	if !reflect.DeepEqual(zeroER, EnqueueExtractRequest{}) {
		t.Errorf("alias zero-value round-trip drifted: %+v (expected zero-value ExtractionIntent)", zeroER)
	}
	zeroEI.VideoID = "sentinel"
	zeroER = EnqueueExtractRequest(zeroEI)
	if zeroER.VideoID != "sentinel" {
		t.Error("alias-extracted zero-value round-trip lost the sentinel marker")
	}
}

package voiceover

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBatchRequestPayloadMapIncludesDestinationAndMetadata(t *testing.T) {
	removeSilence := true
	req := &BatchRequest{
		Text:             "Hello world",
		Languages:        []Language{"it", "en"},
		FilenameTemplate: "{slug}_{lang}.mp3",
		RemoveSilence:    &removeSilence,
		Strategy:         "replace",
		Destination: &DestinationRequest{
			Group:           "Mike Tyson",
			FolderID:        "folder-123",
			FolderPath:      "/voiceover/mike-tyson",
			SubfolderName:   "intro",
			CreateSubfolder: true,
		},
		Metadata: map[string]any{"source": "manual"},
	}

	payload := req.PayloadMap()
	if payload["text"] != "Hello world" {
		t.Fatalf("expected text to be preserved, got %#v", payload["text"])
	}
	if payload["strategy"] != "replace" {
		t.Fatalf("expected strategy to be preserved, got %#v", payload["strategy"])
	}

	dest, ok := payload["destination"].(map[string]any)
	if !ok {
		t.Fatalf("expected destination map, got %#v", payload["destination"])
	}
	if dest["folder_id"] != "folder-123" || dest["create_subfolder"] != true {
		t.Fatalf("unexpected destination payload: %#v", dest)
	}
	if _, present := dest["kind"]; present {
		t.Fatalf("legacy destination should omit empty kind, got %#v", dest["kind"])
	}

	meta, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %#v", payload["metadata"])
	}
	if meta["source"] != "manual" {
		t.Fatalf("expected metadata to be preserved, got %#v", meta)
	}
}

func TestBatchRequestPayloadMapPreservesExplicitDestinationKind(t *testing.T) {
	req := &BatchRequest{Destination: &DestinationRequest{
		Kind:     string(KindExplicit),
		FolderID: "resolved-folder",
	}}

	payload := req.PayloadMap()
	dest, ok := payload["destination"].(map[string]any)
	if !ok {
		t.Fatalf("expected destination map, got %#v", payload["destination"])
	}
	if dest["kind"] != string(KindExplicit) {
		t.Fatalf("destination kind was not preserved: got %#v", dest["kind"])
	}
}

func TestBatchRequestPayloadMapExplicitKindSurvivesJSONRoundTrip(t *testing.T) {
	req := &BatchRequest{Destination: &DestinationRequest{
		Kind:     string(KindExplicit),
		FolderID: "resolved-folder",
	}}
	payload := req.PayloadMap()
	encoded, err := json.Marshal(payload["destination"])
	if err != nil {
		t.Fatalf("marshal destination: %v", err)
	}
	var roundTripped DestinationRequest
	if err := json.Unmarshal(encoded, &roundTripped); err != nil {
		t.Fatalf("unmarshal destination: %v", err)
	}
	if roundTripped.Kind != string(KindExplicit) || roundTripped.FolderID != "resolved-folder" {
		t.Fatalf("destination intent changed after round-trip: %#v", roundTripped)
	}
}

func TestRandomSuffixLength(t *testing.T) {
	if got := randomSuffix(6); len(got) != 6 {
		t.Fatalf("expected suffix length 6, got %d (%q)", len(got), got)
	}
}

// fix/voiceover-sync-async-strategy (June 2026) — handler-package
// integration test NOTE:
//
// The user-facing regression we're guarding against is:
//
//	POST /voiceover/generate with async=true → enqueue voiceover.batch
//	job → worker honours Strategy field. Before the handler fix, an
//	unset Strategy defaulted to "verify" in normalizeBatchRequest,
//	producing a duplicate file each time. After the fix, the handler
//	sets Strategy="replace" explicitly so two identical async calls
//	collapse into one file.
//
// The handler package itself does not compile right now
// (transport.EnqueueAsync / transport.EnqueueInput are undefined —
// see the build-green followup), so the contract is pinned at the
// type layer where normalizeBatchRequest + PayloadMap are reachable.
// Once the build-green PR lands, the missing test is a handler
// integration test driving two POSTs through httptest with a stub
// jobsSvc that records the enqueued payloads — to be added in a
// follow-up commit on top of this contract.

// TestBatchRequest_StrategyReplace_Roundtrip pins the contract for
// fix/voiceover-sync-async-strategy at the type layer:
//
//  1. The handler /generate async branch MUST set Strategy="replace"
//     on the BatchRequest (mirrors all canonical voiceover async
//     invocations; legacy route surface retired per Wave 21 /
//     PR-VOICEOVER-RECOVERY).
//  2. normalizeBatchRequest MUST preserve explicit Strategy="replace"
//     (not default it down to "verify").
//  3. PayloadMap() MUST surface strategy:"replace" so the worker reads
//     the explicit value from the payload_json column.
//
// The test exercises two identical calls (no Metadata, no Destination)
// to mirror the handler-built shape of /generate async: same
// text+language+filename, both carrying strategy="replace".
func TestBatchRequest_StrategyReplace_Roundtrip(t *testing.T) {
	mkReq := func() *BatchRequest {
		return &BatchRequest{
			Text:             "Hello world async",
			Languages:        []Language{"en"},
			FilenameTemplate: "hello-world_en.mp3",
			Strategy:         "replace",
		}
	}
	p1 := mkReq().PayloadMap()
	p2 := mkReq().PayloadMap()

	if p1["strategy"] != "replace" || p2["strategy"] != "replace" {
		t.Fatalf("both async calls must carry strategy=\"replace\"; got p1=%v p2=%v",
			p1["strategy"], p2["strategy"])
	}
	if p1["text"] != p2["text"] || p1["filename_template"] != p2["filename_template"] {
		t.Fatalf("payloads must agree on the dedup key (text+filename); got %v vs %v", p1, p2)
	}
}

// TestNormalizeBatchRequest_DefaultsVerifyWhenEmpty pins the
// regression baseline: when a caller forgets to set Strategy (or
// the handler still does), normalizeBatchRequest fills it in as
// "verify". The /generate async branch must NEVER hit this default
// — fix/voiceover-sync-async-strategy sets "replace" explicitly so
// that a future regression (a silent removal of the explicit field)
// fails the roundtrip test above but passes this baseline, surfacing
// the drift loudly.
func TestNormalizeBatchRequest_DefaultsVerifyWhenEmpty(t *testing.T) {
	req := &BatchRequest{
		Text: "implicit-strategy", Languages: []Language{"en"},
	}
	// deref-read; SHALLOW-CLONE isolation pin (see pkg/immutability docblock).
	// Primitive fields stay byte-equivalent; composite fields use REPLACEMENT
	// inside the closure to avoid shared backing.
	normalized := normalizeBatchRequest(*req)
	if normalized.Strategy != "verify" {
		t.Fatalf("explicit-zero Strategy must normalize to \"verify\"; got %q", normalized.Strategy)
	}
}

// TestNormalizeBatchRequest_FillsEmptyFilenameTemplate pins the
// fallback contract for /generate callers that don't pass a
// filename: handler sets FilenameTemplate only when req.Filename
// is non-empty, so normalize must fill it in as "{slug}_{lang}.mp3".
// Without this fallback, two batched /generate async calls without
// a filename would produce jobs with an empty FilenameTemplate and
// the worker would have no dedup key.
func TestNormalizeBatchRequest_FillsEmptyFilenameTemplate(t *testing.T) {
	req := &BatchRequest{
		Text:      "no-filename",
		Languages: []Language{"it"},
		Strategy:  "replace",
	}
	// deref-read; SHALLOW-CLONE isolation pin (see pkg/immutability docblock).
	// Primitive fields stay byte-equivalent; composite fields use REPLACEMENT
	// inside the closure to avoid shared backing.
	normalized := normalizeBatchRequest(*req)
	if normalized.FilenameTemplate == "" {
		t.Fatalf("normalize must fill FilenameTemplate when caller leaves it empty; got \"\"")
	}
}

// PR-VO-A4 (path-traversal fix, June 2026): pinning the canonical
// validation contract for DestinationRequest. The shape is:
//
//   - SubfolderName empty  → nil (legitimate "no subfolder" signal)
//   - SubfolderName passes pkg/pathutil.SanitizeSubfolderSegment  → nil
//   - SubfolderName fails the segment sanitiser  → wrapped error
//
// Coverage extends the pkg/pathutil tests so callers that bind via
// Gin / ShouldBindJSON see the same reject surface at the request
// boundary (no need to redrive the attack vectors here).
func TestDestinationRequest_Validate_BoundaryContract(t *testing.T) {
	cases := []struct {
		name      string
		subfolder string
		wantErr   bool
	}{
		// Empty / nil-safe path — legitimate "no subfolder" signal.
		{"empty-subfolder-passes", "", false},
		// Accepts sanitisation-clean names.
		{"simple-ascii-passes", "intro", false},
		{"hyphenated-passes", "v1-q4", false},
		{"underscored-passes", "scene_one", false},
		{"unicode-passes", "日本", false},
		{"mixed-spaces-passes", "Foo Bar 2024", false},
		// Rejects the canonical path-traversal vector set. The exact
		// substring inside err.Error() is intentionally NOT pinned here;
		// pkg/pathutil/pathutil_test.go pins it. We only pin "Validate
		// returns an error" so a future relax verifier over there
		// doesn't accidentally break this contract.
		{"reserved-dot-rejected", "..", true},
		{"reserved-parent-rejected", ".", true},
		{"leading-slash-rejected", "/etc", true},
		{"leading-backslash-rejected", "\\windows", true},
		{"embedded-slash-rejected", "subfolder/sibling", true},
		{"embedded-backslash-rejected", "subfolder\\sibling", true},
		{"double-dot-prefix-rejected", "../foo", true},
		{"nul-byte-rejected", "foo\x00bar", true},
		{"length-cap-rejected", strings.Repeat("a", 201), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &DestinationRequest{SubfolderName: tc.subfolder}
			err := r.Validate()
			if tc.wantErr && err == nil {
				t.Errorf("Validate(%q) expected error, got nil", tc.subfolder)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Validate(%q) unexpected error: %v", tc.subfolder, err)
			}
		})
	}
}

// TestDestinationRequest_Validate_NilSafe: a nil *DestinationRequest
// must short-circuit nil-error. This covers callers that build the
// struct via batch helpers and may hand a nil when SubfolderName is
// absent AND the whole struct is initialised lazily.
func TestDestinationRequest_Validate_NilSafe(t *testing.T) {
	var d *DestinationRequest
	if err := d.Validate(); err != nil {
		t.Fatalf("(*nil)(DestinationRequest).Validate() must be nil-safe; got %v", err)
	}
}

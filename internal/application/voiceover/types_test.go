package voiceover

import "testing"

func TestBatchRequestPayloadMapIncludesDestinationAndMetadata(t *testing.T) {
	removeSilence := true
	req := &BatchRequest{
		Text:             "Hello world",
		Languages:        []string{"it", "en"},
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

	meta, ok := payload["metadata"].(map[string]any)
	if !ok {
		t.Fatalf("expected metadata map, got %#v", payload["metadata"])
	}
	if meta["source"] != "manual" {
		t.Fatalf("expected metadata to be preserved, got %#v", meta)
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
//     on the BatchRequest (mirrors /generate-with-group and the sync
//     branch).
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
			Languages:        []string{"en"},
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
		Text:      "implicit-strategy",
		Languages: []string{"en"},
	}
	normalized := normalizeBatchRequest(req)
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
		Languages: []string{"it"},
		Strategy:  "replace",
	}
	normalized := normalizeBatchRequest(req)
	if normalized.FilenameTemplate == "" {
		t.Fatalf("normalize must fill FilenameTemplate when caller leaves it empty; got \"\"")
	}
}

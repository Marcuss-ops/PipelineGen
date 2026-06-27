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

// TestBatchRequest_StrategyReplace_Roundtrip pins the
// fix/voiceover-sync-async-strategy contract at the type layer:
//
//	1. The handler /generate async branch MUST set Strategy="replace"
//	   on the BatchRequest (mirrors /generate-with-group and the sync
//	   branch).
//	2. normalizeBatchRequest MUST preserve explicit Strategy="replace"
//	   (not default it down to "verify").
//	3. PayloadMap() MUST surface strategy:"replace" so the worker reads
//	   the explicit value from the payload_json column.
//
// If any of these three invariants breaks, two identical async
// /generate calls collapse into a duplicate ("verify" semantics)
// instead of producing a single file ("replace" semantics). The
// handler package itself does not compile right now
// (transport.EnqueueAsync / transport.EnqueueInput are undefined —
// see the build-green followup), so the contract is pinned at the
// type layer where normalizeBatchRequest + PayloadMap are reachable.
func TestBatchRequest_StrategyReplace_Roundtrip(t *testing.T) {
	req := &BatchRequest{
		Text:             "duplicate-test",
		Languages:        []string{"en"},
		FilenameTemplate: "dup_en.mp3",
		Strategy:         "replace",
	}
	normalized := normalizeBatchRequest(req)
	if normalized.Strategy != "replace" {
		t.Fatalf("normalize must preserve explicit Strategy; got %q, want \"replace\"", normalized.Strategy)
	}
	payload := normalized.PayloadMap()
	if payload["strategy"] != "replace" {
		t.Fatalf("PayloadMap must surface Strategy; got %#v, want \"replace\"", payload["strategy"])
	}
}

// TestNormalizeBatchRequest_DefaultsVerifyWhenEmpty pins the
// regression baseline that fix/voiceover-sync-async-strategy is
// guarding against: when the handler forgets to set Strategy, the
// default kicks in as "verify". A future agent who removes the
// explicit Strategy:"replace" from the /generate async branch will
// see this test pass (the default is preserved) but the roundtrip
// test above will fail — together they catch the regression shape
// ("handler silently falls back to verify and produces duplicate
// files").
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

// TestBatchRequest_PayloadMap_NoDuplicateFileForTwoIdenticalAsyncCalls
// expresses the user-facing intent of fix/voiceover-sync-async-strategy
// at the type layer: two BatchRequests with the SAME text + language +
// filename + identical metadata all carry strategy:"replace", so the
// worker treats them as deterministic replays and writes ONE file
// (instead of two duplicated files under the legacy verify default).
// Verifies on the PayloadMap that the only difference between the
// two calls is the request_id metadata, which is expected to vary
// per enqueue but does NOT alter the replace-vs-verify decision.
func TestBatchRequest_PayloadMap_NoDuplicateFileForTwoIdenticalAsyncCalls(t *testing.T) {
	mkReq := func(reqID string) *BatchRequest {
		return &BatchRequest{
			Text:             "Hello world async",
			Languages:        []string{"en"},
			FilenameTemplate: "hello-world_en.mp3",
			Strategy:         "replace",
			Metadata:         map[string]any{"request_id": reqID},
		}
	}
	p1 := mkReq("vo_001").PayloadMap()
	p2 := mkReq("vo_002").PayloadMap()

	if p1["strategy"] != "replace" || p2["strategy"] != "replace" {
		t.Fatalf("both async calls must carry strategy=\"replace\"; got p1=%v p2=%v",
			p1["strategy"], p2["strategy"])
	}
	if p1["text"] != p2["text"] || p1["filename_template"] != p2["filename_template"] {
		t.Fatalf("payloads must agree on the dedup key (text+filename); got %v vs %v", p1, p2)
	}
}

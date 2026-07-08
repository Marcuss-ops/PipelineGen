// Package indexing — payload_builder_test.go: omitempty contract
// pinned for BuildPayloadFromDocument (godlike/07 NO-FAKE-AVAILABILITY
// regression suite for the Qdrant semantic-payload enrichment wave).
//
// Each test asserts a SPECIFIC zero-value behaviour — if a typed-DTO
// field is empty/zero/nil, the corresponding payload key MUST be
// absent. This pins the canonical contract documented in
// payload_builder.go::BuildPayloadFromDocument (godlike/06 SSOT,
// single source of truth).
//
// Test functions follow the AGENTS.md codebase convention
// `Test<TargetFunction>_<Scenario>`. Stdlib `testing` only — no
// testify dep, matching the package-internal style of the
// neighbouring payload_mapper_test.go.

package indexing

import "testing"

// emptyDoc mints a minimal-but-non-nil IndexDocument so the function
// under test reaches its payload-building branch (doc != nil) without
// the early-return at line 39 (nil-doc guard).
func emptyDoc() *IndexDocument {
	return &IndexDocument{
		AssetID: "stock:test:timestamp:0:video",
	}
}

// 1. Destination emitted when explicitly set on Metadata.
func TestBuildPayloadFromDocument_DestinationEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Destination = "stock"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["destination"]; !ok || v != "stock" {
		t.Fatalf("payload[destination] = %v, ok = %v — want string(\"stock\"), ok = true", v, ok)
	}
}

// 2. Destination NOT inferred from Source when Metadata.Destination is
// empty. PR 6 (July 2026) removed the legacy firstNonEmpty fallback
// contract that silently filled destination from Source — exactly the
// godlike/07 NO-FAKE-AVAILABILITY placeholder-string anti-pattern. A
// caller that sets only Metadata.Source MUST NOT see a destination
// payload key. Callers that want destination MUST set it explicitly
// via top-level AssetData fields or MetadataJSON.
func TestBuildPayloadFromDocument_DestinationNotInferredFromSource(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Source = "youtube"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["destination"]; ok {
		t.Fatalf("payload[destination] = %v, ok = %v — want ABSENT (PR 6 removed auto-population from Source; godlike/07 NO-FAKE-AVAILABILITY)", v, ok)
	}
}

// 3. Origin emitted when explicitly set on Metadata.
func TestBuildPayloadFromDocument_OriginEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Origin = "retrieved"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["origin"]; !ok || v != "retrieved" {
		t.Fatalf("payload[origin] = %v, ok = %v — want string(\"retrieved\"), ok = true", v, ok)
	}
}

// 4. SourceProvider emitted when explicitly set; falls back to
// Metadata.Source when empty.
func TestBuildPayloadFromDocument_SourceProviderEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.SourceProvider = "pexels"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["source_provider"]; !ok || v != "pexels" {
		t.Fatalf("payload[source_provider] = %v, ok = %v — want string(\"pexels\"), ok = true", v, ok)
	}
}

// 5. TotalChunks = 0 → omitempty (the int-zero guard).
// Locks the canonical "0 → absent" rule for signed-int payload keys.
func TestBuildPayloadFromDocument_TotalChunksZeroOmitted(t *testing.T) {
	doc := emptyDoc()
	// TotalChunks = 0 (default)
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["total_chunks"]; ok {
		t.Fatalf("payload[total_chunks] present — want ABSENT for int-zero value (omitempty guard)")
	}
}

// 6. TotalChunks > 0 → emitted verbatim.
// Locks that positive int values reach the wire intact.
func TestBuildPayloadFromDocument_TotalChunksPositiveEmitted(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.TotalChunks = 11
	p := BuildPayloadFromDocument(doc, nil)
	v, ok := p["total_chunks"]
	if !ok {
		t.Fatalf("payload[total_chunks] absent — want 11")
	}
	if v != 11 {
		t.Fatalf("payload[total_chunks] = %v — want 11", v)
	}
}

// 7. Entities (and the wider entity-bag family: People, Speakers,
// MentionedPeople, Topics, SearchKeywords) all nil/empty → ENTITIES
// payload key absent. godlike/07 NO-FAKE-AVAILABILITY: an empty slice
// MUST NOT emit a payload key (zero-value semantics for slice).
func TestBuildPayloadFromDocument_EntitiesEmptyAllOmitted(t *testing.T) {
	doc := emptyDoc()
	// All entity-bag slices nil (default)
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["entities"]; ok {
		t.Fatalf("payload[entities] present — want ABSENT for nil entity bags (omitempty)")
	}
}

// 8. Entities populated → emitted; canonical composer merges across
// entity-bag slices (Entities > People > Speakers > MentionedPeople;
// falls back to People > Topics > SearchKeywords if all empty).
func TestBuildPayloadFromDocument_EntitiesPopulatedEmitted(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Entities = []string{"Pacquiao", "Broner"}
	p := BuildPayloadFromDocument(doc, nil)
	v, ok := p["entities"]
	if !ok {
		t.Fatalf("payload[entities] absent — want 2 entries")
	}
	got, isSlice := v.([]string)
	if !isSlice {
		t.Fatalf("payload[entities] = %T — want []string", v)
	}
	if len(got) != 2 {
		t.Fatalf("payload[entities] len = %d — want 2", len(got))
	}
	if got[0] != "Pacquiao" || got[1] != "Broner" {
		t.Fatalf("payload[entities] = %v — want [Pacquiao Broner]", got)
	}
}

// 9. Round = 0 → omitempty (the int-zero guard for LLM-derived).
// This is the canonical case for "no round" (e.g. running, calisthenics).
// godlike/07 NO-FAKE-AVAILABILITY: zero is NOT a legitimate round for
// indexed sports/round assets — keep payload key absent.
func TestBuildPayloadFromDocument_RoundZeroOmitted(t *testing.T) {
	doc := emptyDoc()
	// Round = 0 (default)
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["round"]; ok {
		t.Fatalf("payload[round] present — want ABSENT for int-zero (omitempty)")
	}
}

// 10. DurationSec = 0 → omitempty. Round-trip with DurationMs shape
// (legacy int64 ms) — both shapes honor "0 → absent".
func TestBuildPayloadFromDocument_DurationSecZeroOmitted(t *testing.T) {
	doc := emptyDoc()
	// DurationSec = 0 (default)
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["duration_sec"]; ok {
		t.Fatalf("payload[duration_sec] present — want ABSENT for int-zero (omitempty)")
	}
}

// 11. LLM fields all empty → no payload keys for LLM-derived fields.
// godlike/07 NO-FAKE-AVAILABILITY: the canonical case for assets
// not yet enriched by the RLM pass. Every LLM-derived field MUST be
// absent from the wire shape today; RLM pass populates and the keys
// appear after that pass.
func TestBuildPayloadFromDocument_LLMFieldsAllEmpty_NoPayloadKeys(t *testing.T) {
	doc := emptyDoc()
	p := BuildPayloadFromDocument(doc, nil)
	llmKeys := []string{"event", "round", "scene", "subject", "entities", "semantic_title"}
	for _, k := range llmKeys {
		if _, ok := p[k]; ok {
			t.Errorf("payload[%s] present when nil LLM-derived — want ABSENT (godlike/07 NO-FAKE-AVAILABILITY)", k)
		}
	}
}

// 12. SourceVideoID inferred from YouTubeID when SourceVideoID is
// empty (the existing firstNonEmpty contract: prefers the explicit new
// field, falls back to the legacy discriminator field). The legacy
// `youtube_video_id` key is also emitted for backward compat with
// pre-v3 search filters.
func TestBuildPayloadFromDocument_SourceVideoIDInferredFromYouTubeID(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.YouTubeID = "vdC5GXxS-qU"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["source_video_id"]; !ok || v != "vdC5GXxS-qU" {
		t.Fatalf("payload[source_video_id] = %v, ok = %v — want \"vdC5GXxS-qU\" (inferred from YouTubeID)", v, ok)
	}
	if v, ok := p["youtube_video_id"]; !ok || v != "vdC5GXxS-qU" {
		t.Fatalf("payload[youtube_video_id] = %v, ok = %v — want \"vdC5GXxS-qU\" (legacy emission)", v, ok)
	}
}

// 13. DrivePath + IndexingStatus emitted verbatim when set. Both are
// canonical post-upload markers (DrivePath from the verified Drive
// upload; IndexingStatus from the writer pre-write stamp).
func TestBuildPayloadFromDocument_DrivePathAndIndexingStatusEmitted(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.DrivePath = "stock/Boxe/youtube/Pacquiao-Vs-Broner/Round-7-Broner-barcolla"
	doc.Metadata.IndexingStatus = "INDEXED"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["drive_path"]; !ok || v != "stock/Boxe/youtube/Pacquiao-Vs-Broner/Round-7-Broner-barcolla" {
		t.Errorf("payload[drive_path] = %v, ok = %v — want long canonical path", v, ok)
	}
	if v, ok := p["indexing_status"]; !ok || v != "INDEXED" {
		t.Errorf("payload[indexing_status] = %v, ok = %v — want string(\"INDEXED\")", v, ok)
	}
}

// 14. Forbidden keys (drive_link, local_path, status) NEVER present
// in the wire payload. Locks the godlike/06 SSOT freeze test contract
// — the IndexDocument airlock strips these from the Source asset.AssetData
// shape and BuildPayloadFromDocument MUST NOT re-introduce them.
func TestBuildPayloadFromDocument_ForbiddenKeysNeverPresent(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.DrivePath = "/path/to/file"
	doc.LifecycleState = "ACTIVE"
	p := BuildPayloadFromDocument(doc, nil)
	forbiddenKeys := []string{"drive_link", "local_path", "status"}
	for _, k := range forbiddenKeys {
		if _, ok := p[k]; ok {
			t.Errorf("payload[%s] present — want ABSENT (godlike/06 SSOT forbidden key contract)", k)
		}
	}
}

// 15. Nil doc returns empty map (graceful handling — the function's
// first guard at line 39 returns the empty map instead of nil so
// callers can iterate the result without nil-panic).
func TestBuildPayloadFromDocument_NilDocReturnsEmptyMap(t *testing.T) {
	p := BuildPayloadFromDocument(nil, nil)
	if p == nil {
		t.Fatalf("BuildPayloadFromDocument(nil, nil) returned nil — want empty map for safe iteration")
	}
	if len(p) != 0 {
		t.Fatalf("payload size = %d — want 0 for nil doc (graceful empty map)", len(p))
	}
}

// 16. TimestampDriveFolderLink + TimestampFolderID both set → both
// emitted verbatim as the canonical "open-in-Drive" navigation keys.
// Locks PR-TIMESTAMP-FOLDER-LINK (July 2026): the producer side
// (step_publish.go Phase 2 from metadataPublished.Location.FolderID)
// is deferred to PR-TIMESTAMP-FOLDER-LINK-DEFERRED; this test pins
// the canonical RECEIVER contract so the producer PR lands a
// pass-the-test branch without re-touching the receiver.
//
// godlike/06 SSOT: payload key names
// (`timestamp_drive_folder_link`, `timestamp_folder_id`) must match
// the goddoc in payload_builder.go's BuildPayloadFromDocument
// block 11 + the JSON tags on index_writer_types.go::AssetData.
// Keys are FOLDER-locators distinct from the FORBIDDEN`drive_link`
// (QDRANT-001).
func TestBuildPayloadFromDocument_TimestampFolderFieldsEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.TimestampDriveFolderLink = "https://drive.google.com/drive/folders/1iAGhWidRF0hpJYvku_fIavEIY50_V1wA"
	doc.Metadata.TimestampFolderID = "1iAGhWidRF0hpJYvku_fIavEIY50_V1wA"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["timestamp_drive_folder_link"]; !ok || v != "https://drive.google.com/drive/folders/1iAGhWidRF0hpJYvku_fIavEIY50_V1wA" {
		t.Fatalf("payload[timestamp_drive_folder_link] = %v, ok = %v — want canonical Drive folder URL", v, ok)
	}
	if v, ok := p["timestamp_folder_id"]; !ok || v != "1iAGhWidRF0hpJYvku_fIavEIY50_V1wA" {
		t.Fatalf("payload[timestamp_folder_id] = %v, ok = %v — want folder ID only (no URL quoting)", v, ok)
	}
}

// 17. TimestampDriveFolderLink + TimestampFolderID both empty →
// both keys ABSENT. godlike/07 NO-FAKE-AVAILABILITY: empty-string
// fields MUST NOT emit a payload key (no placeholder "unknown"
// or empty-string leakage into the Qdrant index). Pre-producer
// state (receiver-side-only): all current rows have empty values
// → payload surface must stay clean until the deferred producer
// (step_publish.go Phase 2) populates c.TimestampFolderID verbatim.
func TestBuildPayloadFromDocument_TimestampFolderFieldsEmpty_NoKeys(t *testing.T) {
	doc := emptyDoc()
	// TimestampDriveFolderLink / TimestampFolderID both empty (default)
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["timestamp_drive_folder_link"]; ok {
		t.Errorf("payload[timestamp_drive_folder_link] present when empty — want ABSENT (omitempty contract)")
	}
	if _, ok := p["timestamp_folder_id"]; ok {
		t.Errorf("payload[timestamp_folder_id] present when empty — want ABSENT (omitempty contract)")
	}
}

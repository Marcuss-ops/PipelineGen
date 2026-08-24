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

import (
	"strings"
	"testing"
)

// emptyDoc mints a minimal-but-non-nil IndexDocument so the function
// under test reaches its payload-building branch (doc != nil) without
// the early-return at line 39 (nil-doc guard).
func emptyDoc() *IndexDocument {
	return &IndexDocument{
		AssetID: "stock:test:timestamp:0:video",
	}
}

func TestBuildPayloadFromDocument_AIStockAudioFields(t *testing.T) {
	doc := emptyDoc()
	hasDialogue := false
	doc.Metadata.Source = "ai_generated"
	doc.Metadata.AssetRole = "stock"
	doc.Metadata.NormalizedGroup = "stock"
	doc.Metadata.HasDialogue = &hasDialogue
	doc.Metadata.AudioProfile = "ambient_and_effects"
	p := BuildPayloadFromDocument(doc, nil)
	if p["source"] != "ai_generated" || p["asset_role"] != "stock" || p["normalized_group"] != "stock" || p["has_dialogue"] != false || p["audio_profile"] != "ambient_and_effects" {
		t.Fatalf("unexpected AI stock payload: %#v", p)
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

// 13b. FolderPath emitted verbatim when set. This is the canonical
// Drive folder label for folder-aware filtering/navigation.
func TestBuildPayloadFromDocument_FolderPathEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.FolderPath = "Manny Pacquiao vs Adrien Broner"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["folder_path"]; !ok || v != "Manny Pacquiao vs Adrien Broner" {
		t.Errorf("payload[folder_path] = %v, ok = %v — want canonical folder label", v, ok)
	}
}

// 13c. FolderID emitted verbatim when set. This is the canonical Drive
// folder ID for the asset's destination folder.
func TestBuildPayloadFromDocument_FolderIDEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.FolderID = "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["folder_id"]; !ok || v != "1G7MYF-EDrkoMXmDvAHbwOnaOza4f2HBJ" {
		t.Errorf("payload[folder_id] = %v, ok = %v — want canonical folder ID", v, ok)
	}
}

// 14. Forbidden keys (local_path, status) NEVER present in the wire
// payload. PR-CATALOG-MULTILINGUA step 6 (July 2026): drive_link is NO
// LONGER in this set — it is canonical in the payload. Locks the
// godlike/06 SSOT freeze test contract — the IndexDocument airlock
// strips forbidden keys from the Source asset.AssetData shape and
// BuildPayloadFromDocument MUST NOT re-introduce them. drive_link is
// emitted in step 18 below (positive test for the canonical payload
// surface).
func TestBuildPayloadFromDocument_ForbiddenKeysNeverPresent(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.DrivePath = "/path/to/file"
	doc.LifecycleState = "ACTIVE"
	p := BuildPayloadFromDocument(doc, nil)
	forbiddenKeys := []string{"local_path", "status"}
	for _, k := range forbiddenKeys {
		if _, ok := p[k]; ok {
			t.Errorf("payload[%s] present — want ABSENT (godlike/06 SSOT forbidden key contract)", k)
		}
	}
	// Explicit absence assertion: drive_link is NOT forbidden anymore.
	// The positive emission contract is pinned by step 18 below.
	if _, ok := p["drive_link"]; ok {
		t.Errorf("payload[drive_link] present without setting doc.Metadata.DriveLink — surface contract broken")
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

// 18. PR-CATALOG-MULTILINGUA step 6 (July 2026): drive_link IS now
// canonical in payload (the legacy QDRANT-001 NO-DRIVE-LINK rule is
// REPLACED per the Italian plan). Positive emission contract —
// when doc.Metadata.DriveLink is set, payload["drive_link"] is present
// and verbatim; when empty, omitempty keeps the key absent. Forward-
// prevention: drive_link is NEVER in embedding_text (pinned by test
// 21 below).
func TestBuildPayloadFromDocument_DriveLinkEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.DriveLink = "https://drive.google.com/file/d/abc123/view"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["drive_link"]; !ok || v != "https://drive.google.com/file/d/abc123/view" {
		t.Fatalf("payload[drive_link] = %v, ok = %v — want canonical open-in-Drive URL", v, ok)
	}
}
func TestBuildPayloadFromDocument_DriveFileIDAbsentWhenEmpty(t *testing.T) {
	doc := emptyDoc()
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["drive_file_id"]; ok {
		t.Errorf("payload[drive_file_id] present when empty — want ABSENT")
	}
}

func TestBuildPayloadFromDocument_DriveLinkAbsentWhenEmpty(t *testing.T) {
	doc := emptyDoc()
	// doc.Metadata.DriveLink is "" by default
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["drive_link"]; ok {
		t.Errorf("payload[drive_link] present when empty — want ABSENT (omitempty contract)")
	}
}

// 19. PR-CATALOG-MULTILINGUA step 6 (July 2026): current_semantic_hash IS
// canonical in payload. Source-of-truth precedence (godlike/06 SSOT):
//
//	(a) asset_visual_summaries.source_hash (migration 151) — the
//	    canonical VLM fingerprint when a real VLM pass has run.
//	(b) media_assets.semantic_hash (migration 152) — fallback.
//
// Positive emission contract — when doc.Metadata.CurrentSemanticHash is
// set, payload["current_semantic_hash"] is present and verbatim; when
// empty, omitempty keeps the key absent.
func TestBuildPayloadFromDocument_CurrentSemanticHashEmittedWhenSet(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.CurrentSemanticHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	p := BuildPayloadFromDocument(doc, nil)
	if v, ok := p["current_semantic_hash"]; !ok || v != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("payload[current_semantic_hash] = %v, ok = %v — want 64-char SHA-256 verbatim", v, ok)
	}
}
func TestBuildPayloadFromDocument_CurrentSemanticHashAbsentWhenEmpty(t *testing.T) {
	doc := emptyDoc()
	p := BuildPayloadFromDocument(doc, nil)
	if _, ok := p["current_semantic_hash"]; ok {
		t.Errorf("payload[current_semantic_hash] present when empty — want ABSENT (omitempty contract)")
	}
}

// 20. PR-CATALOG-MULTILINGUA step 6 (July 2026): canonical 8-field
// search_document composition. Pinned composition:
//  1. title
//  2. description
//  3. visual_summary
//  4. transcript
//  5. topics (joined as "topics: a, b, c")
//  6. entities (joined as "entities: a, b, c")
//  7. event
//  8. scene
//
// All fields must appear in the embedding_text in this order (with
// `\n` separation) when populated. Empty fields are omitted (NO
// placeholder line).
func TestBuildPayloadFromDocument_CanonicalSearchDocument_AllEightFieldsInOrder(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Title = "Round 7: Broner barcolla"
	doc.Metadata.Description = "Broner loses composure on camera"
	doc.Metadata.VisualSummary = "a boxer ducks a punch and stumbles"
	doc.Metadata.Transcript = "Pacquiao lands a left hook"
	doc.Metadata.Topics = []string{"boxing", "round-7"}
	doc.Metadata.Entities = []string{"Pacquiao", "Broner"}
	doc.Metadata.Event = "Pacquiao vs Broner"
	doc.Metadata.Scene = "main arena, Mandalay Bay"
	p := BuildPayloadFromDocument(doc, nil)
	v, ok := p["embedding_text"]
	if !ok {
		t.Fatalf("payload[embedding_text] absent — want canonical 8-field composition")
	}
	got, _ := v.(string)
	want := "Round 7: Broner barcolla\n" +
		"Broner loses composure on camera\n" +
		"a boxer ducks a punch and stumbles\n" +
		"Pacquiao lands a left hook\n" +
		"topics: boxing, round-7\n" +
		"entities: Pacquiao, Broner\n" +
		"event: Pacquiao vs Broner\n" +
		"scene: main arena, Mandalay Bay"
	if got != want {
		t.Fatalf("canonical 8-field composition mismatch.\n got = %q\nwant = %q", got, want)
	}
} // 21. PR-CATALOG-MULTILINGUA step 6 (July 2026): FORWARD-PREVENTION.
// embedding_text MUST NOT contain any link/locator metadata. Each
// of the forbidden substrings below is a godlike/06 SSOT violation if
// it appears in the embedding_text. This is the canonical gate that
// keeps the embedding search-vector input clean (no URL/locator
// contamination).
//
// The test populates every field that could leak a link/locator into
// embedding_text (drive_path, source_url, job_id, workflow_id,
// policy_version, etc.) and asserts embedding_text NEVER contains
// any forbidden substring.
func TestBuildPayloadFromDocument_CanonicalSearchDocument_NoLinkOrLocatorInEmbeddingText(t *testing.T) {
	doc := emptyDoc()
	// Populate everything that could leak.
	doc.Metadata.Title = "Round 7: Broner barcolla"
	doc.Metadata.Description = "Broner stumbles against Pacquiao's left hook"
	doc.Metadata.VisualSummary = "boxer ducks and steps back"
	doc.Metadata.Transcript = "Pacquiao lands the punch"
	doc.Metadata.Topics = []string{"boxing"}
	doc.Metadata.Entities = []string{"Pacquiao", "Broner"}
	doc.Metadata.Event = "Pacquiao vs Broner"
	doc.Metadata.Scene = "main arena"
	doc.Metadata.SourceURL = "https://www.youtube.com/watch?v=abc"
	doc.Metadata.YouTubeURL = "https://www.youtube.com/watch?v=abc"
	doc.Metadata.YouTubeID = "vdC5GXxS-qU"
	doc.Metadata.SourceVideoID = "vdC5GXxS-qU"
	doc.Metadata.DrivePath = "stock/boxe/youtube/Round-7-Broner-barcolla"
	doc.Metadata.DriveLink = "https://drive.google.com/file/d/abc123/view"
	doc.Metadata.JobID = "job-12345-ref"
	doc.Metadata.WorkflowID = "wf-broner-barcolla-v1"
	doc.Metadata.RunFingerprint = "fp-deadbeef-cafe"
	doc.Metadata.ChunkIndex = 0
	doc.Metadata.TotalChunks = 11
	doc.Metadata.PolicyVersion = "v1.2.3"
	doc.Metadata.SourceProvider = "youtube"
	doc.Metadata.Hook = "Stay focused!"
	doc.Metadata.Summary = "Broner can't recover the round"
	doc.Metadata.Tags = []string{"boxing", "pacquiao"}
	doc.Metadata.Speakers = []string{"commentator"}
	doc.Metadata.MentionedPeople = []string{"Floyd Mayweather"}
	doc.Metadata.SearchKeywords = []string{"left hook"}
	doc.Metadata.Category = "Boxe"
	doc.Metadata.Language = "en"
	p := BuildPayloadFromDocument(doc, nil)
	v, ok := p["embedding_text"]
	if !ok {
		t.Fatalf("payload[embedding_text] absent — fill metadata first")
	}
	got, _ := v.(string)
	forbiddenSubstrings := []string{
		"drive_link", "drive_path",
		"source_url", "http://", "https://",
		"source_video_id", "youtube_video_id", "youtube_url",
		"job_id", "workflow_id", "policy_version",
		"chunk_index", "total_chunks", "run_fingerprint",
		// The 4 cosmetic labels preserved as PROHIBITED under step 6 —
		// pre-step-6 composers emitted these as labels; under the
		// canonical composer the labels are gone because Topics/Entities
		// absorb the labels (no "tags:", "topics:" prefix inside
		// embedding_text — the prefix IS allowed for topics + entities
		// but NOT for other keys).
		"hook:", "speakers:", "mentioned_people:", "search_keywords:", "tags:",
		"category:", "language:", "subject:",
	}
	for _, substr := range forbiddenSubstrings {
		if strings.Contains(got, substr) {
			t.Errorf("embedding_text contains forbidden substring %q — PR-CATALOG-MULTILINGUA step 6 invariant violated\nembedding_text = %q", substr, got)
		}
	}
}

// 22. PR-CATALOG-MULTILINGUA step 6 (July 2026): the canonical payload
// surface (asset_id + drive_link + source_provider + available_languages
// + original_language + current_semantic_hash) is the search-result
// key for the multilingual catalog. asset_id MUST be present (already
// pre-existing); source_provider, available_languages, and
// original_language are already in the IndexedMetadata (pre-existing
// from FASE-9); drive_link and current_semantic_hash are the new
// step-6 additions. Tests all 6 keys in a single document so a
// regression in any of them surfaces immediately.
// 23. PR-CATALOG-MULTILINGUA step 6 (July 2026): multi-language
// transcript composer — the canonical 8-field embedding_text path
// reads doc.Metadata.Transcripts (slice) instead of the single-string
// Transcript. IsOriginal row → bare text first; remaining rows →
// `transcript ({Lang}): {Text}` sequels in Lang-ASC alphabetical
// order for byte-stable output across re-runs.
//
// Locks:
//   - Original-language row (matched case-folded against
//     doc.Metadata.OriginalLanguage) is emitted as bare text on its
//     own line BEFORE any sequel.
//   - Subsequent rows render as `transcript ({Lang}): {Text}` on
//     new lines.
//   - Order of sequels is Lang-ASC alphabetical, NOT insertion order.
func TestBuildPayloadFromDocument_Transcripts_MultilingualSlice_OriginalFirstThenSequelsLangASC(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Title = "Round 7 Test"
	doc.Metadata.OriginalLanguage = "en"
	doc.Metadata.Transcripts = []TranscriptTrack{
		{Lang: "es", Text: "Broner troppa lento en el ring.", IsOriginal: false},
		{Lang: "en", Text: "Pacquiao lands a clean left hook on Broner.", IsOriginal: true},
		{Lang: "it", Text: "Pacquiao molla Broner con un gancio.", IsOriginal: false},
	}
	p := BuildPayloadFromDocument(doc, nil)
	v, ok := p["embedding_text"]
	if !ok {
		t.Fatalf("payload[embedding_text] absent — multi-language composer must emit bare + sequels")
	}
	got, _ := v.(string)
	wantSegs := []string{
		"Pacquiao lands a clean left hook on Broner.", // original-language bare row
		"transcript (es): Broner troppa lento en el ring.",
		"transcript (it): Pacquiao molla Broner con un gancio.",
	}
	// Order check: original must come BEFORE any sequel.
	originalIdx := strings.Index(got, wantSegs[0])
	firstSequelIdx := strings.Index(got, wantSegs[1])
	secondSequelIdx := strings.Index(got, wantSegs[2])
	if originalIdx < 0 {
		t.Fatalf("original-language bare row missing from embedding_text: %q", got)
	}
	if firstSequelIdx < 0 || secondSequelIdx < 0 {
		t.Fatalf("sequel rows missing from embedding_text: %q", got)
	}
	if !(originalIdx < firstSequelIdx && originalIdx < secondSequelIdx) {
		t.Fatalf("original-language row must precede sequels in embedding_text: %q", got)
	}
	// Lang-ASC alphabetical order for sequels: es < it.
	if !(firstSequelIdx < secondSequelIdx) {
		t.Fatalf("sequels must be Lang-ASC (es before it) but got: %q", got)
	}
	// All three segments must appear verbatim.
	for _, seg := range wantSegs {
		if !strings.Contains(got, seg) {
			t.Errorf("embedding_text missing segment %q\n got = %q", seg, got)
		}
	}
}

// 24. PR-CATALOG-MULTILINGUA step 6 (July 2026): empty Transcripts
// slice falls back to the legacy single-string Transcript field.
// godlike/07 minimum-blast-radius: producers that haven't yet adopted
// the TextTrackQuerier flow still surface a non-empty
// doc.Metadata.Transcript (single string from metadata_json) and
// that verbatim output must reach embedding_text on the transition.
func TestBuildPayloadFromDocument_Transcripts_SliceEmptyFallsBackToLegacySingleString(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Transcript = "legacy single-string transcript text"
	// doc.Metadata.Transcripts is nil (default).
	p := BuildPayloadFromDocument(doc, nil)
	v, ok := p["embedding_text"]
	if !ok {
		t.Fatalf("payload[embedding_text] absent — empty-slice fallback to legacy must emit")
	}
	got, _ := v.(string)
	if !strings.Contains(got, "legacy single-string transcript text") {
		t.Fatalf("embedding_text missing legacy single-string fallback: %q", got)
	}
}

// 25. PR-CATALOG-MULTILINGUA step 6 (July 2026): empty Transcripts
// slice AND empty legacy Transcript field → embedding_text skips the
// transcript slot entirely (no placeholder line). godlike/07
// NO-FAKE-AVAILABILITY: a missing field MUST NOT emit a placeholder.
func TestBuildPayloadFromDocument_Transcripts_AllEmpty_TranscriptsSlotSkipped(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.Title = "Only other fields populated"
	doc.Metadata.Description = "no transcript here"
	// Both doc.Metadata.Transcript (empty default) and
	// doc.Metadata.Transcripts (nil default) are empty.
	p := BuildPayloadFromDocument(doc, nil)
	v, ok := p["embedding_text"]
	if !ok {
		t.Fatalf("payload[embedding_text] absent — but description should make it appear")
	}
	got, _ := v.(string)
	if strings.Contains(got, "transcript:") || strings.Contains(got, "transcript (") {
		t.Errorf("embedding_text MUST NOT emit placeholder transcript line when both canonical and legacy are empty: %q", got)
	}
	if !strings.Contains(got, "no transcript here") {
		t.Errorf("description should still appear in embedding_text when transcript is empty: %q", got)
	}
}

// 26. PR-CATALOG-MULTILINGUA step 6 (July 2026): language-code
// case-insensitive match against doc.Metadata.OriginalLanguage.
// BCP-47 codes are lowercase by convention, but case-folded matching
// (`strings.EqualFold`) prevents a future producer accidentally
// storing `EN` or `En` from breaking the bare-text slot.
func TestBuildPayloadFromDocument_Transcripts_CaseFoldedLanguageMatch(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.OriginalLanguage = "en" // lowercase canonical
	doc.Metadata.Transcripts = []TranscriptTrack{
		{Lang: "EN", Text: "bare text from uppercase-EN row", IsOriginal: true},
		{Lang: "fr", Text: "version française.", IsOriginal: false},
	}
	p := BuildPayloadFromDocument(doc, nil)
	v, _ := p["embedding_text"].(string)
	bareIdx := strings.Index(v, "bare text from uppercase-EN row")
	sequelIdx := strings.Index(v, "transcript (fr):")
	if bareIdx < 0 {
		t.Fatalf("case-folded lang match missing bare row: %q", v)
	}
	if sequelIdx < 0 {
		t.Fatalf("case-folded sequel missing: %q", v)
	}
	if !(bareIdx < sequelIdx) {
		t.Fatalf("case-folded bare row must precede sequel: %q", v)
	}
}

func TestBuildPayloadFromDocument_CanonicalPayloadKey_AllSixPresent(t *testing.T) {
	doc := emptyDoc()
	doc.Metadata.SourceProvider = "youtube"
	doc.Metadata.DriveLink = "https://drive.google.com/file/d/abc/view"
	doc.Metadata.CurrentSemanticHash = "sha256-abc"
	doc.Metadata.OriginalLanguage = "en"
	doc.Metadata.AvailableLanguages = []string{"en", "it", "es", "pt-BR"}
	doc.Metadata.DriveFileID = "drive-file-abc"
	p := BuildPayloadFromDocument(doc, nil)
	canonical := map[string]string{
		"asset_id":              "stock:test:timestamp:0:video",
		"drive_file_id":         "drive-file-abc",
		"drive_link":            "https://drive.google.com/file/d/abc/view",
		"source_provider":       "youtube",
		"original_language":     "en",
		"current_semantic_hash": "sha256-abc",
	}
	for k, want := range canonical {
		v, ok := p[k]
		if !ok {
			t.Errorf("payload[%s] absent — PR-CATALOG-MULTILINGUA step 6 canonical payload surface incomplete", k)
			continue
		}
		s, isStr := v.(string)
		if !isStr {
			t.Errorf("payload[%s] = %T — want string", k, v)
			continue
		}
		if s != want {
			t.Errorf("payload[%s] = %q, want %q", k, s, want)
		}
	}
	got, _ := p["available_languages"].([]string)
	if len(got) != 4 {
		t.Errorf("payload[available_languages] len = %d, want 4 ([en it es pt-BR])", len(got))
	}
}

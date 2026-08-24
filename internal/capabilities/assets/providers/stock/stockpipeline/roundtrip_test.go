package assets

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyStockPayloadToMap is the reference implementation of the removed
// `stockPayloadToMap` helper (see the comment above ToJobPayload in
// command.go). The legacy helper round-tripped through
// json.Marshal/Unmarshal and silently dropped Marshal errors — its
// observable behaviour for OK inputs was: build a StockRunPayload
// (then a struct with equivalent fields), Marshal it, Unmarshal to
// map[string]any, hand the map back. The reference impl below
// replicates exactly that behaviour so the equivalence test below
// pins ToJobPayload vs the legacy shape byte-for-byte.
//
// The reference impl lives ONLY in this test file — production code
// consumes ToJobPayload exclusively. The legacy helper is intentionally
// NOT reintroduced into the production tree.
func legacyStockPayloadToMap(c *StockCommand) map[string]any {
	if c == nil {
		return map[string]any{}
	}

	temp := StockRunPayload{
		SearchQueries: c.SearchQueries,
		DirectURLs:    c.DirectURLs,
		TotalMinutes:  c.TotalMinutes,
		ChunkDuration: c.ChunkDuration,
		ClipDuration:  c.ClipDuration,
		NoAudio:       c.NoAudio,
		NoEffects:     c.NoEffects,
		NoTransitions: c.NoTransitions,
		MaxVideos:     c.MaxVideos,
		Subfolder:     c.Subfolder,
		FolderName:    c.FolderName,
		FolderID:      c.FolderID,
		Async:         c.Async,
	}
	if c.Metadata != nil {
		temp.Metadata = &StockRunPayloadMetadata{
			Title:       c.Metadata.Title,
			Description: c.Metadata.Description,
			Tags:        c.Metadata.Tags,
			Category:    c.Metadata.Category,
			Author:      c.Metadata.Author,
			Extra:       c.Metadata.Extra,
		}
	}

	// The legacy helper dropped Marshal errors silently. Match that.
	b, _ := json.Marshal(temp)
	var out map[string]any
	// Same pattern on Unmarshal.
	_ = json.Unmarshal(b, &out)
	return out
}

// TestToJobPayload_RoundTrip_ToStockRunPayload pins the contract that
// command \u2192 cmd.ToJobPayload() \u2192 json.Marshal \u2192 json.Unmarshal \u2192
// StockRunPayload reproduces the command's fields verbatim. Any drift
// between the manual field-by-field projection in ToJobPayload and
// the JSON tags on StockRunPayload fails this test. Async is
// included since the field is part of both the projection and the
// payload DTO.
func TestToJobPayload_RoundTrip_ToStockRunPayload(t *testing.T) {
	cmd := &StockCommand{
		SearchQueries: []string{"ocean", "beach"},
		DirectURLs:    []string{"https://example.com/vid.mp4"},
		TotalMinutes:  5,
		ChunkDuration: 25,
		ClipDuration:  10,
		NoAudio:       true,
		NoEffects:     true,
		NoTransitions: false, // test the false-but-emitted path
		MaxVideos:     10,
		Subfolder:     "broll",
		FolderName:    "Proj",
		FolderID:      "folder-123",
		Async:         true,
		Metadata: &ChunkMetadataInput{
			Title:       "Test Title",
			Description: "Test Description",
			Tags:        []string{"a", "b"},
			Category:    "TestCategory",
			Author:      "TestAuthor",
			Extra:       map[string]string{"foo": "bar"},
		},
	}

	payload := cmd.ToJobPayload()
	raw, err := json.Marshal(payload)
	require.NoError(t, err, "ToJobPayload output must marshal cleanly")

	var rp StockRunPayload
	require.NoError(t, json.Unmarshal(raw, &rp),
		"marshalled ToJobPayload output must unmarshal cleanly into StockRunPayload")

	assert.Equal(t, cmd.SearchQueries, rp.SearchQueries)
	assert.Equal(t, cmd.DirectURLs, rp.DirectURLs)
	assert.Equal(t, cmd.TotalMinutes, rp.TotalMinutes)
	assert.Equal(t, cmd.ChunkDuration, rp.ChunkDuration)
	assert.Equal(t, cmd.ClipDuration, rp.ClipDuration)
	assert.Equal(t, cmd.NoAudio, rp.NoAudio)
	assert.Equal(t, cmd.NoEffects, rp.NoEffects)
	assert.Equal(t, cmd.NoTransitions, rp.NoTransitions)
	assert.Equal(t, cmd.MaxVideos, rp.MaxVideos)
	assert.Equal(t, cmd.Subfolder, rp.Subfolder)
	assert.Equal(t, cmd.FolderName, rp.FolderName)
	assert.Equal(t, cmd.FolderID, rp.FolderID)
	assert.Equal(t, cmd.Async, rp.Async)

	require.NotNil(t, rp.Metadata)
	assert.Equal(t, cmd.Metadata.Title, rp.Metadata.Title)
	assert.Equal(t, cmd.Metadata.Description, rp.Metadata.Description)
	assert.Equal(t, cmd.Metadata.Tags, rp.Metadata.Tags)
	assert.Equal(t, cmd.Metadata.Category, rp.Metadata.Category)
	assert.Equal(t, cmd.Metadata.Author, rp.Metadata.Author)
	assert.Equal(t, cmd.Metadata.Extra, rp.Metadata.Extra)
}

func TestToJobPayload_RoundTrip_ClipDescriptions(t *testing.T) {
	cmd := &StockCommand{
		Clips: []ClipSpec{
			{
				Title:       "Round 1",
				Description: "Pacquiao touches Broner with a probing left and resets.",
				URL:         "https://www.youtube.com/watch?v=abc123",
				StartSec:    32,
				EndSec:      37,
			},
		},
		TotalMinutes: 1,
	}

	payload := cmd.ToJobPayload()
	raw, err := json.Marshal(payload)
	require.NoError(t, err, "ToJobPayload output must marshal cleanly")

	var rp StockRunPayload
	require.NoError(t, json.Unmarshal(raw, &rp),
		"marshalled ToJobPayload output must unmarshal cleanly into StockRunPayload")

	require.Len(t, rp.Clips, 1)
	assert.Equal(t, cmd.Clips[0].Title, rp.Clips[0].Title)
	assert.Equal(t, cmd.Clips[0].Description, rp.Clips[0].Description)
	assert.Equal(t, cmd.Clips[0].URL, rp.Clips[0].URL)
	assert.Equal(t, cmd.Clips[0].StartSec, rp.Clips[0].StartSec)
	assert.Equal(t, cmd.Clips[0].EndSec, rp.Clips[0].EndSec)
}

// TestToJobPayload_RoundTrip_ClipTagsDefensiveCopy locks the
// godlike/06 SSOT contract: ClipSpec.Tags is defensive-copied at
// the ToJobPayload boundary (clipsCopy + per-ClipSpec.Tags deep
// copy in command.go::ToJobPayload). Mutating the source
// ClipSpec.Tags after ToJobPayload() must NOT affect the already-
// produced payload (the copy points to a new backing array).
//
// Regression guard: if a future refactor reverts the defensive copy
// and routes the source slice header through (payload["clips"] =
// c.Clips with no copy), this test fails because the marshalled
// JSON would include post-mutation tokens.
//
// Contract: this test calls ToJobPayload() ONCE, then re-marshals
// the same payload after the source mutation. The defensive copy
// ensures the payload is independent of subsequent source mutations.
// (Calling ToJobPayload() twice would defeat the test — the second
// call would snapshot the post-mutation state regardless of whether
// the copy happened.)
func TestToJobPayload_RoundTrip_ClipTagsDefensiveCopy(t *testing.T) {
	originalTags := []string{"boxing", "pacquiao", "broner"}
	cmd := &StockCommand{
		Clips: []ClipSpec{
			{
				Title:    "Round 7",
				URL:      "https://www.youtube.com/watch?v=round7",
				StartSec: 993,
				EndSec:   1048,
				Round:    7,
				Tags:     originalTags,
				Category: "boxing",
				Slug:     "round-7",
			},
		},
		TotalMinutes: 1,
	}

	// Single payload snapshot.
	payload := cmd.ToJobPayload()
	rawBefore, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.Contains(t, string(rawBefore), `"tags":["boxing","pacquiao","broner"]`,
		"initial wire shape must contain the original Tags verbatim")

	// Mutate the source ClipSpec.Tags AFTER ToJobPayload. The
	// already-produced payload must remain stable (godlike/06 SSOT:
	// defensive copy at every boundary).
	cmd.Clips[0].Tags[0] = "MUTATED"
	cmd.Clips[0].Tags = append(cmd.Clips[0].Tags, "APPENDED")

	// Re-marshal the SAME payload (not a fresh ToJobPayload call).
	// With the defensive copy in place, the payload's clipsCopy
	// is independent of the source mutation. Without the copy,
	// the payload's clips would share the source's backing array
	// and the mutation would leak into the JSON.
	rawAfter, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.NotContains(t, string(rawAfter), "MUTATED",
		"payload must remain stable after caller mutates cmd.Clips[0].Tags[0]")
	assert.NotContains(t, string(rawAfter), "APPENDED",
		"payload must remain stable after caller appends to cmd.Clips[0].Tags")
	assert.Contains(t, string(rawAfter), `"tags":["boxing","pacquiao","broner"]`,
		"payload's Tags must remain the original after caller mutation")
}

// TestToJobPayload_RoundTrip_ClipRoundTagsCategorySlug pins the JSON
// wire-shape contract for the 4 PR-STOCK-TIMESTAMP-CLIPS Front 2
// fields (Round / Tags / Category / Slug) on ClipSpec. Each field
// uses omitempty so a zero-value ClipSpec produces the same wire
// shape as the pre-PR baseline (no spurious keys on the legacy
// search/direct-url paths).
//
// godlike/06 SSOT: the canonical wire shape lives at
// internal/capabilities/assets/providers/stock/stockpipeline/types_run.go
// (the ClipSpec struct). Drift in any JSON tag surfaces as a
// test failure here.
func TestToJobPayload_RoundTrip_ClipRoundTagsCategorySlug(t *testing.T) {
	cmd := &StockCommand{
		Clips: []ClipSpec{
			{
				Title:       "Round 7 - Broner barcolla",
				Description: "Pacquiao lands a series of clean left hands.",
				URL:         "https://www.youtube.com/watch?v=round7",
				StartSec:    993,
				EndSec:      1048,
				Round:       7,
				Tags:        []string{"boxing", "pacquiao", "broner", "round-7"},
				Category:    "boxing",
				Slug:        "round-7-broner-barcolla",
			},
		},
		TotalMinutes: 1,
	}

	payload := cmd.ToJobPayload()
	raw, err := json.Marshal(payload)
	require.NoError(t, err, "ToJobPayload output must marshal cleanly")

	// omitempty contract: non-empty fields are present on the wire.
	for _, want := range []string{`"round":7`, `"category":"boxing"`, `"slug":"round-7-broner-barcolla"`, `"tags":`} {
		assert.Contains(t, string(raw), want, "JSON wire shape must contain %s", want)
	}

	var rp StockRunPayload
	require.NoError(t, json.Unmarshal(raw, &rp),
		"marshalled ToJobPayload output must unmarshal cleanly into StockRunPayload")

	require.Len(t, rp.Clips, 1)
	assert.Equal(t, cmd.Clips[0].Round, rp.Clips[0].Round)
	assert.Equal(t, cmd.Clips[0].Tags, rp.Clips[0].Tags)
	assert.Equal(t, cmd.Clips[0].Category, rp.Clips[0].Category)
	assert.Equal(t, cmd.Clips[0].Slug, rp.Clips[0].Slug)
}

// TestToJobPayload_RoundTrip_ClipZeroFieldsOmitsAll4 locks the
// godlike/07 NO-FAKE-AVAILABILITY contract: when Round/Tags/Category/
// Slug are all at zero value (the deterministic-planner case), the
// JSON wire shape does NOT include those keys. Operators inspecting
// the metadata see the same wire shape they did pre-PR for the
// legacy search/direct-url paths.
func TestToJobPayload_RoundTrip_ClipZeroFieldsOmitsAll4(t *testing.T) {
	cmd := &StockCommand{
		Clips: []ClipSpec{
			{
				Title:    "Round 1",
				URL:      "https://www.youtube.com/watch?v=round1",
				StartSec: 32,
				EndSec:   51,
				// Round/Tags/Category/Slug all zero — deterministic-planner case
			},
		},
		TotalMinutes: 1,
	}

	payload := cmd.ToJobPayload()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	for _, absent := range []string{`"round"`, `"tags"`, `"category"`, `"slug"`} {
		assert.NotContains(t, string(raw), absent,
			"zero-value field %s must be omitted from JSON wire (omitempty contract)", absent)
	}
}

// TestToJobPayload_EquivalentToLegacyStockPayloadToMap is the
// invariant test the user requested: ToJobPayload() must produce
// output equivalent to the (removed) legacy stockPayloadToMap helper
// under the canonical command \u2192 map[string]any \u2192 unmarshal \u2192
// StockRunPayload chain. Any drift in the omitempty handling, key
// naming, or typed-projection semantics fails this test.
//
// We compare the two paths via their unmarshalled StockRunPayload
// projection (testify's assert.Equal uses reflect.DeepEqual under
// the hood, which correctly handles slice + map fields).
func TestToJobPayload_EquivalentToLegacyStockPayloadToMap(t *testing.T) {
	cmd := &StockCommand{
		SearchQueries: []string{"ocean", "coast"},
		DirectURLs:    []string{"https://example.com/v.mp4"},
		TotalMinutes:  10,
		ChunkDuration: 25,
		ClipDuration:  8,
		NoAudio:       true,
		NoEffects:     false,
		NoTransitions: false,
		MaxVideos:     5,
		Subfolder:     "broll",
		FolderName:    "Project",
		FolderID:      "folder-abc",
		Async:         true,
		Metadata: &ChunkMetadataInput{
			Title:       "T",
			Description: "D",
			Tags:        []string{"x"},
			Category:    "Cat",
			Extra:       map[string]string{"k": "v"},
		},
	}

	canonicalPayload := cmd.ToJobPayload()
	legacyPayload := legacyStockPayloadToMap(cmd)

	rawCanonical, err := json.Marshal(canonicalPayload)
	require.NoError(t, err)
	rawLegacy, err := json.Marshal(legacyPayload)
	require.NoError(t, err)

	var rpCanonical, rpLegacy StockRunPayload
	require.NoError(t, json.Unmarshal(rawCanonical, &rpCanonical))
	require.NoError(t, json.Unmarshal(rawLegacy, &rpLegacy))

	assert.Equal(t, rpLegacy, rpCanonical,
		"ToJobPayload projection must produce the same StockRunPayload values as the legacy stockPayloadToMap helper for an equivalent command")
}

// TestFromSearchAndRunRequest_PreservesQueryLimits pins the per-query
// `limit` propagation that was previously dropped: each SearchQuery.Limit
// must survive the request → StockCommand projection aligned 1:1 with the
// resolved query string (zero when the caller omitted limit).
func TestFromSearchAndRunRequest_PreservesQueryLimits(t *testing.T) {
	r := &StockSearchAndRunRequest{
		Queries: []SearchQuery{
			{Q: "ocean", Limit: 1},
			{Q: "beach", Limit: 3},
			{Q: "coast"},
		},
	}
	cmd, err := FromSearchAndRunRequest(r)
	require.NoError(t, err)
	assert.Equal(t, []string{"ocean", "beach", "coast"}, cmd.SearchQueries)
	assert.Equal(t, []int{1, 3, 0}, cmd.SearchQueryLimits)
}

// TestToJobPayload_RoundTrip_SearchQueryLimits pins the search_query_limits
// wire shape so the async path (command → ToJobPayload → jobs broker →
// HandleJob → RunInput) preserves per-query limits end-to-end.
func TestToJobPayload_RoundTrip_SearchQueryLimits(t *testing.T) {
	cmd := &StockCommand{
		SearchQueries:     []string{"ocean", "beach"},
		SearchQueryLimits: []int{1, 3},
		TotalMinutes:      1,
	}

	payload := cmd.ToJobPayload()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"search_query_limits":[1,3]`)

	var rp StockRunPayload
	require.NoError(t, json.Unmarshal(raw, &rp))
	assert.Equal(t, cmd.SearchQueryLimits, rp.SearchQueryLimits)
}

// TestToJobPayload_OmitsEmptySearchQueryLimits locks the omitempty-adjacent
// contract: when no query carries an explicit limit, the wire shape must not
// emit a search_query_limits key (legacy /run and direct-url callers stay
// byte-compatible).
func TestToJobPayload_OmitsEmptySearchQueryLimits(t *testing.T) {
	cmd := &StockCommand{
		SearchQueries: []string{"ocean"},
		TotalMinutes:  1,
	}
	raw, err := json.Marshal(cmd.ToJobPayload())
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "search_query_limits")
}

// TestStockRunPayload_AsyncRoundTrip pins the JSON wire-shape contract
// of the Async field: zero-value omitted (omitempty), true emitted
// verbatim. Operators who set "async": false on the wire get exactly
// the false they sent back; operators who set "async": true get
// the true back.
func TestStockRunPayload_AsyncRoundTrip(t *testing.T) {
	t.Run("zero_value_omitted", func(t *testing.T) {
		p := StockRunPayload{}
		raw, err := json.Marshal(p)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), `"async"`,
			"async=false (zero value) must be omitted via omitempty so existing clients see no behaviour change")
	})
	t.Run("true_emitted", func(t *testing.T) {
		p := StockRunPayload{Async: true}
		raw, err := json.Marshal(p)
		require.NoError(t, err)
		assert.Contains(t, string(raw), `"async":true`,
			"async=true must be emitted verbatim")
		var q StockRunPayload
		require.NoError(t, json.Unmarshal(raw, &q))
		assert.True(t, q.Async)
	})
}

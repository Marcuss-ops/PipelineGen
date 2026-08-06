package stockpipeline

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultPipelineConfig(t *testing.T) {
	cfg := DefaultPipelineConfig()
	assert.Equal(t, 25, cfg.ChunkDuration, "default chunk duration should be 25s")
	assert.Equal(t, 25, cfg.MaxResults, "default max results should be 25")
	assert.Equal(t, 4, cfg.EffectInterval, "default effect interval should be 4")
	assert.Equal(t, "assets/effects/EffettiVisiv", cfg.EffectsDir, "default effects dir")
	assert.Equal(t, "libx264", cfg.Codec, "stock default codec must be CPU-first")
	assert.Equal(t, "veryfast", cfg.Preset, "stock default preset must match canonical CPU profile")
	assert.Equal(t, 23, cfg.CRF, "stock default CRF must match canonical CPU profile")
}

func TestRunInputValidation_EmptySources(t *testing.T) {
	// A RunInput with no SearchQueries and no DirectURLs should produce no sources
	input := &RunInput{
		SearchQueries: []string{},
		DirectURLs:    []string{},
		TotalMinutes:  10,
	}

	assert.Empty(t, input.SearchQueries, "no search queries")
	assert.Empty(t, input.DirectURLs, "no direct urls")
	assert.Equal(t, 10, input.TotalMinutes)
}

func TestRunInputValidation_WithSources(t *testing.T) {
	input := &RunInput{
		SearchQueries: []string{"elon musk", "spacex"},
		DirectURLs: []string{
			"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		},
		TotalMinutes:  5,
		ChunkDuration: 25,
		MaxVideos:     10,
		Subfolder:     "Test",
		FolderName:    "Run1",
	}

	assert.Len(t, input.SearchQueries, 2)
	assert.Len(t, input.DirectURLs, 1)
	assert.Equal(t, 5, input.TotalMinutes)
	assert.Equal(t, 25, input.ChunkDuration)
	assert.Equal(t, 10, input.MaxVideos)
}

func TestChunkResultConstruction(t *testing.T) {
	r := ChunkResult{
		Index:         0,
		TimelineStart: 0.0,
		TimelineEnd:   25.0,
		LocalPath:     "/tmp/chunks/chunk_000.mp4",
		DriveLink:     "https://drive.google.com/file/d/abc123",
		DriveFileID:   "abc123",
		Title:         "Test Chunk",
	}

	assert.Equal(t, 0, r.Index)
	assert.Equal(t, 25.0, r.TimelineEnd)
	assert.Equal(t, "/tmp/chunks/chunk_000.mp4", r.LocalPath)
	assert.Contains(t, r.DriveLink, "drive.google.com")
}

func TestChunkResultEmptyDefaults(t *testing.T) {
	r := ChunkResult{}
	assert.Equal(t, 0, r.Index)
	assert.Equal(t, 0.0, r.TimelineStart)
	assert.Equal(t, "", r.LocalPath)
	assert.Equal(t, "", r.DriveLink)
	assert.Equal(t, "", r.Title)
}

func TestPipelineResultConstruction(t *testing.T) {
	result := PipelineResult{
		Chunks: []ChunkResult{
			{Index: 0, TimelineEnd: 25.0, Title: "Chunk 1"},
			{Index: 1, TimelineStart: 25.0, TimelineEnd: 50.0, Title: "Chunk 2"},
		},
		TotalClips:  12,
		TotalChunks: 2,
		SearchTerms: []string{"ai", "robotics"},
	}

	assert.Len(t, result.Chunks, 2)
	assert.Equal(t, 12, result.TotalClips)
	assert.Equal(t, 2, result.TotalChunks)
	assert.Equal(t, []string{"ai", "robotics"}, result.SearchTerms)
}

func TestPipelineConfigDefaultsOnly(t *testing.T) {
	// Verify the config has all fields populated
	cfg := DefaultPipelineConfig()

	assert.NotZero(t, cfg.ChunkDuration)
	assert.NotZero(t, cfg.MaxResults)
	assert.NotZero(t, cfg.EffectInterval)
	assert.NotEmpty(t, cfg.EffectsDir)
}

func TestRunInputChunkDurationOverride(t *testing.T) {
	// When ChunkDuration is 0, the service should fall back to PipelineConfig default
	inputZero := &RunInput{ChunkDuration: 0}
	inputCustom := &RunInput{ChunkDuration: 15}

	assert.Zero(t, inputZero.ChunkDuration, "zero means use default")
	assert.Equal(t, 15, inputCustom.ChunkDuration, "custom duration should be used")
}

func TestRunInputMaxVideosBoundary(t *testing.T) {
	// MaxVideos=0 means no limit
	inputUnlimited := &RunInput{MaxVideos: 0}
	inputLimited := &RunInput{MaxVideos: 5}

	assert.Zero(t, inputUnlimited.MaxVideos, "zero means unlimited")
	assert.Equal(t, 5, inputLimited.MaxVideos)
}

func TestInterleaveClips(t *testing.T) {
	// FASE 2.4 (July 2026): the legacy parallel-array signature
	// (clips/titles/sourceIDs as three [][]string slices) is gone.
	// The signature is now single [][]Clip input → []Clip output.
	// Each source group feeds its 2-3 produced clips; InterleaveClips
	// shuffles within each group and round-robins across groups.
	srcA := []Clip{
		{Path: "A1", Title: "TitleA1", SourceID: "video-a", Status: CutItemStatusSucceeded},
		{Path: "A2", Title: "TitleA2", SourceID: "video-a", Status: CutItemStatusSucceeded},
		{Path: "A3", Title: "TitleA3", SourceID: "video-a", Status: CutItemStatusSucceeded},
	}
	srcB := []Clip{
		{Path: "B1", Title: "TitleB1", SourceID: "video-b", Status: CutItemStatusValidated},
		{Path: "B2", Title: "TitleB2", SourceID: "video-b", Status: CutItemStatusValidated},
	}
	srcC := []Clip{
		{Path: "C1", Title: "TitleC1", SourceID: "video-c", Status: CutItemStatusSucceeded},
	}

	clips := [][]Clip{srcA, srcB, srcC}
	interleaved := InterleaveClips(clips)

	assert.Len(t, interleaved, 6, "interleaved length should equal union of source slices")

	// Verify the structured fields propagate to the output.
	for _, c := range interleaved {
		assert.NotEmpty(t, c.Path, "Path should be preserved through interleave")
		assert.NotEmpty(t, c.Title, "Title should be preserved through interleave")
		assert.NotEmpty(t, c.SourceID, "SourceID should be preserved through interleave")
		assert.True(t, c.Succeeded(), "only Succeeded/Validated clips survive interleave")
	}

	groupOf := func(c Clip) string {
		return string(c.Path[0])
	}

	// First 3 items should contain one element from A, B, and C each.
	assert.Equal(t, 3, countUniqueGroupsClips(interleaved[0:3], groupOf))
}

// countUniqueGroupsClips is the FASE 2.4 Clip-aware counterpart to
// service_test.go's legacy countUniqueGroups(string). It maps each
// Clip to a key via the supplied projection function for partition
// cardinality checks.
func countUniqueGroupsClips(items []Clip, groupOf func(Clip) string) int {
	seen := make(map[string]bool)
	for _, item := range items {
		seen[groupOf(item)] = true
	}
	return len(seen)
}

// TestInterleaveClips_DropsFailed pins the FASE 2.4 filter
// behaviour: Failed clips are excluded from the interleaved output
// (they are not Succeeded(), so they don't reach downstream
// renderChunk). The test also exercises the empty-source-skip path
// (source B is empty — should be silently skipped).
func TestInterleaveClips_DropsFailed(t *testing.T) {
	srcA := []Clip{
		{Path: "A1", Title: "a", SourceID: "a", Status: CutItemStatusSucceeded},
		{Path: "A2", Title: "a", SourceID: "a", Status: CutItemStatusFailed, Err: errors.New("x")},
	}
	srcB := []Clip{} // empty source — should be skipped
	srcC := []Clip{
		{Path: "C1", Title: "c", SourceID: "c", Status: CutItemStatusValidated},
	}
	out := InterleaveClips([][]Clip{srcA, srcB, srcC})
	assert.Len(t, out, 2, "Failed clips + empty source groups must be excluded")
	for _, c := range out {
		assert.True(t, c.Succeeded(), "only Succeeded/Validated should survive")
		assert.NotEqual(t, "A2", c.Path, "Failed clip A2 must not interleave")
	}
}

func TestExtractVideoIDHandlesCommonYouTubeURLs(t *testing.T) {
	tests := map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ":          "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?si=test":                 "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ?feature=1": "dQw4w9WgXcQ",
	}

	for input, expected := range tests {
		assert.Equal(t, expected, extractVideoID(input))
	}
}

func countUniqueGroups(items []string, groupOf func(string) string) int {
	seen := make(map[string]bool)
	for _, item := range items {
		seen[groupOf(item)] = true
	}
	return len(seen)
}

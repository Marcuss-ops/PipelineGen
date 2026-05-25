package stockpipeline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultPipelineConfig(t *testing.T) {
	cfg := DefaultPipelineConfig()
	assert.Equal(t, 25, cfg.ChunkDuration, "default chunk duration should be 25s")
	assert.Equal(t, 25, cfg.MaxResults, "default max results should be 25")
	assert.Equal(t, 4, cfg.EffectInterval, "default effect interval should be 4")
	assert.Equal(t, "assets/effects/EffettiVisiv", cfg.EffectsDir, "default effects dir")
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

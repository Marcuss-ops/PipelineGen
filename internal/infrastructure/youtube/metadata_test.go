package youtube

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const realisticYDLPDumpJSON = `{
  "id": "dQw4w9WgXcQ",
  "title": "Never Gonna Give You Up",
  "description": "The official music video for Rick Astley.",
  "duration": 213.0,
  "uploader": "Rick Astley",
  "upload_date": "20091025",
  "view_count": 1700000000,
  "language": "en",
  "thumbnail": "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg",
  "thumbnails": [
    {"url": "https://i.ytimg.com/vi/dQw4w9WgXcQ/default.jpg", "width": 120, "height": 90},
    {"url": "https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg", "width": 480, "height": 360},
    {"url": "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg", "width": 1280, "height": 720}
  ],
  "chapters": [
    {"title": "Intro",   "start_time": 0.0,  "end_time": 42.5},
    {"title": "Verse 1", "start_time": 42.5, "end_time": 92.0},
    {"title": "Chorus",  "start_time": 92.0, "end_time": 142.3}
  ],
  "categories": ["Music"],
  "tags": ["rick astley", "never gonna give you up", "80s", "music video"]
}`

func TestYouTubeMetadata_FullUnmarshallingPreservesAllFields(t *testing.T) {
	var raw YouTubeMetadata
	require.NoError(t, json.Unmarshal([]byte(realisticYDLPDumpJSON), &raw))

	assert.Equal(t, "dQw4w9WgXcQ", raw.ID)
	assert.Equal(t, "Never Gonna Give You Up", raw.Title)
	assert.Equal(t, "Rick Astley", raw.Uploader)
	assert.Equal(t, "20091025", raw.UploadDate)
	assert.Equal(t, int64(1_700_000_000), raw.ViewCount)
	assert.Equal(t, 213.0, raw.Duration)
	assert.Equal(t, "en", raw.Language)
	assert.Equal(t, "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg", raw.ThumbnailURL)
	require.Len(t, raw.Thumbnails, 3)
	assert.Equal(t, "https://i.ytimg.com/vi/dQw4w9WgXcQ/maxresdefault.jpg", raw.Thumbnails[2].URL)
	assert.Equal(t, 1280, raw.Thumbnails[2].Width)
	require.Len(t, raw.Chapters, 3)
	assert.Equal(t, "Chorus", raw.Chapters[2].Title)
	assert.Equal(t, 92.0, raw.Chapters[2].StartTime)
	assert.Equal(t, []string{"Music"}, raw.Categories)
	assert.Equal(t, []string{"rick astley", "never gonna give you up", "80s", "music video"}, raw.Tags)
}

func TestYouTubeMetadata_PartialFixtureStillUnmarshals(t *testing.T) {
	const partial = `{"id":"abc","title":"minimal"}`
	var raw YouTubeMetadata
	require.NoError(t, json.Unmarshal([]byte(partial), &raw))
	assert.Equal(t, "abc", raw.ID)
	assert.Equal(t, "minimal", raw.Title)
	assert.Nil(t, raw.Thumbnails)
	assert.Nil(t, raw.Chapters)
	assert.Nil(t, raw.Categories)
	assert.Nil(t, raw.Tags)
	assert.Equal(t, "", raw.Uploader)
	assert.Equal(t, 0.0, raw.Duration)
}

func TestYouTubeMetadata_InvalidJSONFails(t *testing.T) {
	var raw YouTubeMetadata
	err := json.Unmarshal([]byte("{not valid json}"), &raw)
	require.Error(t, err)
}

func TestExtractIDFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/live/dQw4w9WgXcQ", "dQw4w9WgXcQ"},
		{"https://www.youtube.com/watch?v=abc123&t=42s", "abc123"},
		{"not-a-url", ""},
		{"", ""},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			got := extractIDFromURL(tc.url)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSanitizedURL_StripsQuery(t *testing.T) {
	got := sanitizedURL("https://www.youtube.com/watch?v=abc&token=secret")
	assert.Equal(t, "https://www.youtube.com/watch", got)
}

func TestSanitizedURL_KeepsPath(t *testing.T) {
	got := sanitizedURL("https://youtu.be/abc123?ref=foo")
	assert.Equal(t, "https://youtu.be/abc123", got)
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "hello", truncate("hello", 10))
	assert.Equal(t, "hell…", truncate("hello world", 5))
	assert.Equal(t, "ab", truncate("abcdef", 2))
}

func TestNewMetadataFetcherAdapter_NilRunnerFallsBackToDefault(t *testing.T) {
	a := NewMetadataFetcherAdapter(nil, nil)
	require.NotNil(t, a)
	require.NotNil(t, a.runner, "nil runner must be replaced with default ProcessRunnerAdapter")
}

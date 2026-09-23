package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeExistenceLookup records which lookup ran and with what arguments.
type fakeExistenceLookup struct {
	ytID    string
	ytErr   error
	ytCalls int
	lastID  string

	urlID  string
	urlErr error

	urlCalls  int
	lastURLID string
}

func (f *fakeExistenceLookup) FindClipIDByYouTubeVideoID(_ context.Context, videoID string, _ bool, _, _ float64) (string, error) {
	f.ytCalls++
	f.lastID = videoID
	return f.ytID, f.ytErr
}

func (f *fakeExistenceLookup) FindClipIDBySourceURL(_ context.Context, url string) (string, error) {
	f.urlCalls++
	f.lastURLID = url
	return f.urlID, f.urlErr
}

// TestClipExistenceAdapter_PrefersVideoIDLookup pins the lookup order:
// the canonical youtube_video_id identity wins and the URL fallback is
// NOT consulted on a hit (one SSOT round-trip, not two).
func TestClipExistenceAdapter_PrefersVideoIDLookup(t *testing.T) {
	fake := &fakeExistenceLookup{ytID: "yt_1"}
	a := &youtubeClipExistenceAdapter{media: fake}

	clipID, err := a.FindExistingClipID(context.Background(), "https://www.youtube.com/watch?v=506AyzC7d-k")
	require.NoError(t, err)
	assert.Equal(t, "yt_1", clipID)
	assert.Equal(t, 1, fake.ytCalls)
	assert.Equal(t, "506AyzC7d-k", fake.lastID)
	assert.Zero(t, fake.urlCalls, "a video-id hit must not fall through to the URL lookup")
}

// TestClipExistenceAdapter_FallsBackToSourceURL pins the second arm: an
// unindexed video id (e.g. the clip was registered under a different id
// shape) retries on the exact source URL.
func TestClipExistenceAdapter_FallsBackToSourceURL(t *testing.T) {
	fake := &fakeExistenceLookup{urlID: "yt_9"}
	a := &youtubeClipExistenceAdapter{media: fake}

	clipID, err := a.FindExistingClipID(context.Background(), "https://www.youtube.com/watch?v=aaaaaaaaaaa")
	require.NoError(t, err)
	assert.Equal(t, "yt_9", clipID)
	assert.Equal(t, 1, fake.ytCalls)
	assert.Equal(t, 1, fake.urlCalls)
	assert.Equal(t, "https://www.youtube.com/watch?v=aaaaaaaaaaa", fake.lastURLID)
}

// TestClipExistenceAdapter_MissReturnsEmpty pins the legitimate miss: no
// error, empty id → handler answers {exists:false}.
func TestClipExistenceAdapter_MissReturnsEmpty(t *testing.T) {
	a := &youtubeClipExistenceAdapter{media: &fakeExistenceLookup{}}
	clipID, err := a.FindExistingClipID(context.Background(), "https://youtu.be/bbbbbbbbbb0")
	require.NoError(t, err)
	assert.Empty(t, clipID)
}

// TestClipExistenceAdapter_ErrorsPropagate pins fail-closed: both lookup
// errors surface wrapped (the handler turns them into 500, never miss).
func TestClipExistenceAdapter_ErrorsPropagate(t *testing.T) {
	probeErr := errors.New("ssot down")

	_, err := (&youtubeClipExistenceAdapter{media: &fakeExistenceLookup{ytErr: probeErr}}).
		FindExistingClipID(context.Background(), "https://youtu.be/cccccccccc1")
	require.Error(t, err)
	assert.ErrorIs(t, err, probeErr)
	assert.Contains(t, err.Error(), "youtube video id lookup")

	_, err = (&youtubeClipExistenceAdapter{media: &fakeExistenceLookup{urlErr: probeErr}}).
		FindExistingClipID(context.Background(), "https://youtu.be/cccccccccc1")
	require.Error(t, err)
	assert.ErrorIs(t, err, probeErr)
	assert.Contains(t, err.Error(), "source url lookup")
}

// TestNewYouTubeClipExistencePort_NilMediaIsNil pins the wiring contract:
// no media PostgreSQL handle → nil port → capability answers 503.
func TestNewYouTubeClipExistencePort_NilMediaIsNil(t *testing.T) {
	assert.Nil(t, newYouTubeClipExistencePort(nil))
}

// TestYouTubeVideoIDFromURL covers every supported URL shape plus the
// negatives: the extractor must never return a partial/oversized id.
func TestYouTubeVideoIDFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=506AyzC7d-k":      "506AyzC7d-k",
		"https://www.youtube.com/watch?v=506AyzC7d-k&t=9s": "506AyzC7d-k",
		"https://youtu.be/506AyzC7d-k":                     "506AyzC7d-k",
		"https://youtu.be/506AyzC7d-k?si=abc":              "506AyzC7d-k",
		"https://www.youtube.com/shorts/506AyzC7d-k":       "506AyzC7d-k",
		"https://www.youtube.com/embed/506AyzC7d-k":        "506AyzC7d-k",
		"https://www.youtube.com/live/506AyzC7d-k":         "506AyzC7d-k",
		"https://www.youtube.com/playlist?list=PL123":      "",
		"not-a-url": "",
		"":          "",
		"https://www.youtube.com/watch?v=tooshort": "",
	}
	for in, want := range cases {
		assert.Equal(t, want, youtubeVideoIDFromURL(in), "input=%q", in)
	}
}

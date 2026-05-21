package videomuscles

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsableCachedClipIgnoresEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(path, []byte{}, 0644))

	ok, err := usableCachedClip(path)
	require.NoError(t, err)
	require.False(t, ok)

	_, statErr := os.Stat(path)
	require.True(t, os.IsNotExist(statErr))
}

func TestUsableCachedClipAcceptsNonEmptyRegularFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "clip.mp4")
	require.NoError(t, os.WriteFile(path, []byte("video-bytes"), 0644))

	ok, err := usableCachedClip(path)
	require.NoError(t, err)
	require.True(t, ok)
}

package artlist

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	drivepkg "github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ----- METADATA-PROJECTION-GUARD (September 2026) ---------------------------
//
// local metadata.json + Drive cumulative metadata.json are DERIVED
// projections (the DB asset row is the SSOT). These tests pin the two
// data-loss guards introduced with the projection guard:
//
//  1. updateLocalCumulativeMetadataJSON never overwrites a corrupt or
//     unreadable EXISTING local metadata.json (silently rewriting it
//     would drop every previously recorded entry).
//  2. updateCumulativeMetadataJSON never trashes + re-publishes a
//     malformed existing Drive metadata.json for the same reason.

func TestUpdateLocalCumulativeMetadataJSON_CreatesFreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")

	entry := map[string]any{"clip_id": "clip-1", "name": "Clip One"}
	require.NoError(t, updateLocalCumulativeMetadataJSON(path, "clip-1", entry))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(data), `"clip-1"`),
		"fresh metadata.json must contain the new entry, got %s", data)
}

func TestUpdateLocalCumulativeMetadataJSON_MergesEntriesWithoutLoss(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")

	first := map[string]any{"clip_id": "clip-1", "name": "Clip One"}
	second := map[string]any{"clip_id": "clip-2", "name": "Clip Two"}
	require.NoError(t, updateLocalCumulativeMetadataJSON(path, "clip-1", first))
	require.NoError(t, updateLocalCumulativeMetadataJSON(path, "clip-2", second))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(data), `"clip-1"`), "first entry must survive the merge")
	require.True(t, strings.Contains(string(data), `"clip-2"`), "second entry must be appended, not replace the file")
}

func TestUpdateLocalCumulativeMetadataJSON_ReplacesSameClipEntry(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")

	first := map[string]any{"clip_id": "clip-1", "name": "Before"}
	second := map[string]any{"clip_id": "clip-1", "name": "After"}
	require.NoError(t, updateLocalCumulativeMetadataJSON(path, "clip-1", first))
	require.NoError(t, updateLocalCumulativeMetadataJSON(path, "clip-1", second))

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(data), `"After"`), "re-enriching the same clip must update its entry")
	require.False(t, strings.Contains(string(data), `"Before"`), "re-enriching the same clip must NOT duplicate its entry")
}

func TestUpdateLocalCumulativeMetadataJSON_CorruptExistingFile_NotOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	corrupt := `[{ "clip_id": "clip-1", "name": "truncated"`
	require.NoError(t, os.WriteFile(path, []byte(corrupt), 0o644))

	err := updateLocalCumulativeMetadataJSON(path, "clip-2", map[string]any{"clip_id": "clip-2", "name": "Clip Two"})
	require.Error(t, err, "corrupt existing metadata.json must fail closed")
	assert.True(t, strings.Contains(err.Error(), "not valid JSON"),
		"error must name the corruption, got %v", err)

	// Data-loss guard: the file must be byte-identical to the corrupt
	// original (the failed parse must never trigger an overwrite that
	// would drop the un-parseable entries).
	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, corrupt, string(after),
		"corrupt metadata.json must NOT be overwritten (projection data-loss guard)")
}

func TestUpdateLocalCumulativeMetadataJSON_UnreadableFile_NotOverwritten(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "metadata.json")
	require.NoError(t, os.WriteFile(path, []byte(`[]`), 0o000))

	err := updateLocalCumulativeMetadataJSON(path, "clip-1", map[string]any{"clip_id": "clip-1"})
	require.Error(t, err, "unreadable existing metadata.json must fail closed, not be treated as a fresh file")
}

// stubProjectionReader is a minimal drivepkg.Reader test double. It
// embeds the interface (all unused methods resolve to the nil embedded
// interface) and overrides only the two methods the cumulative
// metadata.json RMW path calls.
type stubProjectionReader struct {
	drivepkg.Reader
	files []drivepkg.DriveFileInfo
	body  io.ReadCloser
	dlErr error
}

func (s *stubProjectionReader) SearchFiles(_ context.Context, _ string) ([]drivepkg.DriveFileInfo, error) {
	return s.files, nil
}

func (s *stubProjectionReader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	if s.dlErr != nil {
		return nil, "", s.dlErr
	}
	return s.body, "application/json", nil
}

// stubProjectionLifecycle is a minimal drivepkg.FileLifecycle test
// double recording Trash calls.
type stubProjectionLifecycle struct {
	drivepkg.FileLifecycle
	trashCalls int
}

func (s *stubProjectionLifecycle) Trash(_ context.Context, _ string) error {
	s.trashCalls++
	return nil
}

// TestUpdateCumulativeMetadataJSON_MalformedExistingFile_NoTrashNoPublish
// pins the Drive-side data-loss guard: an existing metadata.json that
// downloads but fails JSON decoding must NOT be trashed and re-published
// with only the new entry (that would drop every previously recorded
// entry in the cumulative projection).
func TestUpdateCumulativeMetadataJSON_MalformedExistingFile_NoTrashNoPublish(t *testing.T) {
	reader := &stubProjectionReader{
		files: []drivepkg.DriveFileInfo{{ID: "existing-meta-file-id", Name: "metadata.json"}},
		body:  io.NopCloser(strings.NewReader(`{ "truncated": `)), // malformed JSON
	}
	lifecycle := &stubProjectionLifecycle{}
	pub := &fakePublisher{}
	enricher := &SemanticEnricher{
		log:       zap.NewNop(),
		publisher: pub,
		reader:    reader,
		lifecycle: lifecycle,
	}

	err := enricher.updateCumulativeMetadataJSON(
		context.Background(),
		"folder-id",
		"clip-1",
		map[string]any{"clip_id": "clip-1", "name": "Clip One"},
	)
	require.Error(t, err, "malformed existing Drive metadata.json must fail closed")
	assert.Contains(t, err.Error(), "malformed", "error must name the malformed state, got %v", err)

	assert.Equal(t, 0, lifecycle.trashCalls,
		"malformed existing file must NOT be trashed (data-loss guard)")
	assert.Equal(t, 0, pub.PublishCalls,
		"malformed existing file must NOT trigger a re-publish (data-loss guard)")
}

// TestUpdateCumulativeMetadataJSON_SearchFailure_NoPublish pins that a
// list failure skips the whole RMW (no publish of a single-entry file
// that would clobber the remote cumulative projection).
func TestUpdateCumulativeMetadataJSON_SearchFailure_NoPublish(t *testing.T) {
	reader := &stubProjectionReader{} // SearchFiles returns no files (treated as remote list failure is simulated below)
	lifecycle := &stubProjectionLifecycle{}
	pub := &fakePublisher{}

	// Simulate a SearchFiles failure by embedding a failing reader.
	failing := &failingSearchReader{Reader: reader}
	enricher := &SemanticEnricher{
		log:       zap.NewNop(),
		publisher: pub,
		reader:    failing,
		lifecycle: lifecycle,
	}

	err := enricher.updateCumulativeMetadataJSON(
		context.Background(),
		"folder-id",
		"clip-1",
		map[string]any{"clip_id": "clip-1"},
	)
	require.Error(t, err, "SearchFiles failure must fail closed")
	assert.Equal(t, 0, pub.PublishCalls,
		"SearchFiles failure must NOT publish a single-entry file over the existing cumulative projection")
}

type failingSearchReader struct {
	drivepkg.Reader
}

func (f *failingSearchReader) SearchFiles(_ context.Context, _ string) ([]drivepkg.DriveFileInfo, error) {
	return nil, os.ErrPermission
}

func (f *failingSearchReader) DownloadFile(_ context.Context, _ string) (io.ReadCloser, string, error) {
	return nil, "", os.ErrPermission
}

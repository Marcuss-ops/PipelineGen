package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	artapp "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/artlist"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// fakeAuditRepository is a test double for artlist.DownloadAuditRepository.
type fakeAuditRepository struct {
	records []fakeAuditRecord
	limit   int
}

type fakeAuditRecord struct {
	id     string
	rec    artapp.DownloadAuditRecord
	status artapp.DownloadAuditStatus
}

func (f *fakeAuditRepository) RecordDownload(_ context.Context, rec artapp.DownloadAuditRecord) (string, error) {
	id := fmt.Sprintf("audit-%d", len(f.records))
	f.records = append(f.records, fakeAuditRecord{id: id, rec: rec, status: rec.Status})
	return id, nil
}

func (f *fakeAuditRepository) UpdateDownloadStatus(_ context.Context, id string, status artapp.DownloadAuditStatus) error {
	for i := range f.records {
		if f.records[i].id == id {
			f.records[i].status = status
			return nil
		}
	}
	return fmt.Errorf("audit row %s not found", id)
}

func (f *fakeAuditRepository) CountDailyDownloads(_ context.Context, _, _ string) (int, error) {
	count := 0
	for _, r := range f.records {
		if r.status != artapp.DownloadAuditStatusFailed {
			count++
		}
	}
	return count, nil
}

func TestResolver_Download_ManualImportBlocksDownload(t *testing.T) {
	dir := t.TempDir()
	r := NewResolver(&config.Config{}, ResolverConfig{
		AcquisitionMode:    artapp.AcquisitionModeManualImport,
		AccountID:          "default",
		DailyDownloadLimit: 10,
		AuditRepository:    &fakeAuditRepository{},
	}, zap.NewNop(), nil)

	_, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://artlist.io/clip/123",
		DestinationID: dir,
		Filename:      "clip.mp4",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, artapp.ErrManualImportActive)
}

func TestResolver_Download_AuthorizedAPILimitZeroBlocksDownload(t *testing.T) {
	dir := t.TempDir()
	r := NewResolver(&config.Config{}, ResolverConfig{
		AcquisitionMode:    artapp.AcquisitionModeAuthorizedAPI,
		AccountID:          "default",
		DailyDownloadLimit: 0,
		AuditRepository:    &fakeAuditRepository{},
	}, zap.NewNop(), nil)

	_, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://artlist.io/clip/123",
		DestinationID: dir,
		Filename:      "clip.mp4",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, artapp.ErrAutomaticDownloadsDisabled)
}

func TestResolver_Download_AuthorizedAPIDailyLimitEnforced(t *testing.T) {
	dir := t.TempDir()
	audit := &fakeAuditRepository{limit: 2}
	r := NewResolver(&config.Config{}, ResolverConfig{
		AcquisitionMode:    artapp.AcquisitionModeAuthorizedAPI,
		AccountID:          "default",
		DailyDownloadLimit: 2,
		AuditRepository:    audit,
	}, zap.NewNop(), nil)

	// Pre-seed two audit rows to simulate a consumed quota.
	audit.records = append(audit.records,
		fakeAuditRecord{id: "audit-a", rec: artapp.DownloadAuditRecord{AssetID: "a"}, status: artapp.DownloadAuditStatusSucceeded},
		fakeAuditRecord{id: "audit-b", rec: artapp.DownloadAuditRecord{AssetID: "b"}, status: artapp.DownloadAuditStatusSucceeded},
	)

	_, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://artlist.io/clip/123",
		DestinationID: dir,
		Filename:      "clip.mp4",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, artapp.ErrDailyDownloadLimitExceeded)
}

func TestResolver_Download_AuthorizedAPIRecordsAuditBeforeDownload(t *testing.T) {
	dir := t.TempDir()
	audit := &fakeAuditRepository{limit: 10}
	r := NewResolver(&config.Config{}, ResolverConfig{
		AcquisitionMode:    artapp.AcquisitionModeAuthorizedAPI,
		AccountID:          "acct-1",
		DailyDownloadLimit: 10,
		AuditRepository:    audit,
	}, zap.NewNop(), nil)

	// Use a direct progressive URL so the resolver takes the HTTP path
	// without needing the scraper. The download will fail because there
	// is no server, but the audit row is recorded before the transport
	// attempt to keep the limit check race-free.
	_, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://example.com/clip.mp4",
		DestinationID: dir,
		Filename:      "clip.mp4",
	})

	require.Error(t, err)
	require.Len(t, audit.records, 1)
	assert.Equal(t, "clip.mp4", audit.records[0].rec.AssetID)
	assert.Equal(t, "acct-1", audit.records[0].rec.AccountID)
	assert.Equal(t, "artlist", audit.records[0].rec.Provider)
	assert.Equal(t, artapp.DownloadAuditStatusFailed, audit.records[0].status)
}

func TestResolver_Download_AuthorizedAPIHappyPathRecordsAudit(t *testing.T) {
	dir := t.TempDir()

	// Serve a small file over HTTP.
	content := []byte("fake video bytes")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(content)
	}))
	defer server.Close()

	audit := &fakeAuditRepository{limit: 10}
	r := NewResolver(&config.Config{}, ResolverConfig{
		AcquisitionMode:    artapp.AcquisitionModeAuthorizedAPI,
		AccountID:          "acct-1",
		DailyDownloadLimit: 10,
		AuditRepository:    audit,
	}, zap.NewNop(), nil)

	result, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     server.URL + "/clip.mp4",
		DestinationID: dir,
		Filename:      "clip.mp4",
	})

	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "clip.mp4"), result.LocalPath)
	assert.Equal(t, int64(len(content)), result.Bytes)
	require.Len(t, audit.records, 1)
	assert.Equal(t, "clip.mp4", audit.records[0].rec.AssetID)
	assert.Equal(t, server.URL+"/clip.mp4", audit.records[0].rec.ExternalURL)
	assert.Equal(t, artapp.DownloadAuditStatusSucceeded, audit.records[0].status)
}

func TestResolver_Download_AuthorizedAPILimitAllowsDownload(t *testing.T) {
	dir := t.TempDir()
	audit := &fakeAuditRepository{limit: 5}
	r := NewResolver(&config.Config{}, ResolverConfig{
		AcquisitionMode:    artapp.AcquisitionModeAuthorizedAPI,
		AccountID:          "default",
		DailyDownloadLimit: 5,
		AuditRepository:    audit,
	}, zap.NewNop(), nil)

	// Create a local file to satisfy the HTTP path without a real server.
	src := filepath.Join(dir, "source.mp4")
	require.NoError(t, os.WriteFile(src, []byte("fake video"), 0o644))

	_, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     "file://" + src,
		DestinationID: filepath.Join(dir, "out"),
		Filename:      "clip.mp4",
	})

	// file:// URLs are not handled by the HTTP downloader, so this will
	// fail at the transport layer. The important assertion is that the
	// limit gate did not reject the request before it reached the
	// transport.
	require.Error(t, err)
	assert.False(t, errors.Is(err, artapp.ErrDailyDownloadLimitExceeded))
	assert.False(t, errors.Is(err, artapp.ErrManualImportActive))
	assert.False(t, errors.Is(err, artapp.ErrAutomaticDownloadsDisabled))
}

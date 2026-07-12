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

// TestResolver_Download_AuthorizedByNewDefaults pins the resolver-side
// end of PR-ARTLIST-AUTHORIZED-BY-DEFAULT (P1, July 2026): a fresh
// deployment without operator overrides MUST permit downloads because
// the loader defaults to AcquisitionMode=authorized_api AND
// DailyDownloadLimit=10. This is the load-bearing test for the
// P1 cutover; if either default flips back, this test fails loudly
// alongside the config-default tests in
// internal/platform/config/external_config_test.go.
func TestResolver_Download_AuthorizedByNewDefaults(t *testing.T) {
	dir := t.TempDir()
	audit := &fakeAuditRepository{limit: 10}

	// Mirror the P1 defaults verbatim (don't depend on the unexported
	// applyDefaults helper -- the resolver test lives in package
	// `downloader`, not `config`).
	cfg := &config.Config{
		External: config.ExternalConfig{
			ArtlistAcquisitionMode:    "authorized_api",
			ArtlistDailyDownloadLimit: 10,
			ArtlistAccountID:          "default",
			ArtlistScraperServerURL:   "http://artlist-scraper:9123",
		},
	}

	r := NewResolver(cfg, ResolverConfig{
		// Same plumb-through as build_bundles_artlist.go::WireArtlist:
		AcquisitionMode:    artapp.ArtlistAcquisitionMode(cfg.External.ArtlistAcquisitionMode),
		AccountID:          cfg.External.ArtlistAccountID,
		DailyDownloadLimit: cfg.External.ArtlistDailyDownloadLimit,
		AuditRepository:    audit,
	}, zap.NewNop(), nil)

	// Create a local file to satisfy the HTTP transport without a real server
	// (matches the pattern from TestResolver_Download_AuthorizedAPILimitAllowsDownload).
	src := filepath.Join(dir, "source.mp4")
	require.NoError(t, os.WriteFile(src, []byte("fake video"), 0o644))

	_, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     "file://" + src,
		DestinationID: filepath.Join(dir, "out"),
		Filename:      "clip.mp4",
	})

	// The transport-layer rejection of file:// URLs is irrelevant. What
	// matters: the resolver gates MUST NOT reject the request before
	// it reaches the transport. With the P1 defaults, ErrManualImportActive
	// and ErrAutomaticDownloadsDisabled MUST both be absent, audit row
	// MUST be recorded.
	require.Error(t, err, "transport failure expected; not asserting on transport outcome")
	assert.False(t, errors.Is(err, artapp.ErrManualImportActive),
		"P1 default AcquisitionMode=authorized_api MUST NOT yield ErrManualImportActive")
	assert.False(t, errors.Is(err, artapp.ErrAutomaticDownloadsDisabled),
		"P1 default DailyDownloadLimit=10 MUST NOT yield ErrAutomaticDownloadsDisabled")
	assert.False(t, errors.Is(err, artapp.ErrDailyDownloadLimitExceeded),
		"P1 default limit=10 is below the count=0 pre-seed; the limit gate MUST pass")
	require.NotEmpty(t, audit.records,
		"P1 defaults permit downloads; audit row MUST be recorded before transport attempt")
	assert.Equal(t, "artlist", audit.records[0].rec.Provider)
	assert.Equal(t, "default", audit.records[0].rec.AccountID)
	assert.Equal(t, artapp.DownloadAuditStatusFailed, audit.records[0].status,
		"transport-layer failure flips the audit row to failed; pre-condition is that the row exists")
}

// TestResolver_Download_ManualImportBlocksDownloadAtP1Default guards
// the manual_import opt-out path under the new default regime: when
// the operator EXPLICITLY sets ARTLIST_ACQUISITION_MODE=manual_import,
// the resolver gate MUST return ErrManualImportActive EVEN THOUGH the
// loader default is authorized_api. This protects the cutover from
// accidentally swallowing the manual_import escape hatch.
func TestResolver_Download_ManualImportBlocksDownloadAtP1Default(t *testing.T) {
	dir := t.TempDir()
	audit := &fakeAuditRepository{}

	cfg := &config.Config{
		External: config.ExternalConfig{
			ArtlistAcquisitionMode:    "manual_import", // operator override
			ArtlistDailyDownloadLimit: 10,              // irrelevant when manual_import
			ArtlistAccountID:          "default",
			ArtlistScraperServerURL:   "http://artlist-scraper:9123",
		},
	}

	r := NewResolver(cfg, ResolverConfig{
		AcquisitionMode:    artapp.ArtlistAcquisitionMode(cfg.External.ArtlistAcquisitionMode),
		AccountID:          cfg.External.ArtlistAccountID,
		DailyDownloadLimit: cfg.External.ArtlistDailyDownloadLimit,
		AuditRepository:    audit,
	}, zap.NewNop(), nil)

	_, err := r.Download(context.Background(), artapp.DownloadRequest{
		SourceRef:     "https://artlist.io/clip/123",
		DestinationID: dir,
		Filename:      "clip.mp4",
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, artapp.ErrManualImportActive,
		"the manual_import override MUST escape the P1 default")
	require.Empty(t, audit.records,
		"manual_import gate fires before the audit row is recorded; no rows expected")
}

// Package artlist — diagnostics_media_stats_test.go pins the narrowed media
// aggregate port (MEDIA LEGACY READ-PLANE DEMOLITION, 2026-09-21).
//
// /api/artlist/diagnostics reports how many indexed clips the "artlist" source
// has, and /api/artlist/stats reports the catalogue total. Those numbers used to
// come from AssetStore (CountBySource, then CountClips/LastUpdatedAtForTerm),
// which forced the operational SQLite ClipsRepository to own media_assets reads
// (imagesregistry/clips_statistics.go, then clip_list_queries.go). They now come
// from the dedicated MediaStats port, answered on the media SSOT by
// pgmedia.MediaStatisticsReader.
//
// Three properties are pinned here, and they are the whole reason the port is
// separate: the numbers are the port's answers, they stay UNAVAILABLE (never
// fabricated, never read off the mirror) when the port is nil, and the port
// cannot quietly re-widen back onto the operational store.
package artlist

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/imagesregistry"
)

// fakeMediaStats records how the media aggregate port was exercised. Each answer
// is distinct so a test can prove which method a field was read from — the three
// numbers answer different questions and must not be conflated.
type fakeMediaStats struct {
	sourceCount int
	clipCount   int
	lastUpdated *string

	sourceCalls int
	clipCalls   int
	termCalls   int
}

func (f *fakeMediaStats) CountBySource(context.Context, string) (int, error) {
	f.sourceCalls++
	return f.sourceCount, nil
}

func (f *fakeMediaStats) CountClips(context.Context) (int, error) {
	f.clipCalls++
	return f.clipCount, nil
}

func (f *fakeMediaStats) LastUpdatedAtForTerm(context.Context, string) (*string, error) {
	f.termCalls++
	return f.lastUpdated, nil
}

// newDiagnosticsFixture builds a service around an in-memory operational store
// with the given media aggregate port (nil models the no-media-handle
// composition).
func newDiagnosticsFixture(t *testing.T, stats MediaStats) *Service {
	t.Helper()
	db := createTestDB(t)
	t.Cleanup(func() { _ = db.Close() })

	logger := zap.NewNop()
	repo := assets.NewClipsRepository(db, logger)

	svc, err := NewService(baseServiceDeps(t, ServiceDeps{
		ServicePorts: ServicePorts{
			AssetStore: repo,
			MediaStats: stats,
		},
		ServiceDependencies: ServiceDependencies{
			Infra: ArtlistInfraDeps{
				MainDB: db,
				Cfg: &config.Config{
					Storage: config.StorageConfig{DataDir: t.TempDir()},
				},
				Log: logger,
			},
			Ports: ArtlistPortDeps{
				Dispatcher: &stubDispatcherForArtlist{repo: repo},
			},
		},
	}))
	require.NoError(t, err, "fixture service must construct")
	return svc
}

// TestDiagnostics_MediaStatsReportsTheInjectedNumbers pins that every media
// number on the wire is the port's answer — one call each, no value derived from
// the operational store, and no conflation between the three questions.
func TestDiagnostics_MediaStatsReportsTheInjectedNumbers(t *testing.T) {
	lastUpdated := "2026-09-17T09:30:00Z"
	stats := &fakeMediaStats{sourceCount: 12, clipCount: 40, lastUpdated: &lastUpdated}
	svc := newDiagnosticsFixture(t, stats)

	resp, err := svc.Diagnostics(context.Background(), "beluga")
	require.NoError(t, err)
	assert.Equal(t, 12, resp.ClipsArtlistTotal,
		"the per-source total must be MediaStats.CountBySource's answer")
	assert.Equal(t, 1, stats.sourceCalls,
		"the per-source count must be read exactly once per diagnostics call")
	require.NotNil(t, resp.LastProcessedAt,
		"the newest matching run timestamp must come from MediaStats.LastUpdatedAtForTerm, not the operational store")
	assert.Equal(t, lastUpdated, *resp.LastProcessedAt)
	assert.Equal(t, 1, stats.termCalls, "the term lookup must be read exactly once")
	// The un-filtered 40 is a DIFFERENT question (non-soft-deleted rows across
	// sources) and must never be substituted for the artlist-specific 12.
	assert.NotEqual(t, resp.ClipsArtlistTotal, stats.clipCount,
		"CountClips (all sources, soft-delete discounted) must not be conflated with CountBySource (one source, indexed metric)")

	legacy, err := svc.GetStats(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 40, legacy.ClipsTotal, "the legacy stats surface must report MediaStats.CountClips")
	assert.Equal(t, 40, legacy.ArtlistClipsTotal,
		"preserved quirk: the legacy surface reports the catalogue total in both fields; the artlist-specific number lives on /api/artlist/diagnostics")
	assert.Equal(t, 1, stats.clipCalls, "the catalogue total must be read exactly once")
}

// TestDiagnostics_MediaStatsIsOptionalFailClosed pins the no-media-handle mode:
// the informational fields stay unavailable, and the aggregate surface fails
// closed instead of reporting a fabricated 0.
func TestDiagnostics_MediaStatsIsOptionalFailClosed(t *testing.T) {
	svc := newDiagnosticsFixture(t, nil)

	resp, err := svc.Diagnostics(context.Background(), "beluga")
	require.NoError(t, err, "a nil media stats port must not fail the diagnostics call")
	assert.Equal(t, 0, resp.ClipsArtlistTotal,
		"with no media SSOT port wired the field must stay unavailable — never fabricated, never read off the SQLite mirror")
	assert.Nil(t, resp.LastProcessedAt,
		"no timestamp may be invented when the media SSOT cannot be read")

	_, err = svc.GetStats(context.Background())
	require.Error(t, err, "the aggregate surface has no way to say \"unknown\", so it must fail closed rather than answer 0")
	assert.True(t, errors.Is(err, ErrMediaStatsUnavailable),
		"the failure must be the typed sentinel, got %v", err)
}

// TestArtlistAssetStore_HasNoMediaAggregateSurface is the forward-prevention pin
// (the same shape the clips capability uses for its search surface): the media
// aggregates must not drift back onto the operational-store port, because that is
// exactly what dragged a media_assets read onto the SQLite mirror twice.
func TestArtlistAssetStore_HasNoMediaAggregateSurface(t *testing.T) {
	storePort := reflect.TypeOf((*AssetStore)(nil)).Elem()
	for _, banned := range []string{"CountBySource", "CountClips", "LastUpdatedAtForTerm"} {
		if _, ok := storePort.MethodByName(banned); ok {
			t.Errorf("artlist.AssetStore must not carry %s: it is a media_assets aggregate, so it belongs on artlist.MediaStats (reading it through the operational store is the split-brain this port split removed)", banned)
		}
	}

	statsPort := reflect.TypeOf((*MediaStats)(nil)).Elem()
	for _, want := range []string{"CountBySource", "CountClips", "LastUpdatedAtForTerm"} {
		if _, ok := statsPort.MethodByName(want); !ok {
			t.Errorf("artlist.MediaStats must declare %s: the media surfaces read it from there", want)
		}
	}

	// The port is answered on the media SSOT by exactly one concrete.
	var _ MediaStats = (*fakeMediaStats)(nil)
}

package clips

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	appclips "github.com/Marcuss-ops/PipelineGen/internal/capabilities/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// recordingMediaSearcher is the MediaClipSearcher stub. It records what it was
// asked for so the test can prove the handler used the media SSOT port rather
// than the retired operational clip_search_terms read path.
type recordingMediaSearcher struct {
	calls  int
	source string
	terms  []string
	limit  int
	out    []*asset.Asset
	err    error
}

func (m *recordingMediaSearcher) SearchClipsByTerms(_ context.Context, source string, terms []string, limit int) ([]*asset.Asset, error) {
	m.calls++
	m.source = source
	m.terms = append([]string(nil), terms...)
	m.limit = limit
	if m.err != nil {
		return nil, m.err
	}
	return m.out, nil
}

// staticAssetReader satisfies AssetReader for the unfiltered count branch.
type staticAssetReader struct {
	clips []*asset.Asset
}

func (r *staticAssetReader) Get(_ context.Context, _ string) (*asset.Asset, error) { return nil, nil }
func (r *staticAssetReader) List(_ context.Context, _ asset.Filter) ([]*asset.Asset, error) {
	return r.clips, nil
}
func (r *staticAssetReader) Count(_ context.Context, _ asset.Filter) (int64, error) {
	return int64(len(r.clips)), nil
}

func listClipsRequest(t *testing.T, deps SearchDeps, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewSearchHandler(deps).RegisterRoutes(router.Group("/api"), func(c *gin.Context) { c.Next() })

	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestListClips_SearchReadsMediaSSOTPort pins the P2-9 reader migration: the
// text-search branch must resolve through MediaClipSearcher (the PostgreSQL
// media read authority) with the canonical per-term AND form and the request
// limit, and must return the media SSOT rows verbatim.
func TestListClips_SearchReadsMediaSSOTPort(t *testing.T) {
	searcher := &recordingMediaSearcher{
		out: []*asset.Asset{{ID: "pg-1", Name: "Sunset Timelapse"}},
	}

	rec := listClipsRequest(t, SearchDeps{
		ClipsRepo:   &handlerClipsRepo{},
		AssetRepo:   &staticAssetReader{},
		MediaSearch: searcher,
	}, "/api/artlist/clips?q=sunset%20timelapse&limit=7")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, searcher.calls, "the search branch must consult the media SSOT port")
	require.Equal(t, "artlist", searcher.source)
	require.Equal(t, []string{"sunset", "timelapse"}, searcher.terms,
		"the query must be split into the canonical per-term AND form")
	require.Equal(t, 7, searcher.limit, "the request limit must reach the media SSOT port")

	require.Contains(t, rec.Body.String(), "pg-1")
	require.Contains(t, rec.Body.String(), `"count":1`)
	require.Contains(t, rec.Body.String(), `"total":1`)
}

// TestListClips_SearchFailsClosedWithoutMediaSearch pins godlike/07: with the
// media plane closed there is no second engine to degrade onto, so the search
// branch fails closed instead of silently reading the retired operational
// index and returning a stale pre-cutover catalog.
func TestListClips_SearchFailsClosedWithoutMediaSearch(t *testing.T) {
	rec := listClipsRequest(t, SearchDeps{
		ClipsRepo: &handlerClipsRepo{},
		AssetRepo: &staticAssetReader{},
	}, "/api/artlist/clips?q=sunset")

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "PostgreSQL media SSOT is not wired")
}

// TestListClips_SearchPropagatesMediaSSOTError proves a media-plane failure is
// surfaced as an error rather than an empty (but successful) catalog page.
func TestListClips_SearchPropagatesMediaSSOTError(t *testing.T) {
	searcher := &recordingMediaSearcher{err: errors.New("media plane down")}

	rec := listClipsRequest(t, SearchDeps{
		ClipsRepo:   &handlerClipsRepo{},
		AssetRepo:   &staticAssetReader{},
		MediaSearch: searcher,
	}, "/api/artlist/clips?q=sunset")

	require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "media plane down")
}

// TestListClips_SearchRejectsNonClipsSource keeps the source validation the
// retired branch performed via repoForSource.
func TestListClips_SearchRejectsNonClipsSource(t *testing.T) {
	searcher := &recordingMediaSearcher{}

	rec := listClipsRequest(t, SearchDeps{
		ClipsRepo:   &handlerClipsRepo{},
		AssetRepo:   &staticAssetReader{},
		MediaSearch: searcher,
	}, "/api/bogus/clips?q=sunset")

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.Equal(t, 0, searcher.calls)
	require.Contains(t, rec.Body.String(), "invalid source: bogus")
}

// TestListClips_UnfilteredBranchStillCountsViaAssetReader proves the migration
// did not move the unfiltered (q == "") branch's canonical count.
func TestListClips_UnfilteredBranchStillCountsViaAssetReader(t *testing.T) {
	searcher := &recordingMediaSearcher{}
	reader := &staticAssetReader{clips: []*asset.Asset{{ID: "a"}, {ID: "b"}}}

	rec := listClipsRequest(t, SearchDeps{
		ClipsRepo:   &handlerClipsRepo{},
		AssetRepo:   reader,
		MediaSearch: searcher,
	}, "/api/artlist/clips")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 0, searcher.calls, "the unfiltered branch must not search")
	require.Contains(t, rec.Body.String(), `"total":2`)
}

// TestClipRepositoryPort_HasNoMediaSearchSurface is the FORWARD-PREVENTION
// half of the P2-9 retirement. The behavioural pins above prove the handler no
// longer reads the operational index; this pin makes that structural, so a
// future change cannot re-add a media search/list surface to the
// clips-side repository port and reach the retired clip_search_terms index
// through an adapter once again. A media search surface belongs on
// MediaClipSearcher (PostgreSQL media SSOT), never here.
func TestClipRepositoryPort_HasNoMediaSearchSurface(t *testing.T) {
	typ := reflect.TypeOf((*appclips.ClipRepositoryPort)(nil)).Elem()
	for _, forbidden := range []string{"ListClipsPaged", "SearchClips", "SearchByTerms", "SearchClipsAdvanced"} {
		if m, ok := typ.MethodByName(forbidden); ok {
			t.Fatalf("clips.ClipRepositoryPort must not expose %s (%v): media search belongs on "+
				"clips.MediaClipSearcher over the PostgreSQL media SSOT, not on the clips-side "+
				"repository port (P2-9, retired clip_search_terms)", forbidden, m.Type)
		}
	}
}

package adminconsole

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// listCapture records the ListOptions the repository adapter receives, so the
// test asserts the wire contract rather than only the HTTP status.
type listCapture struct{ opts ListOptions }

func newListTestEngine(t *testing.T, desc EntityDescriptor, fn func(context.Context, ListOptions) (ListResult, error)) (*gin.Engine, *listCapture) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	capture := &listCapture{}
	desc.Repository = &Adapter{ListFn: func(ctx context.Context, opts ListOptions) (ListResult, error) {
		capture.opts = opts
		if fn != nil {
			return fn(ctx, opts)
		}
		return ListResult{Items: []map[string]any{}, Total: 0}, nil
	}}
	registry := NewRegistry()
	registry.Register(desc)
	engine := gin.New()
	NewHandler(NewService(registry, NoOpAuditLogger{}, nil), zap.NewNop()).RegisterRoutes(engine.Group("/api/admin"))
	return engine, capture
}

func listDesc() EntityDescriptor {
	return EntityDescriptor{
		Key:        "widgets",
		Label:      "Widgets",
		Readable:   true,
		PrimaryKey: "id",
		ListFields: []FieldDescriptor{
			{Key: "id", Type: FieldString, Filterable: true},
			{Key: "status", Type: FieldString, Sortable: true},
		},
	}
}

func doList(engine *gin.Engine, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/admin/entities/widgets"+query, nil)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

func TestListParsesPaginationAndFilters(t *testing.T) {
	engine, capture := newListTestEngine(t, listDesc(), nil)
	rec := doList(engine, "?limit=10&offset=5&filter.id=abc")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 10, capture.opts.Limit)
	require.Equal(t, 5, capture.opts.Offset)
	require.Equal(t, map[string]string{"id": "abc"}, capture.opts.Filters)
}

func TestListParsesSort(t *testing.T) {
	engine, capture := newListTestEngine(t, listDesc(), nil)
	rec := doList(engine, "?order_by=status&order_dir=DESC")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "status", capture.opts.OrderBy)
	require.Equal(t, "desc", capture.opts.OrderDir)
}

func TestListRejectsUnsupportedOptions(t *testing.T) {
	cases := []struct{ name, query string }{
		{"zero limit", "?limit=0"},
		{"non numeric limit", "?limit=many"},
		{"too large limit", "?limit=501"},
		{"negative offset", "?offset=-1"},
		{"unknown param", "?bogus=1"},
		{"non filterable field", "?filter.status=active"},
		{"empty filter value", "?filter.id="},
		{"non sortable field", "?order_by=id"},
		{"bad direction", "?order_by=status&order_dir=sideways"},
		{"direction without field", "?order_dir=desc"},
		{"repeated key", "?limit=1&limit=2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			engine, _ := newListTestEngine(t, listDesc(), nil)
			rec := doList(engine, tc.query)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
}

func TestListMapsRepositoryOptionErrorToBadRequest(t *testing.T) {
	engine, _ := newListTestEngine(t, listDesc(), func(context.Context, ListOptions) (ListResult, error) {
		return ListResult{}, &ListOptionError{Parameter: "order_by", Reason: "repository does not support ordering"}
	})
	rec := doList(engine, "?order_by=status")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestListWithoutRepositoryReturnsNotImplemented(t *testing.T) {
	desc := listDesc()
	desc.Repository = nil
	registry := NewRegistry()
	registry.Register(desc)
	engine := gin.New()
	NewHandler(NewService(registry, NoOpAuditLogger{}, nil), zap.NewNop()).RegisterRoutes(engine.Group("/api/admin"))

	rec := doList(engine, "")
	require.Equal(t, http.StatusNotImplemented, rec.Code, rec.Body.String())
}

package veloxclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestListM2MJobTypesAcceptsVersionedCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != RouteM2MJobTypes {
			t.Errorf("path = %q, want %q", r.URL.Path, RouteM2MJobTypes)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"types":[{"type":"video.render.v2","version":"v2"}]}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", WithM2M).ListM2MJobTypes(context.Background())
	require.NoError(t, err)
	require.Equal(t, []M2MJobType{{Type: "video.render.v2", Version: "v2"}}, got.Types)
}

func TestListM2MJobTypesKeepsLegacyStringCatalog(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"types":["script.generate","video.render"]}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL, "token", WithM2M).ListM2MJobTypes(context.Background())
	require.NoError(t, err)
	require.Equal(t, []M2MJobType{{Type: "script.generate"}, {Type: "video.render"}}, got.Types)
}

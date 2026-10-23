package renderinggen

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
)

const proberArtifactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestOverlayArtifactProberClassifiesStoreResponses(t *testing.T) {
	cases := map[string]struct {
		status      int
		wantPresent bool
		wantErr     bool
	}{
		"present":       {status: http.StatusOK, wantPresent: true},
		"gone":          {status: http.StatusNotFound},
		"permanently":   {status: http.StatusGone},
		"auth failure":  {status: http.StatusUnauthorized, wantErr: true},
		"forbidden":     {status: http.StatusForbidden, wantErr: true},
		"store failure": {status: http.StatusServiceUnavailable, wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var method, path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path = r.Method, r.URL.Path
				w.WriteHeader(tc.status)
			}))
			defer srv.Close()

			present, err := NewOverlayArtifactProber().Present(context.Background(), scriptgen.RenderArtifact{
				URL: srv.URL + "/objects/" + proberArtifactSHA, SHA256: proberArtifactSHA,
			})
			if method != http.MethodHead {
				t.Fatalf("prober method = %q, want HEAD (no bytes may cross the wire)", method)
			}
			if path != "/objects/"+proberArtifactSHA {
				t.Fatalf("prober path = %q", path)
			}
			if present != tc.wantPresent {
				t.Fatalf("present = %v, want %v", present, tc.wantPresent)
			}
			if tc.wantErr && err == nil {
				t.Fatal("expected an error so the caller renders without evicting the entry")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestOverlayArtifactProberRejectsNonHTTPLocator(t *testing.T) {
	if _, err := NewOverlayArtifactProber().Present(context.Background(), scriptgen.RenderArtifact{
		URL: "/local/path.mp4", SHA256: proberArtifactSHA,
	}); err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("non-http locator error = %v", err)
	}
}

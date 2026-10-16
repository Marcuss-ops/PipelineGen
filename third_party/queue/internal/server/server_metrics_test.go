package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/metrics"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
)

func TestMetricsEndpoint(t *testing.T) {
	svc := service.New(memory.New(30*time.Second, 3))
	m := metrics.New()
	svc.SetMetrics(m)

	srv := New(svc)
	srv.SetMetricsHandler(m.Handler())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("metrics: want 200, got %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "renderinggen_jobs_pending") {
		t.Fatalf("metrics body missing jobs_pending metric:\n%s", body)
	}
}

package video

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	"github.com/Marcuss-ops/PipelineGen/pkg/remotionjob"
)

func TestHTTPRenderer(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/render" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"job-1","outputPath":"out/job-1.mp4"}`)), Header: http.Header{}, ContentLength: -1, Request: r}, nil
	})}
	got, err := (&HTTPRenderer{BaseURL: "http://remotion.test", Client: client}).Render(context.Background(), remotionjob.RenderJob{SchemaVersion: remotionjob.SchemaVersion, ID: "job-1", Composition: remotionjob.YouTubeShortComposition})
	if err != nil || got.OutputPath != "out/job-1.mp4" {
		t.Fatalf("result=%+v err=%v", got, err)
	}
}

func TestHTTPRendererRejectsLongformCompositionBeforeNetwork(t *testing.T) {
	called := false
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, fmt.Errorf("network must not be called")
	})}
	_, err := (&HTTPRenderer{BaseURL: "http://remotion.test", Client: client}).Render(context.Background(), remotionjob.RenderJob{
		SchemaVersion: remotionjob.SchemaVersion,
		ID:            "longform",
		Composition:   "LongformCompilationComposition",
	})
	if err == nil || called {
		t.Fatalf("longform render should fail before network: err=%v called=%v", err, called)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

var _ = job.StatusQueued

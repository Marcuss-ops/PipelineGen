package remotejob

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCancelJobMapsTerminalConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete || r.URL.Path != "/api/v1/jobs/job_1" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Fatalf("authorization = %q", got)
		}
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"job_not_cancellable"}`))
	}))
	defer srv.Close()

	err := New(srv.URL, "tok").Cancel(context.Background(), "job_1")
	if !errors.Is(err, ErrJobNotCancellable) {
		t.Fatalf("cancel conflict = %v, want ErrJobNotCancellable", err)
	}
}

func TestCancelJobAcceptsNoContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := New(srv.URL, "tok").Cancel(context.Background(), "job_2"); err != nil {
		t.Fatalf("cancel accepted: %v", err)
	}
}

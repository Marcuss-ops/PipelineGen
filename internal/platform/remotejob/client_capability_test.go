package remotejob

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFinalizeCapabilityUnavailableIsTypedAndPermanent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/finalize") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte(`{"error":"invalid_runtime_assets","message":"stock window preparation capability is unavailable","ok":false}`))
	}))
	defer server.Close()

	client := New(server.URL, "test-token")
	err := client.Finalize(context.Background(), "job-capability", map[string]any{"job_id": "job-capability"})
	if err == nil {
		t.Fatal("422 capability response must be an error")
	}
	if !errors.Is(err, ErrRemoteCapabilityUnavailable) {
		t.Fatalf("error = %v, want ErrRemoteCapabilityUnavailable", err)
	}
	var capability *CapabilityUnavailableError
	if !errors.As(err, &capability) {
		t.Fatalf("error = %v, want CapabilityUnavailableError", err)
	}
	if capability.StatusCode != http.StatusUnprocessableEntity || capability.Code != "invalid_runtime_assets" {
		t.Fatalf("capability error = %#v, want status 422 and the Master code", capability)
	}
	if capability.IsRetryable() {
		t.Fatal("a missing remote capability must not be retryable")
	}
}

func TestCapabilityParserOnlyClaimsThe422Contract(t *testing.T) {
	if got := parseCapabilityUnavailableError(http.StatusInternalServerError, `{"error":"invalid_runtime_assets","message":"capability is unavailable"}`); got != nil {
		t.Fatalf("500 must stay transient, got %#v", got)
	}
	if got := parseCapabilityUnavailableError(http.StatusUnprocessableEntity, `{"error":"invalid_payload","message":"something else"}`); got != nil {
		t.Fatalf("unrelated 422 must not be claimed, got %#v", got)
	}
}

func TestCheckCapabilitiesAcceptsAdvertisedStockWindowPreparation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/jobs/capabilities" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"capabilities":["video_source_certification.v1"]}`))
	}))
	defer server.Close()

	client := New(server.URL, "test-token")
	if err := client.CheckCapabilities(context.Background(), "video_source_certification.v1"); err != nil {
		t.Fatalf("CheckCapabilities() error = %v", err)
	}
}

func TestCheckCapabilitiesFailsClosedWhenRequiredCapabilityIsAbsent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"capabilities":["clip_packet_copy"]}`))
	}))
	defer server.Close()

	err := New(server.URL, "test-token").CheckCapabilities(context.Background(), "video_source_certification.v1")
	if err == nil {
		t.Fatal("CheckCapabilities() must reject an unadvertised required feature")
	}
	if !errors.Is(err, ErrRemoteCapabilityUnavailable) {
		t.Fatalf("error = %v, want ErrRemoteCapabilityUnavailable", err)
	}
}

func TestCheckCapabilitiesFailsClosedWhenWorkerInventoryIsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	err := New(server.URL, "test-token").CheckCapabilities(context.Background(), "video_source_certification.v1")
	if err == nil {
		t.Fatal("CheckCapabilities() must reject an unsupported capability endpoint")
	}
}

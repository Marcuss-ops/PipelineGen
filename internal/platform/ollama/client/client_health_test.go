package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsModelResidentReadsLivePSState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Fatalf("path = %q, want /api/ps", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:e2b"}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 2)
	resident, err := c.IsModelResident(context.Background(), "gemma4:e2b")
	if err != nil {
		t.Fatal(err)
	}
	if !resident {
		t.Fatal("expected gemma4:e2b to be resident")
	}
	resident, err = c.IsModelResident(context.Background(), "gemma4:e4b")
	if err != nil {
		t.Fatal(err)
	}
	if resident {
		t.Fatal("did not expect gemma4:e4b to be resident")
	}
}

func TestIsModelResidentAcceptsDigestQualifiedName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" {
			t.Fatalf("path = %q, want /api/ps", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"gemma4:e2b@sha256:abc"}]}`))
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 2)
	resident, err := c.IsModelResident(context.Background(), "gemma4:e2b")
	if err != nil {
		t.Fatal(err)
	}
	if !resident {
		t.Fatal("expected digest-qualified gemma4:e2b to be resident")
	}
}

func TestIsModelResidentPropagatesPSFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	c := NewClient(server.URL, "gemma4:e4b", 2)
	resident, err := c.IsModelResident(context.Background(), "gemma4:e4b")
	if err == nil || resident {
		t.Fatalf("expected live residency error, got resident=%v err=%v", resident, err)
	}
}

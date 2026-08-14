package embeddings

import (
	"context"
	coreembedding "github.com/Marcuss-ops/PipelineGen/internal/kernel/embedding"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContractProbe_Fetch_ParsesCanonicalContract(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/contract" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"contract_version":"v1","model_id":"intfloat/multilingual-e5-base","model_revision":"2026-06-26-v1","dimension":768,"normalization":"l2","distance":"Cosine","query_prefix":"query: ","document_prefix":"passage: ","semantic_document_version":"v3","contract_hash":"` + coreembedding.CanonicalText.Hash() + `"}`))
	}))
	defer s.Close()
	got, err := NewContractProbe(s.URL).Fetch(context.Background())
	if err != nil || !got.Equal(coreembedding.CanonicalText) {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestContractProbe_FailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, url string }{
		{"unconfigured", ""},
		{"404", "http://127.0.0.1:1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewContractProbe(tc.url).Fetch(context.Background())
			if err == nil {
				t.Fatal("expected fail-closed error")
			}
			if tc.name == "404" && !strings.Contains(err.Error(), "request") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

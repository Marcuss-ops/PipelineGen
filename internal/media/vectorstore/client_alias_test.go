package vectorstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// aliasHelperMock is a minimal Qdrant fake: records every request and serves
// canned responses for the alias and collection endpoints.
type aliasHelperMock struct {
	aliases map[string]string // alias -> collection
}

func (m *aliasHelperMock) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(AliasBasePath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			out := struct {
				Result struct {
					Aliases []map[string]string `json:"aliases"`
				} `json:"result"`
			}{}
			for alias, coll := range m.aliases {
				out.Result.Aliases = append(out.Result.Aliases, map[string]string{
					"alias_name":      alias,
					"collection_name": coll,
				})
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(out)
			return
		}
		// POST /aliases with actions
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Actions []map[string]any `json:"actions"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
		for _, action := range req.Actions {
			if ca, ok := action["create_alias"]; ok {
				alias, _ := ca.(map[string]any)["alias_name"].(string)
				coll, _ := ca.(map[string]any)["collection_name"].(string)
				if _, exists := m.aliases[alias]; exists {
					// 409 race condition
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"status":{"error":"Validation error: alias ` + alias + ` already exists"}}`))
					return
				}
				m.aliases[alias] = coll
			}
			if da, ok := action["delete_alias"]; ok {
				alias, _ := da.(map[string]any)["alias_name"].(string)
				delete(m.aliases, alias)
			}
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":true}`))
	})
	mux.HandleFunc("/collections/", func(w http.ResponseWriter, r *http.Request) {
		// Stub: collection presence and alias target match — return points_count
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"points_count":42,"config":{"params":{"vectors":{"text":{},"visual":{}}}}}}`))
	})
	return mux
}

func newAliasClient(t *testing.T, m *aliasHelperMock) *QdrantClient {
	t.Helper()
	srv := httptest.NewServer(m.handler())
	t.Cleanup(srv.Close)
	return NewQdrantClient(Config{
		URL:                  srv.URL,
		Collection:           "pipelinegen_clips",
		TextVectorName:       "text",
		VisualVectorName:     "visual",
		AudioVectorName:      "",
		TranscriptVectorName: "transcript",
		SparseVectorName:     "",
		TextDimensions:       768,
		VisualDimensions:     512,
		TranscriptDimensions: 768,
		TimeoutMs:            5000,
		CollectionVersion:    "v1",
	})
}

func TestAsHTTPError_StructuralExtract(t *testing.T) {
	he := newHTTPError(409, "POST", AliasBasePath, []byte(`{"status":{"error":"exists"}}`))
	// Error() should include method + path so logs are actionable.
	if !strings.Contains(he.Error(), "POST") || !strings.Contains(he.Error(), AliasBasePath) {
		t.Errorf("Error() should include method and path, got: %s", he.Error())
	}
	if !strings.Contains(he.Error(), "409") {
		t.Errorf("Error() should include status code, got: %s", he.Error())
	}

	// Wrap and verify errors.As still finds the httpError.
	wrapped := fmt.Errorf("create_alias failed: %w", he)
	got := AsHTTPError(wrapped)
	if got == nil || got.StatusCode != 409 {
		t.Fatalf("AsHTTPError should extract wrapped httpError, got: %v", got)
	}
	if got.Method != "POST" || got.Path != AliasBasePath {
		t.Errorf("wrapped httpError should preserve Method/Path, got: %+v", got)
	}

	// AsHTTPError on a plain error returns nil — neither panic nor false positives.
	if AsHTTPError(nil) != nil {
		t.Errorf("AsHTTPError(nil) must return nil")
	}
	if AsHTTPError(errors.New("plain")) != nil {
		t.Errorf("AsHTTPError should ignore unrelated error types")
	}
}

func TestEnsureAlias_CreatesAliasWhenMissing(t *testing.T) {
	mock := &aliasHelperMock{aliases: map[string]string{}}
	c := newAliasClient(t, mock)
	if err := c.EnsureCollection(context.Background()); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if got := mock.aliases["pipelinegen_clips_current"]; got != "pipelinegen_clips_v1" {
		t.Errorf("alias should point at v1, got %q", got)
	}
}

func TestEnsureAlias_409_IsTreatedAsSuccess(t *testing.T) {
	// Pre-populate the alias so create_alias will return 409 — even though the
	// collection is the same target.
	mock := &aliasHelperMock{aliases: map[string]string{
		"pipelinegen_clips_current": "pipelinegen_clips_v1",
	}}
	c := newAliasClient(t, mock)
	if err := c.EnsureCollection(context.Background()); err != nil {
		t.Fatalf("EnsureCollection on already-correct alias should not error: %v", err)
	}
}

func TestSwitchAlias_RepointsAtomically(t *testing.T) {
	mock := &aliasHelperMock{aliases: map[string]string{
		"pipelinegen_clips_current": "pipelinegen_clips_v1",
	}}
	c := newAliasClient(t, mock)
	if err := c.SwitchAlias(context.Background(), "pipelinegen_clips_v1", "pipelinegen_clips_v2"); err != nil {
		t.Fatalf("SwitchAlias: %v", err)
	}
	if got := mock.aliases["pipelinegen_clips_current"]; got != "pipelinegen_clips_v2" {
		t.Errorf("alias should now point at v2, got %q", got)
	}
	if _, exists := mock.aliases["pipelinegen_clips_v1"]; exists {
		t.Errorf("old alias target should have been atomically replaced")
	}
}

func TestSwitchAlias_DisabledCollectionVersionIsError(t *testing.T) {
	mock := &aliasHelperMock{aliases: map[string]string{}}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()
	c := NewQdrantClient(Config{
		URL: srv.URL, Collection: "x",
		CollectionVersion: "v1",
		DisableAlias:      true,
	})
	err := c.SwitchAlias(context.Background(), "x_v1", "x_v2")
	if err == nil || !strings.Contains(err.Error(), "alias pattern disabled") {
		t.Fatalf("expected alias-pattern-disabled error, got: %v", err)
	}
}

func TestDeleteAlias_RemovesAliasNotCollection(t *testing.T) {
	mock := &aliasHelperMock{aliases: map[string]string{
		"pipelinegen_clips_current": "pipelinegen_clips_v1",
	}}
	c := newAliasClient(t, mock)
	if err := c.DeleteAlias(context.Background(), "pipelinegen_clips_current"); err != nil {
		t.Fatalf("DeleteAlias: %v", err)
	}
	if _, exists := mock.aliases["pipelinegen_clips_current"]; exists {
		t.Errorf("alias should have been removed")
	}
}

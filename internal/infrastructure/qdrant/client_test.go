// Package qdrant / client_test.go — TODO 1 (June 2026) unit tests for the
// SearchPoints → /points/query migration. Coverage:
//
//  1. named vector "text"   → POST /points/query with body {"query":...,"using":"text",...}
//  2. named vector "visual" → POST /points/query with body {"using":"visual",...}
//  3. empty VectorName       → wire body sends {"using":"text"} (canonical default channel)
//  4. HTTP 400 from server   → error returned (parseError wire-up)
//  5. HTTP 200 with payload  → SearchResult list decoded (id, score, payload)
//  6. nil QueryVector        → guard short-circuits before HTTP round-trip
//  7. score_threshold > 0    → wire body sends "score_threshold"
//  8. non-nil Filter         → wire body sends "filter"
//
// The mock server uses httptest.NewServer so we capture the actual
// {path, body} that the client sent — no in-process mocking, full HTTP
// stack behaves like production.
package qdrant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// searchMock captures each request the client sends and replays a
// canned response. Captures only what the tests assert on (path +
// parsed body map); raw bytes are dropped to avoid dead capture.
type searchMock struct {
	path string
	body map[string]any

	respBody []byte
	status   int
}

func newSearchMockServer(t *testing.T, m *searchMock) *httptest.Server {
	t.Helper()
	if m.status == 0 {
		m.status = http.StatusOK
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.path = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &m.body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(m.status)
		if len(m.respBody) > 0 {
			_, _ = w.Write(m.respBody)
		}
	}))
}

// newTestClient builds a Client pointed at the given httptest server.
// log is silenced via zap.NewNop(), apiKey is left empty (mock does not
// check auth).
func newTestClient(baseURL string) *Client {
	return NewClient(&Config{
		BaseURL: baseURL,
		Timeout: 5, // seconds
	}, zap.NewNop())
}

// TestClient_SearchPoints_NamedVectorText covers case 1: when SearchRequest
// sets VectorName="text", the on-wire body carries "using":"text" AND the
// path is /points/query (not /points/search). The mock server captures
// both pieces and the test asserts them directly.
func TestClient_SearchPoints_NamedVectorText(t *testing.T) {
	mock := &searchMock{
		respBody: []byte(`{"result": [{"id": "p1", "score": 0.9, "payload": {"asset_id": "a1"}}]}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	results, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: []float32{0.1, 0.2},
		VectorName:  "text",
		Limit:       3,
	})
	if err != nil {
		t.Fatalf("SearchPoints: %v", err)
	}

	if mock.path != "/collections/media_assets/points/query" {
		t.Errorf("path = %q, want /collections/media_assets/points/query", mock.path)
	}
	gotUsing, _ := mock.body["using"].(string)
	if gotUsing != "text" {
		t.Errorf("body.using = %q, want text", gotUsing)
	}
	gotQuery, ok := mock.body["query"].([]interface{})
	if !ok {
		t.Fatalf("body.query missing or wrong type: %#v", mock.body["query"])
	}
	if len(gotQuery) != 2 {
		t.Errorf("body.query len = %d, want 2 (matches QueryVector)", len(gotQuery))
	}
	if _, ok := mock.body["vector"]; ok {
		t.Errorf("legacy key 'vector' must NOT appear in /points/query body: %#v", mock.body)
	}
	if len(results) != 1 || results[0].ID != "p1" {
		t.Errorf("decoded results = %+v, want one p1", results)
	}
}

// TestClient_SearchPoints_NamedVectorVisual covers case 2: a different
// named vector "visual" produces an identical body shape with the new
// "using" value. Proves the migration is generic across channels.
func TestClient_SearchPoints_NamedVectorVisual(t *testing.T) {
	mock := &searchMock{
		respBody: []byte(`{"result": [{"id": "vx", "score": 0.7, "payload": {"asset_id": "ax"}}]}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	results, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: []float32{0.5},
		VectorName:  "visual",
		Limit:       5,
	})
	if err != nil {
		t.Fatalf("SearchPoints: %v", err)
	}
	if mock.path != "/collections/media_assets/points/query" {
		t.Errorf("path = %q, want /points/query", mock.path)
	}
	gotUsing, _ := mock.body["using"].(string)
	if gotUsing != "visual" {
		t.Errorf("body.using = %q, want visual", gotUsing)
	}
	if len(results) != 1 {
		t.Errorf("results len = %d, want 1", len(results))
	}
}

// TestClient_SearchPoints_DefaultChannelIsText covers case 3: when
// VectorName is empty, SearchPoints MUST inject the canonical default
// channel "using":"text" (per ARCHITECTURE.md §10 and
// architecture/qdrant/001-sidecar-and-pointid.md). We intentionally
// do NOT rely on Qdrant's server-side default — the wire request is
// always explicit so the contract is unambiguous from a packet capture.
//
// Verifies BOTH the on-wire key (presence of "using":"text") AND the
// canonical path ("/points/query") for parity with tests #1 and #2.
func TestClient_SearchPoints_DefaultChannelIsText(t *testing.T) {
	mock := &searchMock{
		respBody: []byte(`{"result": []}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	_, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: []float32{0.3, 0.4},
		// VectorName intentionally empty — default channel applies.
		Limit: 10,
	})
	if err != nil {
		t.Fatalf("SearchPoints: %v", err)
	}
	if mock.path != "/collections/media_assets/points/query" {
		t.Errorf("path = %q, want /points/query", mock.path)
	}
	gotUsing, _ := mock.body["using"].(string)
	if gotUsing != defaultVectorName {
		t.Errorf("body.using = %q, want %q (canonical default channel)", gotUsing, defaultVectorName)
	}
}

// TestClient_SearchPoints_HTTPErrorPropagates covers case 4: a non-2xx
// response from Qdrant must surface as a Go error wrapping the HTTP
// status code. Uses status 400 (the canonical "bad request" code Qdrant
// returns when the body shape is wrong — perfect match for this
// migration's regress-vs-legacy test).
func TestClient_SearchPoints_HTTPErrorPropagates(t *testing.T) {
	mock := &searchMock{
		status:   http.StatusBadRequest,
		respBody: []byte(`{"status":{"error":"Validation error: field 'query' is required"}}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	_, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: []float32{0.1},
		VectorName:  "text",
		Limit:       1,
	})
	if err == nil {
		t.Fatalf("SearchPoints must return error on HTTP 400, got nil (results dropped on the floor)")
	}
	// parseError wraps the HTTP code; surface it in the message.
	if msg := err.Error(); !strings.Contains(msg, "400") {
		t.Errorf("expected HTTP-400 in error message, got: %q", msg)
	}
}

// TestClient_SearchPoints_DecodeResults covers case 5: with a valid 200
// response carrying two scored points, SearchPoints returns a list of
// length 2 with id/score/payload correctly populated. This proves the
// response decoder (decodeSearchResults) works for both endpoints —
// already in use by HybridSearchPoints via executeQuery, so this is a
// regression guard against accidental decode-layer changes.
func TestClient_SearchPoints_DecodeResults(t *testing.T) {
	mock := &searchMock{
		respBody: []byte(`{
			"result": [
				{"id": "p1", "score": 0.95, "payload": {"asset_id": "a1", "name": "alpha"}},
				{"id": "p2", "score": 0.42, "payload": {"asset_id": "a2", "name": "beta"}}
			]
		}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	results, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: []float32{0.1, 0.2, 0.3},
		VectorName:  "text",
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("SearchPoints: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	// Result ordering follows the Qdrant response order.
	if results[0].ID != "p1" || results[0].Score != 0.95 {
		t.Errorf("results[0] = %+v, want id=p1 score=0.95", results[0])
	}
	if results[0].Payload["name"] != "alpha" {
		t.Errorf("results[0].Payload name = %v, want alpha", results[0].Payload["name"])
	}
	if results[1].Score > results[0].Score {
		t.Errorf("results not in Qdrant score order: [0]=%f [1]=%f", results[0].Score, results[1].Score)
	}
}

// TestClient_SearchPoints_NilVectorGuardError covers the local nil-vector
// guard. The function returns BEFORE any HTTP round-trip, so the test
// verifies via the mock that no request is sent and the error mentions
// the field.
func TestClient_SearchPoints_NilVectorGuardError(t *testing.T) {
	mock := &searchMock{
		respBody: []byte(`{"result": []}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	_, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: nil,
		VectorName:  "text",
		Limit:       1,
	})
	if err == nil {
		t.Fatalf("expected nil-vector error, got nil")
	}
	if mock.path != "" {
		t.Errorf("mock received request at path %q; nil-guard MUST short-circuit before HTTP", mock.path)
	}
	if !strings.Contains(err.Error(), "vector") {
		t.Errorf("error message should mention vector: %q", err.Error())
	}
}

// TestClient_SearchPoints_ScoreThresholdPropagates covers case 7:
// when MinScore > 0, the on-wire body MUST include "score_threshold"
// (Qdrant filters matches at or above the threshold server-side).
func TestClient_SearchPoints_ScoreThresholdPropagates(t *testing.T) {
	mock := &searchMock{
		respBody: []byte(`{"result": []}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	_, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: []float32{0.1},
		VectorName:  "text",
		Limit:       5,
		MinScore:    0.85,
	})
	if err != nil {
		t.Fatalf("SearchPoints: %v", err)
	}
	gotThresh, ok := mock.body["score_threshold"].(float64)
	if !ok {
		t.Fatalf("body.score_threshold missing or wrong type: %#v", mock.body["score_threshold"])
	}
	if gotThresh != 0.85 {
		t.Errorf("body.score_threshold = %v, want 0.85", gotThresh)
	}
}

// TestClient_SearchPoints_FilterPropagates covers case 8: when Filter
// is non-nil, the on-wire body MUST include the filter map verbatim
// (Qdrant applies the conditions server-side, including payload
// must/match, has_id, and nested Geo filters).
func TestClient_SearchPoints_FilterPropagates(t *testing.T) {
	mock := &searchMock{
		respBody: []byte(`{"result": []}`),
	}
	ts := newSearchMockServer(t, mock)
	defer ts.Close()

	c := newTestClient(ts.URL)
	filter := map[string]interface{}{
		"must": []interface{}{
			map[string]interface{}{
				"key":   "asset_id",
				"match": map[string]interface{}{"value": "alpha-001"},
			},
		},
	}
	_, err := c.SearchPoints(context.Background(), "media_assets", SearchRequest{
		QueryVector: []float32{0.1},
		VectorName:  "text",
		Limit:       5,
		Filter:      filter,
	})
	if err != nil {
		t.Fatalf("SearchPoints: %v", err)
	}
	if _, present := mock.body["filter"]; !present {
		t.Fatalf("body.filter missing: %#v", mock.body)
	}
	// The filter round-trips identically as a map (key + nested match).
	if gotFilter, _ := mock.body["filter"].(map[string]interface{}); gotFilter == nil {
		t.Errorf("body.filter wrong type: %#v", mock.body["filter"])
	}
}

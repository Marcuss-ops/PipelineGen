package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// ── Sentinel reachability ────────────────────────────────────────────

func TestSentinels_ErrorsIsReachable(t *testing.T) {
	t.Parallel()
	sentinels := []struct {
		name string
		err  error
	}{
		{"NotWired", ErrAssetPersistenceNotWired},
		{"MissingAssetID", ErrAssetPersistenceMissingAssetID},
		{"MissingSource", ErrAssetPersistenceMissingSource},
		{"MissingContentHash", ErrAssetPersistenceMissingContentHash},
		{"NilTx", ErrAssetPersistenceNilTx},
	}
	for _, s := range sentinels {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			if !errors.Is(s.err, s.err) {
				t.Fatalf("sentinel %q does not match itself via errors.Is", s.name)
			}
			// A non-%%w wrap should NOT recover the sentinel.
			plainWrap := errors.New("wrapper: " + s.err.Error())
			if errors.Is(plainWrap, s.err) {
				t.Fatalf("sentinel %q should not match a non-%%w wrap", s.name)
			}
			// A %%w wrap MUST recover the sentinel.
			wrapped := fmt.Errorf("deep: %w", s.err)
			if !errors.Is(wrapped, s.err) {
				t.Fatalf("sentinel %q should be recoverable via %%w wrap", s.name)
			}
		})
	}
}

// ── Sentinel distinctness ────────────────────────────────────────────

func TestSentinels_AllDistinct(t *testing.T) {
	t.Parallel()
	sentinels := []error{
		ErrAssetPersistenceNotWired,
		ErrAssetPersistenceMissingAssetID,
		ErrAssetPersistenceMissingSource,
		ErrAssetPersistenceMissingContentHash,
		ErrAssetPersistenceNilTx,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel[%d] matches sentinel[%d] via errors.Is — they should be distinct", i, j)
			}
		}
	}
}

// ── Sentinel messages ────────────────────────────────────────────────

func TestSentinelMessages_ContainKeywords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		sentinel error
		keyword  string
	}{
		{ErrAssetPersistenceNotWired, "not wired"},
		{ErrAssetPersistenceMissingAssetID, "AssetID"},
		{ErrAssetPersistenceMissingSource, "Source"},
		{ErrAssetPersistenceMissingContentHash, "ContentHash"},
		{ErrAssetPersistenceNilTx, "Transaction"},
	}
	for _, tt := range tests {
		t.Run(tt.keyword, func(t *testing.T) {
			t.Parallel()
			if !strings.Contains(tt.sentinel.Error(), tt.keyword) {
				t.Errorf("sentinel message %q does not contain keyword %q", tt.sentinel.Error(), tt.keyword)
			}
		})
	}
}

// ── PersistAndIndexRequest zero-value ────────────────────────────────

func TestPersistAndIndexRequest_ZeroValue_IsStructurallyValid(t *testing.T) {
	t.Parallel()
	var req PersistAndIndexRequest
	if req.AssetID != "" {
		t.Error("zero-value AssetID should be empty")
	}
	if req.Source != "" {
		t.Error("zero-value Source should be empty")
	}
	if req.ContentHash != "" {
		t.Error("zero-value ContentHash should be empty")
	}
	if req.Extra != nil {
		t.Error("zero-value Extra should be nil")
	}
	if req.MetadataJSON != nil {
		t.Error("zero-value MetadataJSON should be nil")
	}
}

// ── PersistAndIndexRequest field coverage ────────────────────────────

func TestPersistAndIndexRequest_AllFieldsPopulated(t *testing.T) {
	t.Parallel()
	req := PersistAndIndexRequest{
		AssetID:        "yt_abc_10_60_v1",
		Source:         "youtube",
		Name:           "Pacquiao vs Broner Round 7",
		Filename:       "round-7.mp4",
		MediaType:      "video",
		ContentHash:    "abc123def456",
		Description:    "Broner insults Pacquiao",
		DriveFileID:    "1abc123",
		DriveLink:      "https://drive.google.com/file/d/1abc123",
		DownloadLink:   "https://drive.google.com/uc?id=1abc123",
		LocalPath:      "/tmp/yt_abc_10_60_v1.mp4",
		FolderID:       "folder123",
		FolderPath:     "YouTube/Boxing",
		LifecycleState: "ACTIVE",
		IndexState:     "INDEXING_PENDING",
		SearchText:     "Pacquiao vs Broner boxing round 7",
		MetadataJSON:   []byte(`{"title":"Round 7","round":7}`),
		Extra:          map[string]any{"tags": []string{"boxing", "pacquiao"}},
	}
	if req.AssetID != "yt_abc_10_60_v1" {
		t.Errorf("AssetID = %q, want %q", req.AssetID, "yt_abc_10_60_v1")
	}
	if req.Source != "youtube" {
		t.Errorf("Source = %q, want %q", req.Source, "youtube")
	}
	if req.MediaType != "video" {
		t.Errorf("MediaType = %q, want %q", req.MediaType, "video")
	}
	if req.LifecycleState != "ACTIVE" {
		t.Errorf("LifecycleState = %q, want %q", req.LifecycleState, "ACTIVE")
	}
	if req.IndexState != "INDEXING_PENDING" {
		t.Errorf("IndexState = %q, want %q", req.IndexState, "INDEXING_PENDING")
	}
	// Verify MetadataJSON is valid JSON.
	var parsed map[string]any
	if err := json.Unmarshal(req.MetadataJSON, &parsed); err != nil {
		t.Fatalf("MetadataJSON is not valid JSON: %v", err)
	}
	if parsed["title"] != "Round 7" {
		t.Errorf("MetadataJSON.title = %v, want %q", parsed["title"], "Round 7")
	}
	if parsed["round"] != float64(7) {
		t.Errorf("MetadataJSON.round = %v, want %v", parsed["round"], float64(7))
	}
}

// ── PersistAndIndexResult zero-value ─────────────────────────────────

func TestPersistAndIndexResult_ZeroValue_IsStructurallyValid(t *testing.T) {
	t.Parallel()
	var res PersistAndIndexResult
	if res.EventKey != "" {
		t.Error("zero-value EventKey should be empty")
	}
	if res.PayloadJSON != nil {
		t.Error("zero-value PayloadJSON should be nil")
	}
	if res.RowsAffected != 0 {
		t.Error("zero-value RowsAffected should be 0")
	}
}

// ── PersistAndIndexResult field coverage ─────────────────────────────

func TestPersistAndIndexResult_AllFieldsPopulated(t *testing.T) {
	t.Parallel()
	payload, _ := json.Marshal(map[string]any{
		"schema_version": "asset.index.requested.v1",
		"asset_id":       "yt_abc_10_60_v1",
	})
	res := PersistAndIndexResult{
		EventKey:     "index:yt_abc_10_60_v1:abc123:multilingual-e5-base:v2:media_assets_current",
		PayloadJSON:  payload,
		RowsAffected: 1,
	}
	if res.EventKey == "" {
		t.Error("EventKey should not be empty")
	}
	if len(res.PayloadJSON) == 0 {
		t.Error("PayloadJSON should not be empty")
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", res.RowsAffected)
	}
}

// ── Transaction interface structural compliance ──────────────────────

// mockTx is a minimal implementation of Transaction for interface
// compliance testing. It panics on use — we only verify the interface
// is satisfied at compile time.
type mockTx struct{}

func (mockTx) ExecContext(_ context.Context, _ string, _ ...any) (Result, error) {
	return nil, nil
}
func (mockTx) QueryRowContext(_ context.Context, _ string, _ ...any) Row {
	return nil
}

// Compile-time assertion.
var _ Transaction = mockTx{}

// ── AssetPersistenceWriter interface compile-time pin ────────────────

// mockWriter is a minimal implementation of AssetPersistenceWriter for
// compile-time interface compliance. godlike/06 Pattern 0: if the
// interface signature drifts, this var-declaration fails the build.
type mockWriter struct{}

func (mockWriter) PersistAndIndex(_ context.Context, _ Transaction, _ PersistAndIndexRequest) (PersistAndIndexResult, error) {
	return PersistAndIndexResult{}, nil
}

// Compile-time assertion: mockWriter MUST satisfy AssetPersistenceWriter.
var _ AssetPersistenceWriter = mockWriter{}

func TestTransactionInterface_CompileTimePin(t *testing.T) {
	t.Parallel()
	// If this compiles, the interface is satisfied.
	var _ Transaction = mockTx{}
}

// ── Result/Row interface structural compliance ───────────────────────

type mockResult struct{}

func (mockResult) LastInsertId() (int64, error) { return 0, nil }
func (mockResult) RowsAffected() (int64, error) { return 0, nil }

var _ Result = mockResult{}

type mockRow struct{}

func (mockRow) Scan(_ ...any) error { return nil }

var _ Row = mockRow{}

func TestResultInterface_CompileTimePin(t *testing.T) {
	t.Parallel()
	var _ Result = mockResult{}
}

func TestRowInterface_CompileTimePin(t *testing.T) {
	t.Parallel()
	var _ Row = mockRow{}
}

// ── PersistAndIndexRequest YouTube-shaped literal ────────────────────

func TestPersistAndIndexRequest_YouTubeShape(t *testing.T) {
	t.Parallel()
	req := PersistAndIndexRequest{
		AssetID:        "yt_vdC5GXxS-qU_146_155_v1",
		Source:         "youtube",
		Name:           "Sfuriata contro Pacquiao",
		Filename:       "sfuriata-contro-pacquiao.mp4",
		MediaType:      "video",
		ContentHash:    "sha256-of-search-text",
		DriveFileID:    "1abc",
		DriveLink:      "https://drive.google.com/file/d/1abc",
		LocalPath:      "/tmp/yt_vdC5GXxS-qU_146_155_v1.mp4",
		FolderID:       "boxing-folder",
		FolderPath:     "YouTube/Boxing",
		LifecycleState: "ACTIVE",
		SearchText:     "Sfuriata contro Pacquiao boxing confrontation prefight",
		EventCreatedAt: time.Date(2026, 7, 8, 10, 0, 0, 0, time.UTC),
	}
	if req.Source != "youtube" {
		t.Errorf("Source = %q, want youtube", req.Source)
	}
	if req.LifecycleState != "ACTIVE" {
		t.Errorf("LifecycleState = %q, want ACTIVE", req.LifecycleState)
	}
	if req.IndexState != "" {
		t.Error("IndexState should be empty for YouTube")
	}
	if req.EventCreatedAt.IsZero() {
		t.Error("EventCreatedAt should be non-zero")
	}
}

// ── PersistAndIndexRequest Stock-shaped literal ──────────────────────

func TestPersistAndIndexRequest_EventCreatedAtZero(t *testing.T) {
	t.Parallel()
	req := PersistAndIndexRequest{
		AssetID:     "planner:abc:0",
		Source:      "stock",
		ContentHash: "hash",
	}
	if !req.EventCreatedAt.IsZero() {
		t.Error("zero-value EventCreatedAt should report IsZero() == true")
	}
}

func TestPersistAndIndexRequest_StockShape(t *testing.T) {
	t.Parallel()
	metadata, _ := json.Marshal(map[string]any{
		"title":           "Round 7",
		"description":     "Broner insults Pacquiao, then Pacquiao responds",
		"round":           7,
		"source_provider": "pexels",
		"content_hash":    "sha256-of-video-bytes",
	})
	req := PersistAndIndexRequest{
		AssetID:        "planner:abc123def456:0",
		Source:         "stock",
		Name:           "round-7.mp4",
		Filename:       "round-7.mp4",
		MediaType:      "video",
		ContentHash:    "sha256-of-video-bytes",
		Description:    "Broner insults Pacquiao, then Pacquiao responds",
		DriveFileID:    "1xyz",
		DriveLink:      "https://drive.google.com/file/d/1xyz",
		FolderID:       "timestamp-folder",
		FolderPath:     "Stock/Boxing/Timestamp_0",
		LifecycleState: "PUBLISHED",
		IndexState:     "INDEXING_PENDING",
		MetadataJSON:   metadata,
		Extra: map[string]any{
			"tags":    []string{"boxing", "pacquiao", "broner"},
			"round":   7,
			"event":   "Pacquiao vs Broner press conference",
			"subject": "Boxing confrontation",
		},
	}
	if req.Source != "stock" {
		t.Errorf("Source = %q, want stock", req.Source)
	}
	if req.LifecycleState != "PUBLISHED" {
		t.Errorf("LifecycleState = %q, want PUBLISHED", req.LifecycleState)
	}
	if req.IndexState != "INDEXING_PENDING" {
		t.Errorf("IndexState = %q, want INDEXING_PENDING", req.IndexState)
	}
	if req.Description == "" {
		t.Error("Description should be populated for Stock")
	}
	if len(req.Extra) != 4 {
		t.Errorf("Extra has %d keys, want 4", len(req.Extra))
	}
	// Verify MetadataJSON parses correctly.
	var parsed map[string]any
	if err := json.Unmarshal(req.MetadataJSON, &parsed); err != nil {
		t.Fatalf("MetadataJSON is not valid JSON: %v", err)
	}
	if parsed["round"] != float64(7) {
		t.Errorf("MetadataJSON.round = %v, want 7", parsed["round"])
	}
}

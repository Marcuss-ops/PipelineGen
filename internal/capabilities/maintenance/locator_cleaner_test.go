// Package qdrant — locator_cleaner_test.go (P4 PREALLOC-CLEANER, July 2026).
//
// Tests for LocatorCleaner.CleanLocators pre-allocation: verifies that
// the affectedIDs slice capacity is at least as large as the actual
// number of affected points after the full scroll completes.
package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/transport"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// ── Pre-allocation capacity test ─────────────────────────────────────

func TestCleanLocators_PreAllocCapacityGreaterOrEqualToAffected(t *testing.T) {
	t.Parallel()

	// 600 points total, ~30% affected (every 3rd point has drive_link).
	// Distribution: points 0,3,6,9,... are affected → ~200 points, spread
	// across all 3 pages (250+250+100).
	const totalPoints = 600
	const pageSize = 250

	// Build a deterministic set of affected indices: every 3rd point.
	affectedSet := make(map[int]bool)
	for i := 0; i < totalPoints; i += 3 {
		affectedSet[i] = true
	}
	affectedCount := len(affectedSet)

	addPoint := func(idx int) map[string]interface{} {
		id := fmt.Sprintf("point-%04d", idx)
		pt := map[string]interface{}{
			"id":      id,
			"payload": map[string]interface{}{"asset_id": id, "name": "n", "source": "youtube"},
		}
		if affectedSet[idx] {
			pt["payload"].(map[string]interface{})["drive_link"] = "https://drive.google.com/old-link"
		}
		return pt
	}

	// Pre-generate all pages so the mock can serve them sequentially.
	type pageData struct {
		points []map[string]interface{}
		next   string
	}
	var allPages []pageData
	var buf []map[string]interface{}
	pages := 0
	for i := 0; i < totalPoints; i++ {
		buf = append(buf, addPoint(i))
		if len(buf) >= pageSize || i == totalPoints-1 {
			copied := make([]map[string]interface{}, len(buf))
			copy(copied, buf)
			pages++
			next := ""
			if pages*pageSize < totalPoints {
				next = fmt.Sprintf("offset-%d", pages)
			}
			allPages = append(allPages, pageData{points: copied, next: next})
			buf = buf[:0]
		}
	}

	pageIdx := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points_count": totalPoints,
					"status":       "green",
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_v3/points/scroll":
			if pageIdx >= len(allPages) {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"result": map[string]interface{}{"points": []interface{}{}, "next_page_offset": nil},
				})
				return
			}
			pg := allPages[pageIdx]
			pageIdx++
			var points []interface{}
			for _, p := range pg.points {
				points = append(points, p)
			}
			var nextOffset interface{}
			if pg.next != "" {
				nextOffset = pg.next
			} else {
				nextOffset = nil
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points":           points,
					"next_page_offset": nextOffset,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"aliases": []map[string]interface{}{
						{"alias_name": "media_assets_current", "collection_name": "media_assets_v3"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	schema := &qdrantSchema.IndexSchema{
		Version:      "v3",
		PhysicalName: "media_assets_v3",
		RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{
			{Channel: "text", Dimensions: 768, Distance: "Cosine"},
		},
	}
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cleaner := NewLocatorCleaner(client, schema, zap.NewNop())

	report, err := cleaner.CleanLocators(context.Background(), false)
	require.NoError(t, err)
	require.NotNil(t, report)

	assert.Equal(t, totalPoints, report.TotalPointsScrolled)
	assert.Equal(t, affectedCount, report.PointsAffected,
		"~33%% of %d points should have drive_link (every 3rd)", totalPoints)

	// P4 capacity assertion: the pre-allocated capacity must be >= the
	// actual number of affected points (no overallocation panic, no
	// truncation). With CountPoints=600, make([]string, 0, 600) ensures
	// capacity is always sufficient.
	assert.GreaterOrEqual(t, report.AllocCapacity, report.PointsAffected,
		"pre-allocated capacity (%d) must be >= affected count (%d)",
		report.AllocCapacity, report.PointsAffected)
	assert.GreaterOrEqual(t, report.AllocCapacity, 0,
		"capacity should be non-negative")
}

// ── No legacy keys: zero affected, dry-run ───────────────────────────

func TestCleanLocators_NoLegacyKeys_DryRun(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{"points_count": 3, "status": "green"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_v3/points/scroll":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points": []interface{}{
						map[string]interface{}{"id": "pt-1", "payload": map[string]interface{}{"asset_id": "a1", "name": "n1", "source": "youtube"}},
						map[string]interface{}{"id": "pt-2", "payload": map[string]interface{}{"asset_id": "a2", "name": "n2", "source": "artlist"}},
						map[string]interface{}{"id": "pt-3", "payload": map[string]interface{}{"asset_id": "a3", "name": "n3", "source": "stock"}},
					},
					"next_page_offset": nil,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"aliases": []map[string]interface{}{
						{"alias_name": "media_assets_current", "collection_name": "media_assets_v3"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	schema := &qdrantSchema.IndexSchema{
		Version: "v3", PhysicalName: "media_assets_v3", RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{{Channel: "text", Dimensions: 768, Distance: "Cosine"}},
	}
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cleaner := NewLocatorCleaner(client, schema, zap.NewNop())

	report, err := cleaner.CleanLocators(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, 3, report.TotalPointsScrolled)
	assert.Equal(t, 0, report.PointsAffected)
	assert.Equal(t, 0, report.PointsWithDriveLink)
	assert.Equal(t, 0, report.PointsWithLocalPath)
	assert.GreaterOrEqual(t, report.AllocCapacity, 0, "capacity should be non-negative even when zero affected")
}

// ── CountPoints error: graceful fallback, no pre-alloc ───────────────

func TestCleanLocators_CountPointsError_Fallback(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3":
			http.Error(w, "qdrant overloaded", http.StatusServiceUnavailable)
		case r.Method == http.MethodPost && r.URL.Path == "/collections/media_assets_v3/points/scroll":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"points": []interface{}{
						map[string]interface{}{"id": "pt-1", "payload": map[string]interface{}{
							"asset_id": "a1", "name": "n1", "source": "youtube", "drive_link": "https://old",
						}},
					},
					"next_page_offset": nil,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/aliases":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"result": map[string]interface{}{
					"aliases": []map[string]interface{}{
						{"alias_name": "media_assets_current", "collection_name": "media_assets_v3"},
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	schema := &qdrantSchema.IndexSchema{
		Version: "v3", PhysicalName: "media_assets_v3", RuntimeAlias: "media_assets_current",
		DenseVectors: []qdrantSchema.EmbeddingSpec{{Channel: "text", Dimensions: 768, Distance: "Cosine"}},
	}
	client := transport.NewClient(&qdrantSchema.Config{BaseURL: srv.URL, Timeout: 5}, zap.NewNop())
	cleaner := NewLocatorCleaner(client, schema, zap.NewNop())

	report, err := cleaner.CleanLocators(context.Background(), false)
	require.NoError(t, err)
	assert.Equal(t, 1, report.PointsAffected)
	assert.Equal(t, 1, report.TotalPointsScrolled)
	// Fallback path (CountPoints error → nil slice → Go append growth).
	// Capacity from default growth must still be >= affected count.
	assert.GreaterOrEqual(t, report.AllocCapacity, report.PointsAffected,
		"even without pre-allocation, capacity must be >= affected count")
}

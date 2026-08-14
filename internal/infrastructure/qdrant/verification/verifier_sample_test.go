// Package qdrant — P1 QDRANT-VERIFIER-SPLIT: sample phase tests (July 2026).
package verification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	qdrantSchema "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/qdrant/schema"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestVerifySample_HappyPath verifies that verifySample scrolls all
// points, reports CompleteScan, and returns scrollAborted=false.
func TestVerifySample_HappyPath(t *testing.T) {
	t.Parallel()

	srv := mockQdrantForVerifier(t, []string{canonicalPointPayload("asset-1")})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	assetStore := &stubAssetStore{ids: []string{"asset-1"}}
	v := NewReindexVerifier(newClientAt(srv.URL), assetStore, nil, schema, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	report.ActualPoints = 1 // pre-set by verifyCounts
	sqliteSet := map[string]bool{"asset-1": true}

	scrollAborted := v.verifySample(context.Background(), "media_assets_v3", sqliteSet, report)
	assert.False(t, scrollAborted, "happy path must not abort scroll")
	assert.True(t, report.CompleteScan, "CompleteScan must be true on clean exit")
	assert.Equal(t, 1, report.TotalScrolled)
	assert.Equal(t, 0, report.MissingCount, "no missing — asset-1 is in both sets")
	assert.Equal(t, 0, report.OrphanCount, "no orphan — asset-1 is in both sets")
	assert.Equal(t, 0, report.PayloadIssues)
	assert.Equal(t, 0, report.NonCanonicalPointCount)
}

// TestVerifySample_MissingOrphan verifies that missing and orphan
// IDs are correctly detected when SQLite and Qdrant diverge.
func TestVerifySample_MissingOrphan(t *testing.T) {
	t.Parallel()

	srv := mockQdrantForVerifier(t, []string{canonicalPointPayload("asset-1")})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	v := NewReindexVerifier(newClientAt(srv.URL), nil, nil, schema, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	report.ActualPoints = 1
	// SQLite has "asset-1" AND "asset-2" (only asset-1 in Qdrant).
	// SQLite does NOT have "asset-orphan" (only in Qdrant from mock).
	sqliteSet := map[string]bool{"asset-1": true, "asset-2": true}

	scrollAborted := v.verifySample(context.Background(), "media_assets_v3", sqliteSet, report)
	assert.False(t, scrollAborted)
	assert.Equal(t, 1, report.MissingCount, "asset-2 in SQLite but not Qdrant")
	assert.Contains(t, report.MissingIDs, "asset-2")
	// No orphan since sqliteSet doesn't list any fake IDs beyond asset-1.
	assert.Equal(t, 0, report.OrphanCount)
}

// TestVerifySample_ScrollAborted verifies that a scroll error is
// reported as scrollAborted=true and CompleteScan=false.
// Uses a custom httptest server that returns 500 on every scroll call.
func TestVerifySample_ScrollAborted(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/collections/media_assets_v3" {
			// CountPoints returns 1.
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"result":{"points_count":1,"status":"green"}}`))
			return
		}
		http.Error(w, "qdrant scroll down", http.StatusInternalServerError)
	}))
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	v := NewReindexVerifier(newClientAt(srv.URL), nil, nil, schema, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	// Pre-populate ActualPoints as verifyCounts would.
	report.ActualPoints = 1
	sqliteSet := map[string]bool{"asset-1": true}

	scrollAborted := v.verifySample(context.Background(), "media_assets_v3", sqliteSet, report)
	assert.True(t, scrollAborted, "scroll error must abort")
	assert.False(t, report.CompleteScan, "CompleteScan must be false on abort")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "PR 12 scroll page") && strings.Contains(e, "fatal") {
			found = true
			break
		}
	}
	assert.True(t, found, "Errors must contain PR 12 fatal scroll marker")
}

// TestVerifySample_NonCanonicalPointID verifies that points with
// non-canonical pt.ID are detected and reported.
func TestVerifySample_NonCanonicalPointID(t *testing.T) {
	t.Parallel()

	nonCanonical := "00000000-0000-0000-0000-000000000001"
	if nonCanonical == qdrantSchema.AssetIDToQdrantPointID("asset-1") {
		t.Skip("non-canonical UUID collides with canonical — test precondition failed")
	}
	payload := buildPointPayload(nonCanonical, "asset-1")
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	v := NewReindexVerifier(newClientAt(srv.URL), nil, nil, schema, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	report.ActualPoints = 1
	sqliteSet := map[string]bool{"asset-1": true}

	scrollAborted := v.verifySample(context.Background(), "media_assets_v3", sqliteSet, report)
	assert.False(t, scrollAborted)
	assert.Equal(t, 1, report.NonCanonicalPointCount)
	assert.Contains(t, report.NonCanonicalPointIDs, nonCanonical)
}

// TestVerifySample_PayloadMinimumValidation verifies that missing
// required payload fields are reported as PayloadIssues.
func TestVerifySample_PayloadMinimumValidation(t *testing.T) {
	t.Parallel()

	canonicalID := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	// qdrantSchema.Point missing "source" field.
	payload := `{"id": "` + canonicalID + `", "payload": {"asset_id": "asset-1", "name": "n"}}`
	srv := mockQdrantForVerifier(t, []string{payload})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	v := NewReindexVerifier(newClientAt(srv.URL), nil, nil, schema, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	report.ActualPoints = 1
	sqliteSet := map[string]bool{"asset-1": true}

	scrollAborted := v.verifySample(context.Background(), "media_assets_v3", sqliteSet, report)
	assert.False(t, scrollAborted)
	assert.Equal(t, 1, report.PayloadIssues, "missing 'source' must be a payload issue")
	found := false
	for _, e := range report.Errors {
		if strings.Contains(e, "source") {
			found = true
			break
		}
	}
	assert.True(t, found, "Errors must mention missing 'source' field")
}

// TestVerifySample_DuplicatePoint verifies that the same canonical
// asset_id appearing in more than one point bumps DuplicateQdrantPoints
// (PR-HASH-SEMANTICS item 14: 1 asset = 1 point).
func TestVerifySample_DuplicatePoint(t *testing.T) {
	t.Parallel()

	canonicalID := qdrantSchema.AssetIDToQdrantPointID("asset-1")
	dupID := "00000000-0000-0000-0000-000000000002"
	payloadA := buildPointPayload(canonicalID, "asset-1")
	payloadB := buildPointPayload(dupID, "asset-1")
	srv := mockQdrantForVerifierWithHooks(t, mockQdrantHooks{
		PagePayloads:    []string{payloadA, payloadB},
		PageNextOffsets: []string{"offset-1", ""},
	})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	v := NewReindexVerifier(newClientAt(srv.URL), nil, nil, schema, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	report.ActualPoints = 1
	sqliteSet := map[string]bool{"asset-1": true}

	scrollAborted := v.verifySample(context.Background(), "media_assets_v3", sqliteSet, report)
	assert.False(t, scrollAborted)
	assert.Equal(t, 1, report.DuplicateQdrantPoints, "same asset_id in 2 points = 1 duplicate")
	assert.Contains(t, report.DuplicatePointIDs, dupID)
}

// TestVerifySample_NoDuplicatePoint verifies a clean collection reports
// zero duplicate points.
func TestVerifySample_NoDuplicatePoint(t *testing.T) {
	t.Parallel()

	srv := mockQdrantForVerifier(t, []string{canonicalPointPayload("asset-1")})
	defer srv.Close()
	schema := qdrantSchema.DefaultV3Schema()
	v := NewReindexVerifier(newClientAt(srv.URL), nil, nil, schema, nil, zap.NewNop())

	report := newSwitchReport("media_assets_v3", 1)
	report.ActualPoints = 1
	sqliteSet := map[string]bool{"asset-1": true}

	scrollAborted := v.verifySample(context.Background(), "media_assets_v3", sqliteSet, report)
	assert.False(t, scrollAborted)
	assert.Equal(t, 0, report.DuplicateQdrantPoints)
	assert.Empty(t, report.DuplicatePointIDs)
}

// TestComputeMissingOrphan_Independent verifies computeMissingOrphan
// as a standalone free function with non-overlapping ID sets.
func TestComputeMissingOrphan_Independent(t *testing.T) {
	t.Parallel()

	sqliteSet := map[string]bool{"a": true, "b": true, "c": true}
	qdrantIDs := map[string]bool{"b": true, "c": true, "d": true}
	report := newSwitchReport("test", 3)

	computeMissingOrphan(sqliteSet, qdrantIDs, report)

	assert.Equal(t, 1, report.MissingCount, "a in SQLite but not Qdrant")
	assert.Contains(t, report.MissingIDs, "a")
	assert.Equal(t, 1, report.OrphanCount, "d in Qdrant but not SQLite")
	assert.Contains(t, report.OrphanIDs, "d")
}

// TestValidatePayloadMinimum_NilPayload verifies nil payload detection.
func TestValidatePayloadMinimum_NilPayload(t *testing.T) {
	t.Parallel()
	issue := validatePayloadMinimum(nil, "pt-1")
	assert.Contains(t, issue, "nil")
}

// TestValidatePayloadMinimum_MissingField verifies missing field detection.
func TestValidatePayloadMinimum_MissingField(t *testing.T) {
	t.Parallel()
	payload := map[string]interface{}{"asset_id": "a1", "source": "youtube"}
	issue := validatePayloadMinimum(payload, "pt-1")
	assert.Contains(t, issue, "name")
}

// TestValidatePayloadMinimum_AllPresent verifies valid payload returns empty.
func TestValidatePayloadMinimum_AllPresent(t *testing.T) {
	t.Parallel()
	payload := map[string]interface{}{"asset_id": "a1", "name": "n1", "source": "youtube"}
	issue := validatePayloadMinimum(payload, "pt-1")
	assert.Empty(t, issue)
}

// buildPointPayload constructs a Qdrant-shaped point JSON with the
// given pt.ID and asset_id, plus all required payload fields.
func buildPointPayload(ptID, assetID string) string {
	return `{"id": "` + ptID + `", "payload": {"asset_id": "` + assetID + `", "name": "n", "source": "youtube", "embedding_version_text": "2026-06-16-v1", "embedding_version_transcript": "2026-06-16-v1", "embedding_version_visual": "2026-06-16-v1", "embedding_version_audio": "2026-06-16-v1"}}`
}

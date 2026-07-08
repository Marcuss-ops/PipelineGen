package nonops

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// newTestContext builds a hermetic gin.Context for testing the
// applyBulkTagsDefaults helper without a real router. The
// Content-Type header is set to application/json so gin's binding
// path matches the production code path.
func newTestContext(t *testing.T, source string, body string) *gin.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(http.MethodPost, "/api/clips/"+source+"/bulk/tags/add", nil)
	} else {
		req = httptest.NewRequest(http.MethodPost, "/api/clips/"+source+"/bulk/tags/add", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	}
	c.Request = req
	c.Params = gin.Params{gin.Param{Key: "source", Value: source}}
	return c
}

// TestApplyBulkTagsDefaults_HappyPath: valid JSON returns the
// canonical (source, IDs, Tags) tuple. This is the load-bearing
// happy-path contract — BulkAddTags + BulkRemoveTags both depend
// on it for their primary data flow.
func TestApplyBulkTagsDefaults_HappyPath(t *testing.T) {
	body := `{"ids":["clip1","clip2"],"tags":["boxing","training"]}`
	c := newTestContext(t, "youtube", body)

	input, err := applyBulkTagsDefaults(c)
	if err != nil {
		t.Fatalf("applyBulkTagsDefaults: unexpected error: %v", err)
	}
	if input.Source != "youtube" {
		t.Errorf("Source = %q, want %q", input.Source, "youtube")
	}
	if len(input.IDs) != 2 || input.IDs[0] != "clip1" || input.IDs[1] != "clip2" {
		t.Errorf("IDs = %v, want [clip1 clip2]", input.IDs)
	}
	if len(input.Tags) != 2 || input.Tags[0] != "boxing" || input.Tags[1] != "training" {
		t.Errorf("Tags = %v, want [boxing training]", input.Tags)
	}
}

// TestApplyBulkTagsDefaults_SourceFromPathParamNotBody: the
// source field is taken from c.Param("source") and NOT from the
// request body, even if the body contains a "source" key.
// This pins the canonical contract that path-param-derived source
// is canonical (a body "source" key MUST be ignored, so callers
// cannot spoof the source via a forged body).
func TestApplyBulkTagsDefaults_SourceFromPathParamNotBody(t *testing.T) {
	// Body intentionally has a different "source" key — the helper
	// MUST ignore it and use c.Param("source") only.
	body := `{"source":"body-source","ids":["clip1"],"tags":["t1"]}`
	c := newTestContext(t, "path-source", body)

	input, err := applyBulkTagsDefaults(c)
	if err != nil {
		t.Fatalf("applyBulkTagsDefaults: unexpected error: %v", err)
	}
	if input.Source != "path-source" {
		t.Errorf("Source = %q, want %q (path param MUST win over body key)", input.Source, "path-source")
	}
}

// TestApplyBulkTagsDefaults_MalformedJSON_ReturnsValidationError:
// truncated / malformed JSON returns a *bulkTagsValidationError
// (typed sentinel) — NOT a generic error. The caller translates
// this to HTTP 400 via apiutil.BadRequest.
func TestApplyBulkTagsDefaults_MalformedJSON_ReturnsValidationError(t *testing.T) {
	body := `{"ids":["clip1","clip2",` // truncated JSON
	c := newTestContext(t, "youtube", body)

	_, err := applyBulkTagsDefaults(c)
	if err == nil {
		t.Fatal("applyBulkTagsDefaults: expected error for malformed JSON, got nil")
	}
	if _, ok := err.(*bulkTagsValidationError); !ok {
		t.Errorf("applyBulkTagsDefaults: error type = %T, want *bulkTagsValidationError", err)
	}
}

// TestApplyBulkTagsDefaults_EmptyBody_ReturnsValidationError:
// empty request body (no Content-Type, no payload) returns a
// *bulkTagsValidationError. Distinct from the malformed-JSON case
// (gin's binding returns EOF for empty bodies). Both surface as
// the same typed sentinel so the caller maps both to HTTP 400.
func TestApplyBulkTagsDefaults_EmptyBody_ReturnsValidationError(t *testing.T) {
	c := newTestContext(t, "youtube", "")

	_, err := applyBulkTagsDefaults(c)
	if err == nil {
		t.Fatal("applyBulkTagsDefaults: expected error for empty body, got nil")
	}
	if _, ok := err.(*bulkTagsValidationError); !ok {
		t.Errorf("applyBulkTagsDefaults: error type = %T, want *bulkTagsValidationError", err)
	}
}

// TestApplyBulkTagsDefaults_PreservesRequestValuesByteEquivalent:
// IDs and Tags slice contents are preserved byte-equivalent
// (including order, including special characters like dashes /
// dots / slashes / newlines that gin's JSON binder must round-trip
// faithfully). The fixture uses a 5-element IDs slice and a
// 3-element Tags slice with mixed special characters to catch
// any re-encoding drift.
func TestApplyBulkTagsDefaults_PreservesRequestValuesByteEquivalent(t *testing.T) {
	// Use round-trip through json.Marshal to ensure the helper's
	// behavior is byte-equivalent regardless of source ordering.
	type wireShape struct {
		IDs  []string `json:"ids"`
		Tags []string `json:"tags"`
	}
	original := wireShape{
		IDs:  []string{"a-1", "b-2", "c/3", "d 4", "e\n5"},
		Tags: []string{"tag-with-dash", "tag.with.dots", "tag/with/slash"},
	}
	bodyBytes, _ := json.Marshal(original)
	c := newTestContext(t, "artlist", string(bodyBytes))

	input, err := applyBulkTagsDefaults(c)
	if err != nil {
		t.Fatalf("applyBulkTagsDefaults: unexpected error: %v", err)
	}
	// Byte-equivalent assertion via json round-trip (round-trips
	// the same wire shape the production code would observe).
	if len(input.IDs) != len(original.IDs) {
		t.Fatalf("IDs length mismatch: got %d, want %d", len(input.IDs), len(original.IDs))
	}
	for i, id := range input.IDs {
		if id != original.IDs[i] {
			t.Errorf("IDs[%d] = %q, want %q", i, id, original.IDs[i])
		}
	}
	if len(input.Tags) != len(original.Tags) {
		t.Fatalf("Tags length mismatch: got %d, want %d", len(input.Tags), len(original.Tags))
	}
	for i, tag := range input.Tags {
		if tag != original.Tags[i] {
			t.Errorf("Tags[%d] = %q, want %q", i, tag, original.Tags[i])
		}
	}
}

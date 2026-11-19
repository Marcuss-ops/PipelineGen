package cliprender

// watermark_strict_decode_test.go — ticket CR-19-09: WatermarkSpec owns a
// custom UnmarshalJSON, so encoding/json hands it the whole subtree and
// DisallowUnknownFields no longer applies. The method must re-establish the
// strict gate itself, exactly like TranscriptSpec does.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestWatermarkUnknownFieldFailsClosed(t *testing.T) {
	var req RenderRequest
	err := json.Unmarshal([]byte(`{"source_asset_id":"asset-1","watermark":{"enabld":true}}`), &req)
	if err == nil {
		t.Fatal("an unknown field inside the watermark block must fail closed")
	}
	if !strings.Contains(err.Error(), `json: unknown field "enabld"`) {
		t.Fatalf("error must preserve the decoder taxonomy, got %q", err.Error())
	}
}

func TestRenderHandler_WatermarkUnknownFieldIsUnknownField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := &stubJobsSvc{}
	rec := doRender(newTestRouter(svc), `{"source_asset_id":"asset-1","watermark":{"enabld":true}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var resp renderResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ErrorCode != ErrCodeUnknownField {
		t.Fatalf("error_code = %q, want %q", resp.ErrorCode, ErrCodeUnknownField)
	}
	if svc.enqueued != nil {
		t.Fatal("a rejected watermark payload must not enqueue")
	}
}

// TestWatermarkExplicitZerosSurviveNormalize keeps the presence recording that
// the custom decoder exists for: an explicit 0 must stay different from an
// absent key after Normalize applies the contract defaults.
func TestWatermarkExplicitZerosSurviveNormalize(t *testing.T) {
	var req RenderRequest
	if err := json.Unmarshal([]byte(`{"source_asset_id":"asset-1","watermark":{"enabled":true,"text":"wm","opacity":0,"margin_px":0}}`), &req); err != nil {
		t.Fatalf("decode explicit zeros: %v", err)
	}
	req.Normalize()
	if !req.Watermark.opacitySet || !req.Watermark.marginSet {
		t.Fatalf("presence flags lost: opacitySet=%v marginSet=%v", req.Watermark.opacitySet, req.Watermark.marginSet)
	}
	if req.Watermark.Opacity != 0 {
		t.Fatalf("explicit opacity 0 was re-defaulted to %v", req.Watermark.Opacity)
	}
	if req.Watermark.MarginPX != 0 {
		t.Fatalf("explicit margin_px 0 was re-defaulted to %d", req.Watermark.MarginPX)
	}

	var absent WatermarkSpec
	if err := json.Unmarshal([]byte(`{}`), &absent); err != nil {
		t.Fatalf("decode absent block: %v", err)
	}
	if absent.opacitySet || absent.marginSet {
		t.Fatalf("absent keys must not be recorded as present: opacitySet=%v marginSet=%v", absent.opacitySet, absent.marginSet)
	}
}

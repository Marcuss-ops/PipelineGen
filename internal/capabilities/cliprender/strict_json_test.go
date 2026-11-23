package cliprender

import (
	"net/http"
	"testing"
)

// TestRenderHandler_RejectsTrailingJSON pins CR-09: the handler accepts exactly
// one JSON document. A second document or trailing garbage must be rejected;
// whitespace remains valid.
func TestRenderHandler_RejectsTrailingJSON(t *testing.T) {
	svc := &stubJobsSvc{}
	r := newTestRouter(svc)

	for name, body := range map[string]string{
		"second document":  `{"source_asset_id":"a"} {"source_asset_id":"b"}`,
		"trailing garbage": `{"source_asset_id":"a"} nope`,
		"trailing value":   `{"source_asset_id":"a"} 123`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := doRender(r, body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}

	// Whitespace/newline after the document is not trailing data.
	if rec := doRender(r, "{\"source_asset_id\":\"a\"}\n\t  "); rec.Code != http.StatusAccepted {
		t.Fatalf("whitespace suffix must be accepted, got %d: %s", rec.Code, rec.Body.String())
	}
}

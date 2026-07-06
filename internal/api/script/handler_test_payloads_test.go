// Package script — handler_test_payloads_test.go: reusable test payload builders.
//
// 2026-07-06 (Phase 2 decomposition): extracted from handler_test.go per
// the god-object decomposition plan. Each builder produces the canonical
// JSON payload string for its respective endpoint, avoiding inline-map
// duplication across test functions.
//
// Usage in tests:
//
//	req := httptest.NewRequest("POST", "/api/script/generate",
//	    strings.NewReader(validScriptGeneratePayload()))
package script

// validScriptGeneratePayload returns the canonical JSON payload for
// POST /api/script/generate (and legacy /generate-from-clips).
func validScriptGeneratePayload() string {
	return `{"topic":"observability","clip_ids":["clip-a"],"language":"it"}`
}

// validLegacyClipPayload returns the canonical JSON payload for
// the legacy clip adapter route (POST /api/script/generate-from-clips
// with an alternative topic/clip pair).
func validLegacyClipPayload() string {
	return `{"topic":"boxing","clip_ids":["clip-b"],"language":"en"}`
}

// validSlideshowPayload returns the canonical JSON payload for
// slide-related endpoints (POST /api/script/generate-with-images
// or similar). Includes images + topic + language.
func validSlideshowPayload() string {
	return `{"images":["img1","img2"],"topic":"boxing","language":"en"}`
}

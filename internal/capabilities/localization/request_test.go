package localization

import (
	"encoding/json"
	"errors"
	"testing"
)

// TestNormalize_AppliesCanonicalDefaults verifies the idempotent
// default pass: zero concurrency becomes DefaultRenderConcurrency and
// well-formed codes are canonicalized to BCP-47 (hyphen, lowercase
// language, uppercase region).
func TestNormalize_AppliesCanonicalDefaults(t *testing.T) {
	req := &LocalizationRequest{
		Languages: []LanguageRequest{
			{Language: "en-us", Priority: 0},
			{Language: "es", Priority: 1},
		},
	}
	req.Normalize()

	if req.RenderConcurrency != DefaultRenderConcurrency {
		t.Errorf("RenderConcurrency: got %d, want %d", req.RenderConcurrency, DefaultRenderConcurrency)
	}
	if got, want := req.Languages[0].Language, "en-US"; got != want {
		t.Errorf("Languages[0].Language: got %q, want %q", got, want)
	}
	if got, want := req.Languages[1].Language, "es"; got != want {
		t.Errorf("Languages[1].Language: got %q, want %q", got, want)
	}
}

// TestNormalize_IsIdempotent verifies a second Normalize pass is a
// no-op on the already-normalized request.
func TestNormalize_IsIdempotent(t *testing.T) {
	req := &LocalizationRequest{
		Languages:         []LanguageRequest{{Language: "it", Priority: 0}},
		RenderConcurrency: 3,
	}
	req.Normalize()
	first := req.RenderConcurrency

	req.Normalize()
	if req.RenderConcurrency != first {
		t.Fatalf("Normalize is not idempotent: %d vs %d", req.RenderConcurrency, first)
	}
	if req.Languages[0].Language != "it" {
		t.Fatalf("Normalize is not idempotent on language: %q", req.Languages[0].Language)
	}
}

// TestValidate_RequiresLanguages verifies the mandatory language gate
// and the nil-receiver gate.
func TestValidate_RequiresLanguages(t *testing.T) {
	if err := (*LocalizationRequest)(nil).Validate(); err == nil {
		t.Fatal("Validate must reject a nil request")
	}

	req := &LocalizationRequest{}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Fatal("Validate must reject an empty language list")
	} else if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error must wrap ErrInvalidRequest, got %v", err)
	}
}

// TestValidate_RejectsInvalidBCP47 verifies the fail-closed BCP-47
// gate: underscore separators, 3-letter codes, full names, empty, and
// undetermined inputs never pass.
func TestValidate_RejectsInvalidBCP47(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{"empty", ""},
		{"whitespace", "   "},
		{"underscore separator", "pt_BR"},
		{"3-letter ISO-639-2", "por"},
		{"3-letter region", "en-USA"},
		{"full language name", "portuguese"},
		{"digit-only", "123"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &LocalizationRequest{
				Languages:         []LanguageRequest{{Language: tc.code, Priority: 0}},
				RenderConcurrency: 2,
			}
			req.Normalize()
			if err := req.Validate(); err == nil {
				t.Fatalf("Validate must reject %q", tc.code)
			} else if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error must wrap ErrInvalidRequest, got %v", err)
			}
		})
	}
}

// TestValidate_RejectsDuplicateAfterCanonicalization verifies that
// "en" and "EN" collide after BCP-47 canonicalization and are rejected.
func TestValidate_RejectsDuplicateAfterCanonicalization(t *testing.T) {
	req := &LocalizationRequest{
		Languages: []LanguageRequest{
			{Language: "en", Priority: 0},
			{Language: "EN", Priority: 1},
		},
		RenderConcurrency: 2,
	}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Fatal("Validate must reject duplicate canonical languages")
	} else if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error must wrap ErrInvalidRequest, got %v", err)
	}
}

// TestValidate_RejectsNegativePriority verifies the priority gate.
func TestValidate_RejectsNegativePriority(t *testing.T) {
	req := &LocalizationRequest{
		Languages:         []LanguageRequest{{Language: "en", Priority: -1}},
		RenderConcurrency: 2,
	}
	req.Normalize()
	if err := req.Validate(); err == nil {
		t.Fatal("Validate must reject a negative priority")
	}
}

// TestValidate_RejectsNegativeConcurrency verifies the concurrency gate
// even without Normalize (a raw negative value never passes).
func TestValidate_RejectsNegativeConcurrency(t *testing.T) {
	req := &LocalizationRequest{
		Languages:         []LanguageRequest{{Language: "en", Priority: 0}},
		RenderConcurrency: -3,
	}
	if err := req.Validate(); err == nil {
		t.Fatal("Validate must reject a negative render_concurrency")
	}
}

// TestValidate_AcceptsCanonicalRequest verifies the plan's canonical
// payload (en/es/it, concurrency 3) validates cleanly.
func TestValidate_AcceptsCanonicalRequest(t *testing.T) {
	req := &LocalizationRequest{
		Languages: []LanguageRequest{
			{Language: "en", Priority: 0},
			{Language: "es", Priority: 1},
			{Language: "it", Priority: 2},
		},
		RenderConcurrency: 3,
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Fatalf("canonical request must validate: %v", err)
	}
}

// TestJSON_RoundTrip verifies the wire shape matches the canonical
// payload exactly and survives a marshal/unmarshal round-trip without
// field drift.
func TestJSON_RoundTrip(t *testing.T) {
	const payload = `{
  "languages": [
    { "language": "en", "priority": 0 },
    { "language": "es", "priority": 1 },
    { "language": "it", "priority": 2 }
  ],
  "render_concurrency": 3
}`

	var req LocalizationRequest
	if err := json.Unmarshal([]byte(payload), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	req.Normalize()
	if err := req.Validate(); err != nil {
		t.Fatalf("round-tripped request must validate: %v", err)
	}
	if len(req.Languages) != 3 {
		t.Fatalf("languages: got %d, want 3", len(req.Languages))
	}
	if req.Languages[0].Language != "en" || req.Languages[0].Priority != 0 {
		t.Fatalf("languages[0]: got %+v, want en/0", req.Languages[0])
	}
	if req.Languages[2].Language != "it" || req.Languages[2].Priority != 2 {
		t.Fatalf("languages[2]: got %+v, want it/2", req.Languages[2])
	}
	if req.RenderConcurrency != 3 {
		t.Fatalf("render_concurrency: got %d, want 3", req.RenderConcurrency)
	}

	out, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back LocalizationRequest
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("re-unmarshal: %v", err)
	}
	if back.Languages[1].Language != "es" || back.Languages[1].Priority != 1 {
		t.Fatalf("languages[1] after round-trip: got %+v, want es/1", back.Languages[1])
	}
}

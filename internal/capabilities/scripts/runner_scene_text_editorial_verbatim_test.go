package scriptgeneration

import (
	"context"
	"strings"
	"testing"
)

// A verbatim script is never rewritten, but it still needs the semantic
// editorial projection when automatic titles/bullets are enabled: without it
// the v2 planner receives no `editorial` section and fails closed. The batch is
// skipped only when the caller disabled editorial overlays or the transcript is
// too large for the local embedder window.
func TestShouldComputeEditorialEnrichmentForVerbatimScripts(t *testing.T) {
	tests := []struct {
		name       string
		verbatim   bool
		disabled   bool
		transcript string
		want       bool
	}{
		{name: "non-verbatim runs", verbatim: false, want: true},
		{name: "verbatim with editorials disabled", verbatim: true, disabled: true, want: false},
		{name: "short verbatim with editorials enabled", verbatim: true, transcript: "Il pubblico urla. Tyson è tornato.", want: true},
		{
			name: "long verbatim keeps the historical skip", verbatim: true,
			transcript: strings.Repeat("a", maxVerbatimEditorialTranscriptRunes+1), want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := GenerateRequest{DisableEditorialOverlays: tc.disabled}
			req.ScriptParams.SourceTextVerbatim = tc.verbatim
			if got := shouldComputeEditorialEnrichment(req, tc.transcript); got != tc.want {
				t.Fatalf("shouldComputeEditorialEnrichment = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLongVerbatimSkipStillBuildsCertifiedEditorialManifest(t *testing.T) {
	req := defaultTestRequest()
	req.ScriptParams.SourceTextVerbatim = true
	transcript := strings.Repeat("narrazione authored. ", maxVerbatimEditorialTranscriptRunes/10)
	scenes := []Scene{{
		ID:   "scene-0",
		Text: map[Language]string{"en": "narrazione authored."},
	}}

	out, err := (&Runner{}).computePhraseImpactEnrichment(context.Background(), transcript, scenes, req)
	if err != nil {
		t.Fatalf("computePhraseImpactEnrichment: %v", err)
	}
	if out.editorial == nil {
		t.Fatal("long verbatim skip left the required editorial section nil")
	}
	if !out.editorial.Certified {
		t.Fatal("empty editorial manifest is not certified")
	}
	if len(out.editorial.Scenes) != 0 {
		t.Fatalf("expected no fabricated editorial scenes, got %d", len(out.editorial.Scenes))
	}
	if out.editorial.Fingerprint == "" {
		t.Fatal("editorial manifest has no fingerprint")
	}
	if err := out.editorial.Validate(); err != nil {
		t.Fatalf("editorial manifest is invalid: %v", err)
	}
}

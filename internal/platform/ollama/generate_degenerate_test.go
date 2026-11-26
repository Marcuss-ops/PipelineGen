package ollama

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/client"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

func TestValidateGeneratedScriptRejectsDegenerateReplies(t *testing.T) {
	cases := []struct {
		name    string
		content string
		wantOK  bool
	}{
		{"empty", "", false},
		{"one word five chars", "ciao", false},
		{"one word", "ok", false},
		{"two words below char floor", "va bene", false},
		{"short but usable prose", "Due parole", true},
		{"normal prose", "Questa e una frase di prova.", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateGeneratedScript(tc.content)
			if tc.wantOK && err != nil {
				t.Fatalf("validateGeneratedScript(%q) = %v, want nil", tc.content, err)
			}
			if !tc.wantOK && !errors.Is(err, ErrDegenerateScriptOutput) {
				t.Fatalf("validateGeneratedScript(%q) = %v, want ErrDegenerateScriptOutput", tc.content, err)
			}
		})
	}
}

// TestGenerateScriptRejectsOneWordReply freezes the live 2026-11-25 failure:
// gemma4:e4b answered with 5 characters / 1 word and the fan-out treated it as
// a successful generation. The generator now rejects it before it reaches the
// scene binder, so the run fails visibly instead of poisoning the pipeline.
func TestGenerateScriptRejectsOneWordReply(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"ciao"},"done":true}`))
	}))
	defer server.Close()

	gen := NewGenerator(client.NewClient(server.URL, "gemma4:e2b", 5))
	_, err := gen.GenerateScript(context.Background(), types.TextGenerationRequest{
		Model: "gemma4:e2b", Language: "it", Title: "degenerate canary",
		Prompt: "scrivi una scena", SourceText: "testo", MaxChars: 100,
	})
	if !errors.Is(err, ErrDegenerateScriptOutput) {
		t.Fatalf("GenerateScript error = %v, want ErrDegenerateScriptOutput", err)
	}
}

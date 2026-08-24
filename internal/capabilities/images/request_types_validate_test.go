// Package images (api/images) — request_types_validate_test.go certifies
// the canonical pre-dispatch validation for image-generation requests:
// whitespace-only prompts and negative dimensions are rejected at the
// contract boundary instead of surfacing too late in the provider/use case.
package images

import (
	"strings"
	"testing"
)

func TestImageGenerationRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		req     ImageGenerationRequest
		wantErr string
	}{
		{name: "valid", req: ImageGenerationRequest{Prompt: "cat", Width: 1920, Height: 1080}},
		{name: "empty prompt", req: ImageGenerationRequest{Prompt: ""}, wantErr: "prompt is required"},
		{name: "whitespace prompt", req: ImageGenerationRequest{Prompt: "   "}, wantErr: "prompt is required"},
		{name: "negative width", req: ImageGenerationRequest{Prompt: "cat", Width: -500}, wantErr: "width must be >= 0"},
		{name: "negative height", req: ImageGenerationRequest{Prompt: "cat", Height: -300}, wantErr: "height must be >= 0"},
		{name: "zero dimensions default", req: ImageGenerationRequest{Prompt: "cat", Width: 0, Height: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestGenerateBatchItem_Validate(t *testing.T) {
	tests := []struct {
		name    string
		item    GenerateBatchItem
		wantErr string
	}{
		{name: "valid", item: GenerateBatchItem{Prompt: "cat", Width: 1920, Height: 1080}},
		{name: "whitespace prompt", item: GenerateBatchItem{Prompt: " \t "}, wantErr: "prompt is required"},
		{name: "negative width", item: GenerateBatchItem{Prompt: "cat", Width: -1}, wantErr: "width must be >= 0"},
		{name: "negative height", item: GenerateBatchItem{Prompt: "cat", Height: -1}, wantErr: "height must be >= 0"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.item.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v should contain %q", err, tt.wantErr)
			}
		})
	}
}

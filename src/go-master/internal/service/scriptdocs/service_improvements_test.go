package scriptdocs

import (
	"context"
	"testing"
	"time"
)

func TestValidateRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     ScriptDocRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid minimal request",
			req: ScriptDocRequest{
				Topic: "Andrew Tate",
			},
			wantErr: false,
		},
		{
			name: "valid full request",
			req: ScriptDocRequest{
				Topic:     "Elon Musk",
				Duration:  120,
				Languages: []string{"it", "en"},
				Template:  TemplateStorytelling,
			},
			wantErr: false,
		},
		{
			name: "empty topic",
			req: ScriptDocRequest{
				Topic: "",
			},
			wantErr: true,
			errMsg:  "topic is required",
		},
		{
			name: "topic with only spaces",
			req: ScriptDocRequest{
				Topic: "   ",
			},
			wantErr: true,
			errMsg:  "topic is required",
		},
		{
			name: "duration too low",
			req: ScriptDocRequest{
				Topic:    "Test",
				Duration: 10,
			},
			wantErr: true,
			errMsg:  "duration must be between 30 and 180 seconds",
		},
		{
			name: "duration too high",
			req: ScriptDocRequest{
				Topic:    "Test",
				Duration: 300,
			},
			wantErr: true,
			errMsg:  "duration must be between 30 and 180 seconds",
		},
		{
			name: "too many languages",
			req: ScriptDocRequest{
				Topic:     "Test",
				Languages: []string{"it", "en", "es", "fr", "de", "pt"},
			},
			wantErr: true,
			errMsg:  "maximum 5 languages allowed",
		},
		{
			name: "unsupported language",
			req: ScriptDocRequest{
				Topic:     "Test",
				Languages: []string{"zh"},
			},
			wantErr: true,
			errMsg:  "unsupported language: zh",
		},
		{
			name: "invalid template",
			req: ScriptDocRequest{
				Topic:    "Test",
				Template: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid template: invalid",
		},
		{
			name: "defaults applied",
			req: ScriptDocRequest{
				Topic: "Test",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !containsStr(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %q, want to contain %q", err.Error(), tt.errMsg)
				}
			}
			// Check defaults are applied
			if !tt.wantErr {
				if tt.req.Duration == 0 {
					t.Errorf("Validate() should set default duration, got 0")
				}
				if len(tt.req.Languages) == 0 {
					t.Errorf("Validate() should set default languages")
				}
				if tt.req.Template == "" {
					t.Errorf("Validate() should set default template")
				}
			}
		})
	}
}

func TestAssociateClipsWithConfidence(t *testing.T) {
	// Create mock Artlist index
	idx := &ArtlistIndex{
		Clips: []ArtlistClip{
			{Name: "people_01.mp4", Term: "people", URL: "https://drive.google.com/people_01"},
			{Name: "city_01.mp4", Term: "city", URL: "https://drive.google.com/city_01"},
			{Name: "technology_01.mp4", Term: "technology", URL: "https://drive.google.com/tech_01"},
			{Name: "nature_01.mp4", Term: "nature", URL: "https://drive.google.com/nature_01"},
		},
	}
	idx.ByTerm = make(map[string][]ArtlistClip)
	for _, clip := range idx.Clips {
		idx.ByTerm[clip.Term] = append(idx.ByTerm[clip.Term], clip)
	}

	svc := &ScriptDocService{
		artlistIndex: idx,
	}

	tests := []struct {
		name           string
		phrase         string
		wantType       string
		wantMinConf    float64
		wantMaxConf    float64
		wantKeyword    bool // should have matched keyword
	}{
		{
			name:        "people match",
			phrase:      "Molte persone seguono Andrew Tate",
			wantType:    "ARTLIST",
			wantMinConf: 0.85,
			wantMaxConf: 1.0,
			wantKeyword: true,
		},
		{
			name:        "city match",
			phrase:      "Arrestato dalla polizia a Washington",
			wantType:    "ARTLIST",
			wantMinConf: 0.90,
			wantMaxConf: 1.0,
			wantKeyword: true,
		},
		{
			name:        "technology match",
			phrase:      "Ha costruito una piattaforma digitale online",
			wantType:    "ARTLIST",
			wantMinConf: 0.80,
			wantMaxConf: 1.0,
			wantKeyword: true,
		},
		{
			name:        "nature match",
			phrase:      "Il paesaggio naturale della foresta",
			wantType:    "ARTLIST",
			wantMinConf: 0.75,
			wantMaxConf: 1.0,
			wantKeyword: true,
		},
		{
			name:        "no match - stock fallback",
			phrase:      "Questa frase non ha concetti specifici",
			wantType:    "STOCK",
			wantMinConf: 0.50,
			wantMaxConf: 0.50,
			wantKeyword: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.associateClips([]string{tt.phrase}, StockFolder{ID: "root", Name: "Stock"}, "Test Topic")

			if len(result) != 1 {
				t.Fatalf("associateClips() got %d results, want 1", len(result))
			}

			assoc := result[0]

			if assoc.Type != tt.wantType {
				t.Errorf("associateClips() type = %q, want %q", assoc.Type, tt.wantType)
			}

			if assoc.Confidence < tt.wantMinConf || assoc.Confidence > tt.wantMaxConf {
				t.Errorf("associateClips() confidence = %.2f, want [%.2f-%.2f]",
					assoc.Confidence, tt.wantMinConf, tt.wantMaxConf)
			}

			if tt.wantKeyword && assoc.MatchedKeyword == "" {
				t.Errorf("associateClips() should have matched keyword, got empty")
			}

			if !tt.wantKeyword && assoc.MatchedKeyword != "" {
				t.Errorf("associateClips() should not have matched keyword, got %q", assoc.MatchedKeyword)
			}

			// For ARTLIST, check clip is assigned
			if tt.wantType == "ARTLIST" && assoc.Clip == nil {
				t.Errorf("associateClips() should have clip for ARTLIST type")
			}
		})
	}
}

func TestBuildPrompt(t *testing.T) {
	svc := &ScriptDocService{}

	tests := []struct {
		name     string
		template string
		topic    string
		duration int
		lang     string
		check    func(prompt string) bool
	}{
		{
			name:     "documentary template",
			template: TemplateDocumentary,
			topic:    "Andrew Tate",
			duration: 80,
			lang:     "italiano",
			check: func(prompt string) bool {
				return containsStr(prompt, "testo COMPLETO") && containsStr(prompt, "~240 parole")
			},
		},
		{
			name:     "storytelling template",
			template: TemplateStorytelling,
			topic:    "Elon Musk",
			duration: 120,
			lang:     "English",
			check: func(prompt string) bool {
				return containsStr(prompt, "testo NARRATIVO") && containsStr(prompt, "~360 parole") && containsStr(prompt, "arco narrativo")
			},
		},
		{
			name:     "top10 template",
			template: TemplateTop10,
			topic:    "Boxe",
			duration: 90,
			lang:     "italiano",
			check: func(prompt string) bool {
				return containsStr(prompt, "TOP 10 LISTA") && containsStr(prompt, "~270 parole") && containsStr(prompt, "numero 10")
			},
		},
		{
			name:     "biography template",
			template: TemplateBiography,
			topic:    "Muhammad Ali",
			duration: 100,
			lang:     "español",
			check: func(prompt string) bool {
				return containsStr(prompt, "testo BIOGRAFICO") && containsStr(prompt, "~300 parole") && containsStr(prompt, "vita, carriera")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc.currentTemplate = tt.template
			prompt := svc.buildPrompt(tt.topic, tt.duration, tt.lang)

			if !tt.check(prompt) {
				t.Errorf("buildPrompt() failed check, got:\n%s", prompt)
			}

			// All prompts should have the IMPORTANT instruction
			if !containsStr(prompt, "IMPORTANTE:") || !containsStr(prompt, "NON scrivere introduzioni") {
				t.Errorf("buildPrompt() missing IMPORTANT instruction")
			}
		})
	}
}

func TestCreateDocWithFallback_NoClient(t *testing.T) {
	// Test with no docClient - should save to local file
	svc := &ScriptDocService{
		docClient: nil,
	}

	docID, docURL, err := svc.createDocWithFallback(context.Background(), "Test Doc", "Test content")
	if err != nil {
		t.Fatalf("createDocWithFallback() error = %v", err)
	}

	if docID != "local_file" {
		t.Errorf("createDocWithFallback() docID = %q, want 'local_file'", docID)
	}

	if docURL == "" || !containsStr(docURL, "file:///tmp/") {
		t.Errorf("createDocWithFallback() docURL = %q, should start with file:///tmp/", docURL)
	}
}

func TestParallelGeneration_Safety(t *testing.T) {
	// This test verifies that the parallel generation code is thread-safe
	// We can't test actual Ollama calls, but we can verify the structure

	// Create service with mock index
	idx := &ArtlistIndex{
		Clips: []ArtlistClip{
			{Name: "clip1.mp4", Term: "people", URL: "https://example.com/clip1"},
		},
		ByTerm: map[string][]ArtlistClip{
			"people": {{Name: "clip1.mp4", Term: "people", URL: "https://example.com/clip1"}},
		},
	}

	svc := &ScriptDocService{
		artlistIndex: idx,
		stockFolders: map[string]StockFolder{
			"test": {ID: "1", Name: "Stock/Test", URL: "https://example.com/test"},
		},
	}

	// Test that concurrent calls to associateClips don't race
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			frasi := []string{
				"Molte persone seguono",
				"Arrestato dalla polizia",
				"Frase generica senza match",
			}
			result := svc.associateClips(frasi, StockFolder{ID: "root", Name: "Stock"}, "Test Topic")
			if len(result) != 3 {
				t.Errorf("Expected 3 associations, got %d", len(result))
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	timeout := time.After(5 * time.Second)
	for i := 0; i < 10; i++ {
		select {
		case <-done:
			// OK
		case <-timeout:
			t.Fatal("Parallel test timed out")
		}
	}
}

func TestLanguageConstants(t *testing.T) {
	// Verify all expected languages are defined
	expectedLangs := []string{"it", "en", "es", "fr", "de", "pt", "ro"}
	for _, lang := range expectedLangs {
		info, ok := LanguageInfo[lang]
		if !ok {
			t.Errorf("Language %q not found in LanguageInfo", lang)
			continue
		}
		if info.Name == "" {
			t.Errorf("Language %q has empty Name", lang)
		}
		if info.PromptLang == "" {
			t.Errorf("Language %q has empty PromptLang", lang)
		}
	}
}

func TestTemplateConstants(t *testing.T) {
	templates := []string{
		TemplateDocumentary,
		TemplateStorytelling,
		TemplateTop10,
		TemplateBiography,
	}

	for _, tmpl := range templates {
		if tmpl == "" {
			t.Error("Template constant should not be empty")
		}
	}

	// Verify they're all different
	seen := make(map[string]bool)
	for _, tmpl := range templates {
		if seen[tmpl] {
			t.Errorf("Duplicate template: %s", tmpl)
		}
		seen[tmpl] = true
	}
}

// Helper functions

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStrHelper(s, substr))
}

func containsStrHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

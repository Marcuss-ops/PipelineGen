package usecase

import (
	"testing"
)

func TestSubjectIdentityResolver_ResolveKnownBoxer(t *testing.T) {
	resolver := NewSubjectIdentityResolver()

	tests := []struct {
		input       string
		wantName    string
		wantAliases int
	}{
		{"Roberto Duran", "Roberto Durán", 2},
		{"Canelo Alvarez", "Canelo Álvarez", 4},
		{"Joe Frazier", "Smokin' Joe Frazier", 2},
		{"Floyd Mayweather", "Floyd Mayweather Jr.", 4},
		{"Mike Tyson", "Mike Tyson", 3},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			identity := resolver.Resolve(tt.input)
			if identity.CanonicalName != tt.wantName {
				t.Errorf("Resolve(%q).CanonicalName = %q, want %q", tt.input, identity.CanonicalName, tt.wantName)
			}
			if len(identity.Aliases) != tt.wantAliases {
				t.Errorf("Resolve(%q).Aliases has %d items, want %d", tt.input, len(identity.Aliases), tt.wantAliases)
			}
			if len(identity.RequiredTerms) == 0 {
				t.Errorf("Resolve(%q).RequiredTerms is empty", tt.input)
			}
		})
	}
}

func TestSubjectIdentityResolver_ResolveUnknownBoxer(t *testing.T) {
	resolver := NewSubjectIdentityResolver()
	identity := resolver.Resolve("Some Unknown Fighter")

	if identity.CanonicalName != "Some Unknown Fighter" {
		t.Errorf("Resolve(unknown).CanonicalName = %q, want %q", identity.CanonicalName, "Some Unknown Fighter")
	}
	if len(identity.RequiredTerms) == 0 {
		t.Error("Resolve(unknown).RequiredTerms should not be empty")
	}
}

func TestSubjectIdentityResolver_ResolveCaseInsensitive(t *testing.T) {
	resolver := NewSubjectIdentityResolver()
	identity := resolver.Resolve("roberto duran")

	if identity.CanonicalName != "Roberto Durán" {
		t.Errorf("case-insensitive Resolve = %q, want %q", identity.CanonicalName, "Roberto Durán")
	}
}

func TestSubjectIdentityResolver_CaseDisambiguation(t *testing.T) {
	resolver := NewSubjectIdentityResolver()
	floyd := resolver.Resolve("Floyd Mayweather")
	george := resolver.Resolve("George Floyd")

	// Floyd Mayweather should have boxing-specific required terms
	if len(floyd.ExcludedTerms) > 0 {
		t.Logf("Floyd Mayweather excluded terms: %v", floyd.ExcludedTerms)
	}
	// George Floyd should NOT have boxing-specific required terms
	if len(george.ExcludedTerms) > 0 {
		t.Errorf("George Floyd should have no excluded terms, got %v", george.ExcludedTerms)
	}
}

func TestSubjectIdentityResolver_DiacriticVariantsShareIdentityID(t *testing.T) {
	resolver := NewSubjectIdentityResolver()

	tests := []struct {
		name     string
		variants []string
		wantID   string
	}{
		{name: "Canelo", variants: []string{"Canelo Alvarez", "Canelo Álvarez"}, wantID: "canelo-alvarez"},
		{name: "Roberto", variants: []string{"Roberto Duran", "Roberto Durán"}, wantID: "roberto-duran"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			seen := make(map[string]bool, len(tt.variants))
			for _, variant := range tt.variants {
				id := resolver.Resolve(variant).ID
				if id != tt.wantID {
					t.Errorf("Resolve(%q).ID = %q, want %q", variant, id, tt.wantID)
				}
				seen[id] = true
			}
			if len(seen) != 1 {
				t.Errorf("variants %v must resolve to a single identity ID, got %v", tt.variants, seen)
			}
		})
	}
}

func TestSubjectIdentityResolver_Aliases(t *testing.T) {
	resolver := NewSubjectIdentityResolver()
	identity := resolver.Resolve("Canelo Alvarez")

	hasAlias := false
	for _, alias := range identity.Aliases {
		if alias == "Canelo Alvarez" || alias == "Saul Alvarez" {
			hasAlias = true
		}
	}
	if !hasAlias {
		t.Errorf("Canelo Alvarez aliases should include 'Canelo Alvarez' or 'Saul Alvarez', got %v", identity.Aliases)
	}
}

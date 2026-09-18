package adapters

import "testing"

func TestValidateRequestedTranscriptLanguage(t *testing.T) {
	tests := []struct {
		name      string
		requested string
		actual    string
		wantErr   bool
	}{
		{name: "same language", requested: "it", actual: "it"},
		{name: "case insensitive same language", requested: "en-US", actual: "EN-us"},
		{name: "empty actual is allowed for downstream defaulting", requested: "en", actual: ""},
		{name: "different language fails closed", requested: "it", actual: "en", wantErr: true},
		{name: "different language fails closed for German", requested: "de", actual: "en", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequestedTranscriptLanguage(tt.requested, tt.actual)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateRequestedTranscriptLanguage(%q, %q) error = %v, wantErr=%v", tt.requested, tt.actual, err, tt.wantErr)
			}
		})
	}
}

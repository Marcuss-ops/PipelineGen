package platform

import "testing"

func TestString(t *testing.T) {
	tests := []struct {
		name     string
		val      string
		fallback string
		want     string
	}{
		{"non-empty", "hello", "fallback", "hello"},
		{"empty", "", "fallback", "fallback"},
		{"whitespace only", "   ", "fallback", "fallback"},
		{"both empty", "", "", ""},
		{"whitespace and fallback", "  ", "default", "default"},
		{"no fallback needed", "value", "", "value"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := String(tt.val, tt.fallback)
			if got != tt.want {
				t.Errorf("String(%q, %q) = %q, want %q", tt.val, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestInt(t *testing.T) {
	tests := []struct {
		name     string
		val      int
		fallback int
		want     int
	}{
		{"positive", 10, 5, 10},
		{"zero", 0, 5, 5},
		{"negative", -1, 5, 5},
		{"zero fallback", 0, 0, 0},
		{"one", 1, 99, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Int(tt.val, tt.fallback)
			if got != tt.want {
				t.Errorf("Int(%d, %d) = %d, want %d", tt.val, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestFloat64(t *testing.T) {
	tests := []struct {
		name     string
		val      float64
		fallback float64
		want     float64
	}{
		{"positive", 3.14, 1.0, 3.14},
		{"zero", 0, 1.5, 1.5},
		{"negative", -0.5, 2.0, 2.0},
		{"very small positive", 0.001, 1.0, 0.001},
		{"zero fallback", 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Float64(tt.val, tt.fallback)
			if got != tt.want {
				t.Errorf("Float64(%f, %f) = %f, want %f", tt.val, tt.fallback, got, tt.want)
			}
		})
	}
}

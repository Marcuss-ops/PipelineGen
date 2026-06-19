package platform

import (
	"reflect"
	"testing"
)

func TestUniqueStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"empty", []string{}, []string{}},
		{"no duplicates", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with duplicates", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"case sensitive", []string{"A", "a", "A"}, []string{"A", "a"}},
		{"nil input", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UniqueStrings(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("UniqueStrings(%v) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestUniqueStringsCI(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"empty", []string{}, []string{}},
		{"no duplicates", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with duplicates", []string{"a", "b", "a", "c", "b"}, []string{"a", "b", "c"}},
		{"case insensitive", []string{"A", "a", "B", "b"}, []string{"A", "B"}},
		{"mixed case keeps first", []string{"Hello", "hello", "HELLO"}, []string{"Hello"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UniqueStringsCI(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("UniqueStringsCI(%v) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestTrimStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected []string
	}{
		{"empty", []string{}, []string{}},
		{"no trimming", []string{"a", "b", "c"}, []string{"a", "b", "c"}},
		{"with spaces", []string{" a", "b ", " c "}, []string{"a", "b", "c"}},
		{"nil input", nil, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TrimStrings(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("TrimStrings(%v) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name     string
		primary  []string
		fallback []string
		expected []string
	}{
		{"primary empty", []string{}, []string{"a", "b"}, []string{"a", "b"}},
		{"primary non-empty", []string{"a", "b"}, []string{"c", "d"}, []string{"a", "b"}},
		{"both empty", []string{}, []string{}, []string{}},
		{"nil primary", nil, []string{"a"}, []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FirstNonEmptySlice(tt.primary, tt.fallback)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("FirstNonEmptySlice(%v, %v) = %v, want %v", tt.primary, tt.fallback, got, tt.expected)
			}
		})
	}
}

func TestAppendUniqueStrings(t *testing.T) {
	tests := []struct {
		name     string
		dst      []string
		values   []string
		expected []string
	}{
		{"empty dst empty values", []string{}, []string{}, []string{}},
		{"append to empty", []string{}, []string{"a", "b"}, []string{"a", "b"}},
		{"no duplicates", []string{"a"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"skip duplicates", []string{"a"}, []string{"a", "b"}, []string{"a", "b"}},
		{"case insensitive", []string{"A"}, []string{"a", "b"}, []string{"A", "b"}},
		{"trim and skip empty", []string{}, []string{" a ", "", "  "}, []string{"a"}},
		{"nil dst", nil, []string{"a"}, []string{"a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AppendUniqueStrings(tt.dst, tt.values...)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("AppendUniqueStrings(%v, %v) = %v, want %v", tt.dst, tt.values, got, tt.expected)
			}
		})
	}
}

func TestUniqueLimitedStrings(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		limit    int
		expected []string
	}{
		{"empty", []string{}, 3, []string{}},
		{"no limit", []string{"a", "b", "a"}, 0, []string{"a", "b"}},
		{"within limit", []string{"a", "b", "c"}, 5, []string{"a", "b", "c"}},
		{"at limit", []string{"a", "b", "c"}, 3, []string{"a", "b", "c"}},
		{"exceeds limit", []string{"a", "b", "c", "d"}, 2, []string{"a", "b"}},
		{"case insensitive dedup", []string{"A", "a", "B"}, 3, []string{"A", "B"}},
		{"trim and skip empty", []string{" a ", "", " a ", "b "}, 3, []string{"a", "b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := UniqueLimitedStrings(tt.input, tt.limit)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("UniqueLimitedStrings(%v, %d) = %v, want %v", tt.input, tt.limit, got, tt.expected)
			}
		})
	}
}

func TestMinInt(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int
		expected int
	}{
		{"a smaller", 1, 2, 1},
		{"b smaller", 3, 2, 2},
		{"equal", 5, 5, 5},
		{"negative", -3, -1, -3},
		{"zero", 0, 5, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MinInt(tt.a, tt.b)
			if got != tt.expected {
				t.Errorf("MinInt(%d, %d) = %d, want %d", tt.a, tt.b, got, tt.expected)
			}
		})
	}
}

func TestClamp(t *testing.T) {
	tests := []struct {
		name      string
		v, lo, hi int
		expected  int
	}{
		{"within range", 5, 0, 10, 5},
		{"below lo", -1, 0, 10, 0},
		{"above hi", 15, 0, 10, 10},
		{"at lo", 0, 0, 10, 0},
		{"at hi", 10, 0, 10, 10},
		{"negative range", -5, -10, -1, -5},
		{"below negative lo", -15, -10, -1, -10},
		{"above negative hi", 0, -10, -1, -1},
		{"all zero", 0, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Clamp(tt.v, tt.lo, tt.hi)
			if got != tt.expected {
				t.Errorf("Clamp(%d, %d, %d) = %d, want %d", tt.v, tt.lo, tt.hi, got, tt.expected)
			}
		})
	}
}

func TestClampFloat64(t *testing.T) {
	tests := []struct {
		name      string
		v, lo, hi float64
		expected  float64
	}{
		{"within range", 0.5, 0.0, 1.0, 0.5},
		{"below lo", -0.1, 0.0, 1.0, 0.0},
		{"above hi", 1.5, 0.0, 1.0, 1.0},
		{"at lo", 0.0, 0.0, 1.0, 0.0},
		{"at hi", 1.0, 0.0, 1.0, 1.0},
		{"negative range", -0.5, -1.0, -0.1, -0.5},
		{"below negative lo", -2.0, -1.0, 0.0, -1.0},
		{"above negative hi", 1.0, -1.0, 0.0, 0.0},
		{"all zero", 0.0, 0.0, 0.0, 0.0},
		{"fractional precision", 0.3333, 0.0, 1.0, 0.3333},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClampFloat64(tt.v, tt.lo, tt.hi)
			if got != tt.expected {
				t.Errorf("ClampFloat64(%v, %v, %v) = %v, want %v", tt.v, tt.lo, tt.hi, got, tt.expected)
			}
		})
	}
}

func TestGroupSentences(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		size     int
		expected []string
	}{
		{"empty", []string{}, 3, nil},
		{"nil", nil, 3, nil},
		{"size 1", []string{"a", "b", "c"}, 1, []string{"a", "b", "c"}},
		{"size 2", []string{"a", "b", "c", "d"}, 2, []string{"a b", "c d"}},
		{"size 3 remainder", []string{"a", "b", "c", "d"}, 3, []string{"a b c", "d"}},
		{"default size", []string{"a", "b", "c", "d", "e", "f"}, 0, []string{"a b c d e", "f"}},
		{"skip empty", []string{"a", "", "  ", "b"}, 2, []string{"a b"}},
		{"trim spaces", []string{" a ", " b "}, 2, []string{"a b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GroupSentences(tt.input, tt.size)
			if !reflect.DeepEqual(got, tt.expected) {
				t.Errorf("GroupSentences(%v, %d) = %v, want %v", tt.input, tt.size, got, tt.expected)
			}
		})
	}
}

package sliceutil

import (
	"reflect"
	"strings"
	"testing"
)

func lowerTrim(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func skipEmpty(s string) bool {
	return s == ""
}

func TestNormalizeAndDedupe(t *testing.T) {
	cases := []struct {
		name      string
		input     []string
		normalize NormalizeFunc
		skip      SkipFunc
		want      []string
	}{
		{
			name:  "nil input returns nil",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty input returns nil",
			input: []string{},
			want:  nil,
		},
		{
			name:      "identity normalize+skip keeps order and dedupes",
			input:     []string{"a", "b", "a", "c"},
			normalize: nil,
			skip:      nil,
			want:      []string{"a", "b", "c"},
		},
		{
			name:      "normalize lowercases and trims",
			input:     []string{"  Foo ", "FOO", "bar"},
			normalize: lowerTrim,
			skip:      nil,
			want:      []string{"foo", "bar"},
		},
		{
			name:      "skip filters empty after normalize",
			input:     []string{"  ", "foo", "", "bar"},
			normalize: lowerTrim,
			skip:      skipEmpty,
			want:      []string{"foo", "bar"},
		},
		{
			name:      "input slice is not mutated",
			input:     []string{"a", "b"},
			normalize: lowerTrim,
			skip:      nil,
			want:      []string{"a", "b"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeAndDedupe(tc.input, tc.normalize, tc.skip)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergeNormalizedLists(t *testing.T) {
	cases := []struct {
		name      string
		lists     [][]string
		normalize NormalizeFunc
		skip      SkipFunc
		want      []string
	}{
		{
			name:  "no lists returns nil",
			lists: nil,
			want:  nil,
		},
		{
			name:  "all empty lists returns nil",
			lists: [][]string{nil, {}, nil},
			want:  nil,
		},
		{
			name:  "merges and dedupes across lists",
			lists: [][]string{{"a", "b"}, {"b", "c"}, {"c", "d"}},
			want:  []string{"a", "b", "c", "d"},
		},
		{
			name:      "normalize and skip applied across lists",
			lists:     [][]string{{"Foo", " BAR "}, {"baz", "  "}},
			normalize: lowerTrim,
			skip:      skipEmpty,
			want:      []string{"foo", "bar", "baz"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeNormalizedLists(tc.lists, tc.normalize, tc.skip)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergeNormalizedListsVariadic(t *testing.T) {
	got := MergeNormalizedListsVariadic(
		lowerTrim,
		skipEmpty,
		[]string{"  Foo ", "bar"},
		[]string{"FOO", "baz"},
		nil,
	)
	want := []string{"foo", "bar", "baz"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeAndDedupe_DoesNotMutateInput(t *testing.T) {
	in := []string{"a", "b", "c"}
	original := append([]string(nil), in...)
	_ = NormalizeAndDedupe(in, strings.ToLower, nil)
	if !reflect.DeepEqual(in, original) {
		t.Errorf("input mutated: got %v, want %v", in, original)
	}
}

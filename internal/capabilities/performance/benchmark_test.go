package performance

import "testing"

func TestMedianInt64(t *testing.T) {
	cases := []struct {
		name   string
		values []int64
		want   float64
	}{
		{"empty", nil, 0},
		{"single", []int64{42}, 42},
		{"odd", []int64{5, 1, 3}, 3},
		{"even", []int64{5, 1, 3, 9}, 4},
		{"unsorted even", []int64{100, 300, 200, 400}, 250},
	}
	for _, tc := range cases {
		if got := MedianInt64(tc.values); got != tc.want {
			t.Errorf("%s: median = %v, want %v", tc.name, got, tc.want)
		}
	}
}

package clips

import "testing"

func TestParseRangeHeader(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		size      int64
		wantStart int64
		wantEnd   int64
		wantOK    bool
	}{
		{name: "closed range", raw: "bytes=0-99", size: 1000, wantStart: 0, wantEnd: 99, wantOK: true},
		{name: "open ended", raw: "bytes=900-", size: 1000, wantStart: 900, wantEnd: 999, wantOK: true},
		{name: "suffix", raw: "bytes=-100", size: 1000, wantStart: 900, wantEnd: 999, wantOK: true},
		{name: "end clamped", raw: "bytes=0-2000", size: 1000, wantStart: 0, wantEnd: 999, wantOK: true},
		{name: "start at size", raw: "bytes=1000-", size: 1000, wantOK: false},
		{name: "reversed", raw: "bytes=5-3", size: 1000, wantOK: false},
		{name: "multi range", raw: "bytes=0-1,4-5", size: 1000, wantOK: false},
		{name: "wrong unit", raw: "items=0-1", size: 1000, wantOK: false},
		{name: "zero size", raw: "bytes=0-", size: 0, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, ok := parseRangeHeader(tt.raw, tt.size)
			if ok != tt.wantOK {
				t.Fatalf("ok=%v, want %v", ok, tt.wantOK)
			}
			if ok && (start != tt.wantStart || end != tt.wantEnd) {
				t.Fatalf("range=%d-%d, want %d-%d", start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}

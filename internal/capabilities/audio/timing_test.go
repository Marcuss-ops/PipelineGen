package audio

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestTiming_DefaultRequest(t *testing.T) {
	def := DefaultTimingRequest()
	if def.Mode != TimingBestEffort {
		t.Fatalf("default mode = %q, want best_effort", def.Mode)
	}
	if def.BoundaryMode != BoundaryWord {
		t.Fatalf("default boundary = %q, want word", def.BoundaryMode)
	}
	if !reflect.DeepEqual(def.Formats, []TimingFormat{TimingJSON}) {
		t.Fatalf("default formats = %v, want [json]", def.Formats)
	}
	if err := def.Validate(); err != nil {
		t.Fatalf("default policy must validate: %v", err)
	}
}

func TestTiming_NormalizedFillsDefaults(t *testing.T) {
	got := TimingRequest{}.Normalized()
	want := DefaultTimingRequest()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized zero request = %+v, want %+v", got, want)
	}
}

func TestTiming_NormalizedPreservesExplicitValues(t *testing.T) {
	got := TimingRequest{
		Mode:         TimingRequired,
		BoundaryMode: BoundaryWord,
		Formats:      []TimingFormat{TimingSRT, TimingVTT},
	}.Normalized()
	if got.Mode != TimingRequired || got.BoundaryMode != BoundaryWord {
		t.Fatalf("normalized dropped explicit values: %+v", got)
	}
	if !reflect.DeepEqual(got.Formats, []TimingFormat{TimingSRT, TimingVTT}) {
		t.Fatalf("normalized formats = %v, want [srt vtt]", got.Formats)
	}
}

func TestTiming_NormalizedDeduplicatesFormats(t *testing.T) {
	got := TimingRequest{
		Mode:    TimingBestEffort,
		Formats: []TimingFormat{TimingJSON, TimingSRT, TimingJSON, TimingVTT},
	}.Normalized()
	if !reflect.DeepEqual(got.Formats, []TimingFormat{TimingJSON, TimingSRT, TimingVTT}) {
		t.Fatalf("deduplicated formats = %v, want [json srt vtt]", got.Formats)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("deduplicated normalized policy must validate: %v", err)
	}
}

func TestTiming_ValidateRejectsInvalidMode(t *testing.T) {
	req := DefaultTimingRequest()
	req.Mode = "always"
	if err := req.Validate(); !errors.Is(err, ErrInvalidTimingMode) {
		t.Fatalf("error = %v, want ErrInvalidTimingMode", err)
	}
}

func TestTiming_ValidateRejectsZeroRequest(t *testing.T) {
	err := TimingRequest{}.Validate()
	if !errors.Is(err, ErrInvalidTimingMode) {
		t.Fatalf("zero request error = %v, want ErrInvalidTimingMode", err)
	}
}

func TestTiming_ValidateRejectsUnsupportedBoundary(t *testing.T) {
	req := DefaultTimingRequest()
	req.BoundaryMode = "sentence"
	if err := req.Validate(); !errors.Is(err, ErrUnsupportedBoundaryMode) {
		t.Fatalf("error = %v, want ErrUnsupportedBoundaryMode", err)
	}
}

func TestTiming_ValidateRejectsEmptyAndUnknownFormats(t *testing.T) {
	noFormats := DefaultTimingRequest()
	noFormats.Formats = nil
	if err := noFormats.Validate(); !errors.Is(err, ErrInvalidTimingFormat) {
		t.Fatalf("empty formats error = %v, want ErrInvalidTimingFormat", err)
	}

	unknown := DefaultTimingRequest()
	unknown.Formats = []TimingFormat{"lrc"}
	if err := unknown.Validate(); !errors.Is(err, ErrInvalidTimingFormat) {
		t.Fatalf("unknown format error = %v, want ErrInvalidTimingFormat", err)
	}

	duplicate := DefaultTimingRequest()
	duplicate.Formats = []TimingFormat{TimingJSON, TimingJSON}
	if err := duplicate.Validate(); !errors.Is(err, ErrInvalidTimingFormat) {
		t.Fatalf("duplicate format error = %v, want ErrInvalidTimingFormat", err)
	}
}

func TestTiming_HasFormat(t *testing.T) {
	req := TimingRequest{Mode: TimingRequired, Formats: []TimingFormat{TimingJSON, TimingVTT}}
	if !req.HasFormat(TimingJSON) || !req.HasFormat(TimingVTT) {
		t.Fatal("HasFormat missed a requested format")
	}
	if req.HasFormat(TimingSRT) {
		t.Fatal("HasFormat reported an unrequested format")
	}
	empty := TimingRequest{}
	if !empty.HasFormat(TimingJSON) {
		t.Fatal("empty request must default to json format")
	}
}

func TestTiming_JSONRoundTrip(t *testing.T) {
	original := TimingRequest{Mode: TimingRequired, BoundaryMode: BoundaryWord, Formats: []TimingFormat{TimingJSON, TimingSRT, TimingVTT}}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mode", "boundary", "formats"} {
		if _, ok := wire[key]; !ok {
			t.Fatalf("timing request missing canonical field %q: %s", key, encoded)
		}
	}
	var decoded TimingRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, decoded) {
		t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", decoded, original)
	}
}

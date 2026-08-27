package audio

// Contratto di timing voce: policy provider-neutral appartenente al
// kernel (SSOT dei tipi usati sia dallo spec che dalla capability).

import (
	"errors"
	"fmt"
)

// BoundaryMode identifica la granularita dei confini vocali.
type BoundaryMode string

const (
	BoundaryWord BoundaryMode = "word"
)

// ErrUnsupportedBoundaryMode viene restituito da Validate quando la
// granularita richiesta non e supportata.
var ErrUnsupportedBoundaryMode = errors.New("unsupported voiceover timing boundary mode")

// TimingMode is the voiceover timing capture policy. Timing capture must
// never be implicitly mandatory: callers opt in via required, tolerate
// best-effort, or keep today's behavior with disabled.
type TimingMode string

const (
	TimingDisabled   TimingMode = "disabled"
	TimingBestEffort TimingMode = "best_effort"
	TimingRequired   TimingMode = "required"
)

// TimingFormat is a subtitle projection rendered from the canonical
// timing.json. The JSON artifact itself is always produced when capture is
// enabled; srt/vtt are display projections derived from it.
type TimingFormat string

const (
	TimingJSON TimingFormat = "json"
	TimingSRT  TimingFormat = "srt"
	TimingVTT  TimingFormat = "vtt"
)

// TimingRequest is the canonical, provider-neutral timing policy carried by
// the existing voiceover/audio configuration. It never lives as an
// independent root: callers nest it inside their audio config (wire key
// "voiceover_timing" / "timing").
type TimingRequest struct {
	Mode         TimingMode     `json:"mode,omitempty"`
	BoundaryMode BoundaryMode   `json:"boundary,omitempty"`
	Formats      []TimingFormat `json:"formats,omitempty"`
}

var (
	ErrInvalidTimingMode   = errors.New("invalid voiceover timing mode")
	ErrInvalidTimingFormat = errors.New("invalid voiceover timing format")
)

// DefaultTimingRequest returns the canonical initial policy:
// best_effort + word boundaries + json only. Providers that proved
// reliable in production can later be pinned to required by callers.
func DefaultTimingRequest() TimingRequest {
	return TimingRequest{
		Mode:         TimingBestEffort,
		BoundaryMode: BoundaryWord,
		Formats:      []TimingFormat{TimingJSON},
	}
}

// Normalized returns the effective policy: empty slots fall back to the
// canonical defaults while explicit values are preserved verbatim (so a
// caller-explicit invalid value still surfaces through Validate). Formats
// are deduplicated preserving first-occurrence order.
func (t TimingRequest) Normalized() TimingRequest {
	def := DefaultTimingRequest()
	if t.Mode == "" {
		t.Mode = def.Mode
	}
	if t.BoundaryMode == "" {
		t.BoundaryMode = def.BoundaryMode
	}
	if len(t.Formats) == 0 {
		t.Formats = append([]TimingFormat(nil), def.Formats...)
		return t
	}
	seen := make(map[TimingFormat]struct{}, len(t.Formats))
	formats := make([]TimingFormat, 0, len(t.Formats))
	for _, f := range t.Formats {
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		formats = append(formats, f)
	}
	t.Formats = formats
	return t
}

// Validate enforces the canonical timing policy contract. Callers that want
// default-filling semantics must Normalize first; zero-value requests are
// rejected here so a silently-defaulted policy is always an explicit choice.
func (t TimingRequest) Validate() error {
	if t.Mode != TimingDisabled && t.Mode != TimingBestEffort && t.Mode != TimingRequired {
		return fmt.Errorf("%w: %q", ErrInvalidTimingMode, t.Mode)
	}
	if t.BoundaryMode != BoundaryWord {
		return fmt.Errorf("%w: %q", ErrUnsupportedBoundaryMode, t.BoundaryMode)
	}
	if len(t.Formats) == 0 {
		return fmt.Errorf("%w: at least one format required", ErrInvalidTimingFormat)
	}
	seen := make(map[TimingFormat]struct{}, len(t.Formats))
	for _, f := range t.Formats {
		if f != TimingJSON && f != TimingSRT && f != TimingVTT {
			return fmt.Errorf("%w: %q", ErrInvalidTimingFormat, f)
		}
		if _, dup := seen[f]; dup {
			return fmt.Errorf("%w: duplicate %q", ErrInvalidTimingFormat, f)
		}
		seen[f] = struct{}{}
	}
	return nil
}

// HasFormat reports whether the normalized request requests the given
// subtitle projection. Formats are defaulted before lookup so an empty
// request still reports TimingJSON.
func (t TimingRequest) HasFormat(f TimingFormat) bool {
	for _, candidate := range t.Normalized().Formats {
		if candidate == f {
			return true
		}
	}
	return false
}

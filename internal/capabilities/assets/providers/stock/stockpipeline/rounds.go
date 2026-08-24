package stockpipeline

import (
	"fmt"
	"strconv"
	"strings"
)

// RoundIndexSpec is the compact timestamp-index format accepted by stock.
type RoundIndexSpec struct {
	Round          any    `json:"round"`
	TimestampStart string `json:"timestamp_start"`
	TimestampEnd   string `json:"timestamp_end"`
	Description    string `json:"description,omitempty"`
}

const (
	defaultRoundClipDurationSec = 4
	maxRoundStockDurationSec    = 60
)

// ExpandRoundIndexing deterministically creates at most fifteen 4-second
// clips per round (or the requested duration), never exceeding one minute.
func ExpandRoundIndexing(rounds []RoundIndexSpec, clipDuration int) ([]ClipSpec, error) {
	if len(rounds) == 0 {
		return nil, nil
	}
	if clipDuration <= 0 {
		clipDuration = defaultRoundClipDurationSec
	}
	if clipDuration > maxRoundStockDurationSec {
		return nil, fmt.Errorf("round clip duration %d exceeds round budget %d", clipDuration, maxRoundStockDurationSec)
	}
	clips := make([]ClipSpec, 0, len(rounds)*maxRoundStockDurationSec/clipDuration)
	for _, round := range rounds {
		start, err := parseRoundTimestamp(round.TimestampStart)
		if err != nil {
			return nil, fmt.Errorf("round %v start: %w", round.Round, err)
		}
		end, err := parseRoundTimestamp(round.TimestampEnd)
		if err != nil {
			return nil, fmt.Errorf("round %v end: %w", round.Round, err)
		}
		if end <= start {
			return nil, fmt.Errorf("round %v has non-positive timestamp range", round.Round)
		}
		stop := start + maxRoundStockDurationSec
		if end < stop {
			stop = end
		}
		label := roundLabel(round.Round)
		for cursor := start; cursor+clipDuration <= stop; cursor += clipDuration {
			clip := ClipSpec{Title: label, Description: round.Description, StartSec: float64(cursor), EndSec: float64(cursor + clipDuration)}
			if n, ok := roundNumber(round.Round); ok {
				clip.Round = n
			} else {
				clip.Slug = label
			}
			clips = append(clips, clip)
		}
	}
	return clips, nil
}

func parseRoundTimestamp(raw string) (int, error) {
	parts := strings.Split(strings.TrimSpace(raw), ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("invalid timestamp %q", raw)
	}
	h, e1 := strconv.Atoi(parts[0])
	m, e2 := strconv.Atoi(parts[1])
	s, e3 := strconv.Atoi(parts[2])
	if e1 != nil || e2 != nil || e3 != nil || h < 0 || m < 0 || m > 59 || s < 0 || s > 59 {
		return 0, fmt.Errorf("invalid timestamp %q", raw)
	}
	return h*3600 + m*60 + s, nil
}

func roundNumber(raw any) (int, bool) {
	switch v := raw.(type) {
	case float64:
		return int(v), v >= 1 && v == float64(int(v))
	case int:
		return v, v >= 1
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil && n >= 1
	default:
		return 0, false
	}
}

func roundLabel(raw any) string {
	if n, ok := roundNumber(raw); ok {
		return fmt.Sprintf("Round %d", n)
	}
	if s, ok := raw.(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return "Round"
}

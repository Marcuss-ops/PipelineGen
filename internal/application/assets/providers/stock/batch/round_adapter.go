// Package batch contains stock batch-ingress adapters. It is deliberately
// independent from the normal stock run contract: rounds_indexing is
// expanded here and never becomes part of a StockRunPayload.
package batch

import (
	"fmt"
	"strconv"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers/stock/stockpipeline"
)

type RoundIndexSpec struct {
	Round          any    `json:"round"`
	TimestampStart string `json:"timestamp_start"`
	TimestampEnd   string `json:"timestamp_end"`
	Description    string `json:"description,omitempty"`
}

type Clip struct {
	Title, Description string
	StartSec, EndSec   float64
	Round              int
	Slug               string
}

type ChildPlan struct {
	Name  string
	Clips []Clip
}

// BuildChildCommands converts the batch-only shorthand into ordinary stock
// commands. The caller may enqueue each returned command independently; no
// rounds_indexing field is present in any child payload.
func BuildChildCommands(base stockpipeline.StockCommand, rounds []RoundIndexSpec) ([]stockpipeline.StockCommand, error) {
	clips, err := ExpandRounds(rounds, base.ClipDuration)
	if err != nil {
		return nil, err
	}
	children := SplitChildren(clips)
	out := make([]stockpipeline.StockCommand, 0, len(children))
	for _, child := range children {
		cmd := base
		cmd.Clips = make([]stockpipeline.ClipSpec, 0, len(child.Clips))
		cmd.Subfolder = child.Name
		for _, c := range child.Clips {
			cmd.Clips = append(cmd.Clips, stockpipeline.ClipSpec{
				Title: c.Title, Description: c.Description, URL: firstURL(base),
				StartSec: c.StartSec, EndSec: c.EndSec, Round: c.Round, Slug: c.Slug,
			})
		}
		out = append(out, cmd)
	}
	return out, nil
}

func firstURL(cmd stockpipeline.StockCommand) string {
	if len(cmd.DirectURLs) > 0 {
		return cmd.DirectURLs[0]
	}
	if len(cmd.DriveURLs) > 0 {
		return cmd.DriveURLs[0]
	}
	return ""
}

// SplitChildren preserves section order and guarantees that every child is
// below the normal stock endpoint's 100-clip limit.
func SplitChildren(clips []Clip) []ChildPlan {
	if len(clips) == 0 {
		return nil
	}
	plans := make([]ChildPlan, 0, 16)
	for _, clip := range clips {
		name := clip.Title
		if len(plans) == 0 || plans[len(plans)-1].Name != name || len(plans[len(plans)-1].Clips) >= 100 {
			plans = append(plans, ChildPlan{Name: name})
		}
		last := &plans[len(plans)-1]
		last.Clips = append(last.Clips, clip)
	}
	return plans
}

// ExpandRounds creates at most fifteen four-second clips per section and
// keeps the source contract free of batch-only fields.
func ExpandRounds(rounds []RoundIndexSpec, clipDuration int) ([]Clip, error) {
	if clipDuration <= 0 {
		clipDuration = 4
	}
	if clipDuration <= 0 || clipDuration > 60 {
		return nil, fmt.Errorf("round clip duration %d exceeds round budget 60", clipDuration)
	}
	out := make([]Clip, 0, len(rounds)*15)
	for _, r := range rounds {
		start, err := timestamp(r.TimestampStart)
		if err != nil {
			return nil, fmt.Errorf("round %v start: %w", r.Round, err)
		}
		end, err := timestamp(r.TimestampEnd)
		if err != nil {
			return nil, fmt.Errorf("round %v end: %w", r.Round, err)
		}
		if end <= start {
			return nil, fmt.Errorf("round %v has non-positive timestamp range", r.Round)
		}
		if end > start+60 {
			end = start + 60
		}
		label := label(r.Round)
		for at := start; at+clipDuration <= end; at += clipDuration {
			c := Clip{Title: label, Description: r.Description, StartSec: float64(at), EndSec: float64(at + clipDuration)}
			if n, ok := number(r.Round); ok {
				c.Round = n
			} else {
				c.Slug = label
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func timestamp(raw string) (int, error) {
	p := strings.Split(strings.TrimSpace(raw), ":")
	if len(p) != 3 {
		return 0, fmt.Errorf("invalid timestamp %q", raw)
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	s, e3 := strconv.Atoi(p[2])
	if e1 != nil || e2 != nil || e3 != nil || h < 0 || m < 0 || m > 59 || s < 0 || s > 59 {
		return 0, fmt.Errorf("invalid timestamp %q", raw)
	}
	return h*3600 + m*60 + s, nil
}

func number(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), n >= 1 && n == float64(int(n))
	case int:
		return n, n >= 1
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		return i, err == nil && i >= 1
	default:
		return 0, false
	}
}

func label(v any) string {
	if n, ok := number(v); ok {
		return fmt.Sprintf("Round %d", n)
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return "Round"
}

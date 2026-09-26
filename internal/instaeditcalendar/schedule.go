package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrEventCancelled = errors.New("InstaEdit calendar event was cancelled")

type ScheduleGate struct {
	Client       *Client
	PollInterval time.Duration
}

func (g ScheduleGate) WaitUntilDue(ctx context.Context, jobID string) error {
	if g.Client == nil {
		return nil
	}
	return g.Client.WaitUntilDue(ctx, jobID, g.PollInterval)
}

// WaitUntilDue re-reads scheduled_at before final delivery and while waiting.
// Reschedules therefore take effect immediately; cancellation is carried by
// the Job Master lease and also observed here on every refresh.
func (c *Client) WaitUntilDue(ctx context.Context, jobID string, poll time.Duration) error {
	if poll <= 0 {
		poll = 15 * time.Second
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		raw, err := c.GetEventByJobID(ctx, jobID)
		if errors.Is(err, ErrEventNotFound) {
			return nil
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read linked Calendar schedule: %w", err)
		}
		var event struct {
			ScheduledAt *time.Time `json:"scheduled_at"`
			Status      string     `json:"status"`
			Cancelled   bool       `json:"cancelled"`
		}
		if err := json.Unmarshal(raw, &event); err != nil {
			return fmt.Errorf("decode linked Calendar schedule: %w", err)
		}
		if event.Cancelled || event.Status == "CANCELLED" {
			return ErrEventCancelled
		}
		if event.ScheduledAt == nil || !time.Now().Before(*event.ScheduledAt) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

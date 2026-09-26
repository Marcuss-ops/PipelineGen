// Package scheduling reports worker-created events and per-kind progress to InstaEdit.
package scheduling

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL, apiKey string
	http            *http.Client
}

func NewClient(baseURL, apiKey string) (*Client, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || apiKey == "" {
		return nil, fmt.Errorf("INSTAEDIT_BASE_URL and INSTAEDIT_API_KEY are required")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("invalid InstaEdit base URL")
	}
	return &Client{baseURL: baseURL, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}, nil
}

type Event struct {
	EventKey    string     `json:"event_key"`
	Title       string     `json:"title"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	Kind        string     `json:"kind"`
	JobID       string     `json:"job_id,omitempty"`
}
type BatchRequest struct {
	Events []Event `json:"events"`
}
type Progress struct {
	Kind     string         `json:"kind"`
	Status   string         `json:"status"`
	Progress *int           `json:"progress,omitempty"`
	Phase    string         `json:"phase,omitempty"`
	Snapshot map[string]any `json:"snapshot,omitempty"`
	Error    *WorkerError   `json:"error,omitempty"`
}

type WorkerError struct {
	ErrorCode  string `json:"error_code"`
	Reason     string `json:"reason"`
	OutputTail string `json:"output_tail,omitempty"`
}

func (c *Client) CreateEvents(ctx context.Context, req BatchRequest) (json.RawMessage, error) {
	if len(req.Events) < 1 || len(req.Events) > 20 {
		return nil, fmt.Errorf("events batch must contain between 1 and 20 events")
	}
	return c.doJSON(ctx, http.MethodPost, "/api/v1/agent/calendar/events/batch", req)
}
func (c *Client) UpdateProgress(ctx context.Context, eventKey string, update Progress) (json.RawMessage, error) {
	if strings.TrimSpace(eventKey) == "" {
		return nil, fmt.Errorf("event key is required")
	}
	return c.doJSON(ctx, http.MethodPatch, "/api/v1/agent/calendar/events/"+url.PathEscape(eventKey)+"/progress", update)
}

type EventUpdate struct {
	Title       *string `json:"title,omitempty"`
	ScheduledAt *string `json:"scheduled_at,omitempty"`
}

func (c *Client) UpdateEvent(ctx context.Context, eventKey string, update EventUpdate) (json.RawMessage, error) {
	if strings.TrimSpace(eventKey) == "" {
		return nil, fmt.Errorf("event key is required")
	}
	return c.doJSON(ctx, http.MethodPatch, "/api/v1/agent/calendar/events/"+url.PathEscape(eventKey), update)
}

func (c *Client) DeleteEvent(ctx context.Context, eventKey string) error {
	if strings.TrimSpace(eventKey) == "" {
		return fmt.Errorf("event key is required")
	}
	_, err := c.do(ctx, http.MethodDelete, "/api/v1/agent/calendar/events/"+url.PathEscape(eventKey), nil)
	return err
}
func (c *Client) doJSON(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	return c.do(ctx, method, path, data)
}

// do retries transient network, timeout, 429 and 5xx failures three times
// with exponential backoff and jitter. Calendar writes are idempotent by key.
func (c *Client) do(ctx context.Context, method, path string, data []byte) (json.RawMessage, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var body io.Reader
		if data != nil {
			body = bytes.NewReader(data)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		resp, err := c.http.Do(req)
		if err == nil {
			out, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
			resp.Body.Close()
			if readErr != nil {
				err = readErr
			} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return json.RawMessage(out), nil
			} else {
				lastErr = fmt.Errorf("InstaEdit returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(out)))
				if resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
					return nil, lastErr
				}
			}
		}
		if err != nil {
			lastErr = fmt.Errorf("call InstaEdit: %w", err)
		}
		if attempt < 2 {
			delay := time.Duration(200*(1<<attempt)+rand.Intn(150)) * time.Millisecond
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, fmt.Errorf("InstaEdit update failed after 3 attempts: %w", lastErr)
}

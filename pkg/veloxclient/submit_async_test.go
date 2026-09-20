// submit_async_test.go — pins the client side of the async-enqueue ACK.
//
// Sept 2026: the server's enqueue helper now returns the ADDITIVE
// {ok, message, job_id} body (platform/httpserver/transport EnqueueAsync),
// including POST /api/clips/process, which used to reply ACK-only. A client
// that reads job_id can bind to the job it just created instead of listing
// jobs and guessing; a client that only reads ok/message keeps working.
package veloxclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubmitAsync_DecodesAdditiveJobIDFromAck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// t.Errorf (not require) is the safe form inside a handler goroutine.
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/clips/process" {
			t.Errorf("path = %s, want /api/clips/process", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"message":"YouTube clip extraction job enqueued.","job_id":"job_1789_abc"}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")

	resp, err := c.SubmitAsync(context.Background(), "/api/clips/process", map[string]any{
		"url": "https://www.youtube.com/watch?v=abc12345678",
	}, "req-1")

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "job_1789_abc", resp.JobID,
		"the ACK job_id must reach the caller so the job can be polled by id")
}

// TestSubmitAsync_JobIDEmptyOnLegacyAck pins the additive contract: a
// pre-Sept-2026 server that replies ACK-only still decodes successfully,
// with an empty JobID (the caller falls back to listing jobs).
func TestSubmitAsync_JobIDEmptyOnLegacyAck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"message":"YouTube clip extraction job enqueued."}`))
	}))
	defer srv.Close()

	c := New(srv.URL, "test-token")

	resp, err := c.SubmitAsync(context.Background(), "/api/clips/process", map[string]any{"url": "x"}, "req-2")

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Empty(t, resp.JobID)
}

// TestSubmitAsync_DecodesJobObjectAck pins the OTHER observed ACK shape: a
// deployed build answered `job_id` with the full job object instead of its id.
//
// Live regression (2026-09-20): POST /api/clips/process returned
//
//	{"job_id":{"id":"job_1789910036360274691_b7eb4f16","type":…,"status":"QUEUED"}}
//
// and `velox submit` failed to decode it AFTER the job had been enqueued, so
// the only handle on the already-running work was discarded. The client must
// extract the id from either shape.
func TestSubmitAsync_DecodesJobObjectAck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":{"id":"job_1789910036360274691_b7eb4f16","type":"youtube_clip.extract","status":"QUEUED","priority":0}}`))
	}))
	defer srv.Close()

	resp, err := New(srv.URL, "test-token").SubmitAsync(context.Background(),
		"/api/clips/process", map[string]any{"url": "x"}, "req-3")

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, "job_1789910036360274691_b7eb4f16", resp.JobID,
		"the id inside the job object must reach the caller so the job can be polled")
}

// TestSubmitAsync_JobObjectAckWithoutIDIsNotAnError pins that a job object
// carrying no id degrades to an empty JobID (the documented legacy-ACK
// fallback) instead of failing the decode.
func TestSubmitAsync_JobObjectAckWithoutIDIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"job_id":{"status":"QUEUED"}}`))
	}))
	defer srv.Close()

	resp, err := New(srv.URL, "test-token").SubmitAsync(context.Background(),
		"/api/clips/process", map[string]any{"url": "x"}, "req-4")

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Empty(t, resp.JobID)
}

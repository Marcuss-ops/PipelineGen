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

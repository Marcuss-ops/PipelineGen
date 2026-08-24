package downloader

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMediaDownloader_Download_Success(t *testing.T) {
	wantBody := "hello media"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(wantBody))
	}))
	defer server.Close()

	dl := NewMediaDownloader(5 * time.Second)
	reader, err := dl.Download(context.Background(), server.URL)
	require.NoError(t, err)
	require.NotNil(t, reader)
	defer func() {
		require.NoError(t, reader.Close())
	}()

	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, wantBody, string(got))
}

func TestMediaDownloader_Download_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer server.Close()

	dl := NewMediaDownloader(5 * time.Second)
	reader, err := dl.Download(context.Background(), server.URL)
	require.Error(t, err)
	assert.Nil(t, reader)
	assert.Contains(t, err.Error(), "404")
	assert.Contains(t, err.Error(), "not found")
}

func TestMediaDownloader_Download_Timeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block until the client timeout cancels the request context.
		<-r.Context().Done()
	}))
	defer server.Close()

	dl := NewMediaDownloader(50 * time.Millisecond)
	reader, err := dl.Download(context.Background(), server.URL)
	require.Error(t, err)
	assert.Nil(t, reader)

	// net/http returns an error wrapping context.DeadlineExceeded on client timeout.
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected timeout error wrapping context.DeadlineExceeded, got: %v", err)
}

func TestMediaDownloader_Download_DefaultTimeout(t *testing.T) {
	dl := NewMediaDownloader(0)
	require.NotNil(t, dl)
	assert.Equal(t, 90*time.Second, dl.client.Timeout)
}

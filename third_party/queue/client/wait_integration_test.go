// Package client_test wires the real HTTP server, service and repository to
// the public client. It is an EXTERNAL test package on purpose: the queue's
// internal model imports the public client (the client owns the canonical wire
// types), so an in-package test could not import it without an import cycle.
package client_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/client"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/server"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
)

// TestWaitTerminalEndToEndWithRealQueue wires the real HTTP server and the real
// client together: the producer long-poll must be woken by the terminal
// transition instead of observing it at a polling tick.
func TestWaitTerminalEndToEndWithRealQueue(t *testing.T) {
	repo := memory.New(30*time.Second, 3)
	svc := service.New(repo)
	ts := httptest.NewServer(server.New(svc).Handler())
	defer ts.Close()

	if err := svc.Submit(context.Background(), model.Job{ID: "job-1"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.Claim(context.Background(), "w1"); err != nil {
		t.Fatal(err)
	}

	c := client.New(ts.URL)
	done := make(chan client.Job, 1)
	errCh := make(chan error, 1)
	go func() {
		job, err := c.WaitTerminal(context.Background(), "job-1")
		if err != nil {
			errCh <- err
			return
		}
		done <- job
	}()

	// Park the server-side long poll, then complete the job.
	time.Sleep(150 * time.Millisecond)
	start := time.Now()
	if err := svc.Complete(context.Background(), "job-1", "w1", model.Artifact{
		StorageKey: "abc", ArtifactHash: "abc", SizeBytes: 1, ContentType: "video/mp4",
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case job := <-done:
		if job.State != client.StateCompleted {
			t.Fatalf("state = %q, want completed", job.State)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Fatalf("completion wake took %s; expected event-driven wake", elapsed)
		}
	case err := <-errCh:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("WaitTerminal did not wake on completion")
	}
}

package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/repository/memory"
	"github.com/Marcuss-ops/RenderingGen/queue/internal/service"
)

func TestLongPollClaimWakesOnSubmit(t *testing.T) {
	repo := memory.New(30*time.Second, 3)
	svc := service.New(repo)
	srv := New(svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	result := make(chan int, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/jobs/claim", "application/json", bytes.NewBufferString(`{"worker":"w1","state":"pending","wait_ms":1000}`))
		if err != nil {
			result <- 0
			return
		}
		defer resp.Body.Close()
		result <- resp.StatusCode
	}()

	// Give the claim enough time to enter its wait path, then create work. The
	// response should be immediate rather than waiting for the one-second
	// timeout.
	time.Sleep(25 * time.Millisecond)
	start := time.Now()
	resp, err := http.Post(ts.URL+"/jobs", "application/json", bytes.NewBufferString(`{"id":"wake-job"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("submit: want 201, got %d", resp.StatusCode)
	}

	select {
	case status := <-result:
		if status != http.StatusOK {
			t.Fatalf("claim: want 200, got %d", status)
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("claim wake took %s; expected event-driven wake", elapsed)
		}
	case <-time.After(750 * time.Millisecond):
		t.Fatal("claim did not wake after submit")
	}
}

func TestNotifyStateWakesReplicaWaiter(t *testing.T) {
	repo := memory.New(30*time.Second, 3)
	svc := service.New(repo)
	srv := New(svc)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	result := make(chan int, 1)
	go func() {
		resp, err := http.Post(ts.URL+"/jobs/claim", "application/json", bytes.NewBufferString(`{"worker":"w2","state":"pending","wait_ms":1000}`))
		if err != nil {
			result <- 0
			return
		}
		defer resp.Body.Close()
		result <- resp.StatusCode
	}()

	time.Sleep(25 * time.Millisecond)
	if err := svc.Submit(context.Background(), model.Job{ID: "replica-job"}); err != nil {
		t.Fatal(err)
	}
	// Simulate the PostgreSQL LISTEN loop in another queue replica: the DB
	// change already exists, NotifyState only wakes the local HTTP waiter.
	srv.NotifyState(model.StatePending)

	ctx, cancel := context.WithTimeout(context.Background(), 750*time.Millisecond)
	defer cancel()
	select {
	case status := <-result:
		if status != http.StatusOK {
			t.Fatalf("claim: want 200, got %d", status)
		}
	case <-ctx.Done():
		t.Fatal("replica waiter did not wake from external notification")
	}
}

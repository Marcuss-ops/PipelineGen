package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// TestServeDrainsInFlightRequestsOnShutdown pins the deploy behaviour the
// previous ListenAndServe + log.Fatal shape could not provide: the signal a
// rolling deploy sends must stop the listener from accepting new work, let the
// request already in flight finish, and only then return. Cutting that
// connection loses a producer's submit whose response carries the job id it
// needs, and leaves every long-poll claim waiter without an HTTP reply.
func TestServeDrainsInFlightRequestsOnShutdown(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "drained")
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, server, listener, 5*time.Second, func(string, ...any) {})
	}()

	type response struct {
		status int
		body   string
		err    error
	}
	inFlight := make(chan response, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/slow")
		if err != nil {
			inFlight <- response{err: err}
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		inFlight <- response{status: resp.StatusCode, body: string(body)}
	}()

	<-started
	// The deploy signal.
	cancel()

	// The drain must still be running while the handler is blocked: a serve
	// that returned here cut the request short instead of waiting for it.
	select {
	case err := <-done:
		t.Fatalf("serve returned before the in-flight request finished: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	close(release)
	got := <-inFlight
	if got.err != nil {
		t.Fatalf("in-flight request failed during shutdown: %v", got.err)
	}
	if got.status != http.StatusOK || got.body != "drained" {
		t.Fatalf("in-flight response = %d %q, want 200 %q", got.status, got.body, "drained")
	}
	if err := <-done; err != nil {
		t.Fatalf("serve: %v", err)
	}

	// The listener is closed once the drain completed.
	resp, err := http.Get("http://" + listener.Addr().String() + "/slow")
	if err == nil {
		resp.Body.Close()
		t.Fatal("server still accepts connections after shutdown")
	}
}

// TestServeReportsADrainThatMissedItsDeadline: a shutdown that could not finish
// inside the deadline is an incomplete drain, and the caller has to be able to
// exit non-zero about it. Treating it as a clean stop is how a hung request
// becomes a silent SIGKILL later.
func TestServeReportsADrainThatMissedItsDeadline(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/stuck", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: mux}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, server, listener, 50*time.Millisecond, func(string, ...any) {})
	}()
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/stuck")
		if err == nil {
			resp.Body.Close()
		}
	}()

	<-started
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a drain that missed its deadline must be reported, not treated as a clean stop")
		}
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("serve did not return after the drain deadline")
	}
	// Let the stuck handler finish so the test does not leak it.
	close(release)
}

// TestValidateCapacityRejectsImpossibleConfigurations is the boot-time guard:
// every one of these values would otherwise be accepted and quietly turn into a
// different pool (or a process that never drains) than the operator configured.
func TestValidateCapacityRejectsImpossibleConfigurations(t *testing.T) {
	call := func(maxOpen, maxIdle int, lifetime, idleTime, connect, shutdown time.Duration) error {
		return validateCapacity(maxOpen, maxIdle, lifetime, idleTime, connect, shutdown)
	}

	// The shipped defaults must be accepted, or the queue cannot boot.
	if err := call(25, 10, time.Hour, 5*time.Minute, 30*time.Second, 25*time.Second); err != nil {
		t.Fatalf("the default capacity configuration was rejected: %v", err)
	}

	cases := []struct {
		name string
		call func() error
	}{
		{"open pool below one", func() error { return call(0, 0, time.Hour, time.Minute, time.Second, time.Second) }},
		{"negative idle pool", func() error { return call(25, -1, time.Hour, time.Minute, time.Second, time.Second) }},
		{"idle pool above open pool", func() error { return call(4, 5, time.Hour, time.Minute, time.Second, time.Second) }},
		{"zero connection lifetime", func() error { return call(25, 10, 0, time.Minute, time.Second, time.Second) }},
		{"zero idle time", func() error { return call(25, 10, time.Hour, 0, time.Second, time.Second) }},
		{"zero connect timeout", func() error { return call(25, 10, time.Hour, time.Minute, 0, time.Second) }},
		{"zero shutdown timeout", func() error { return call(25, 10, time.Hour, time.Minute, time.Second, 0) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("configuration accepted; want a startup error naming the flag")
			}
		})
	}
}

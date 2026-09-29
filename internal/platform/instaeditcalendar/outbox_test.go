package scheduling

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestCardlessJobDoesNotBlockOtherJobsReports is the head-of-line-block pin.
//
// A job with no linked Calendar card is the NORMAL case for every kind the
// calendar does not carry (and for a card an operator has not created yet).
// Its spooled reports must be RETAINED for a later tick (the card may still
// appear) without withholding the reports of every other job behind them: the
// spool is a single ordered directory, so any early abort there is a
// repo-wide calendar blackout, not a per-job delay.
func TestCardlessJobDoesNotBlockOtherJobsReports(t *testing.T) {
	var delivered []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/calendar/events/by-job/job-cardless":
			http.NotFound(w, r)
		case "/api/v1/agent/calendar/events/by-job/job-linked":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_key":"event-linked"}`))
		case "/api/v1/agent/calendar/events/event-linked/progress":
			delivered = append(delivered, "event-linked")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.http = srv.Client()
	dir := t.TempDir()
	reporter, err := NewReporter(client, dir, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	// Oldest record first: the cardless job must not starve the linked one.
	if err := reporter.EnqueueJobProgress("job-cardless", Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := reporter.EnqueueJobProgress("job-linked", Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	reporter.drain(context.Background())
	if len(delivered) != 1 {
		t.Fatalf("linked job reports delivered=%v, want exactly 1", delivered)
	}
	pending, dead := spoolCounts(t, dir)
	if pending != 1 || dead != 0 {
		t.Fatalf("spool pending=%d dead=%d, want the cardless record retained and nothing dead-lettered", pending, dead)
	}
}

// TestPermanentRejectionIsDeadLetteredNotWedged pins the second half of the
// same contract: a record the calendar will NEVER accept (a 4xx that is not a
// not-found) must leave the spool instead of being retried forever at the head
// of the queue, and must not stop the records behind it.
func TestPermanentRejectionIsDeadLetteredNotWedged(t *testing.T) {
	var delivered []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/calendar/events/by-job/job-rejected":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_key":"event-rejected"}`))
		case "/api/v1/agent/calendar/events/by-job/job-ok":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_key":"event-ok"}`))
		case "/api/v1/agent/calendar/events/event-rejected/progress":
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":"unsupported kind"}`))
		case "/api/v1/agent/calendar/events/event-ok/progress":
			delivered = append(delivered, "event-ok")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.http = srv.Client()
	dir := t.TempDir()
	reporter, err := NewReporter(client, dir, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.EnqueueJobProgress("job-rejected", Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := reporter.EnqueueJobProgress("job-ok", Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	reporter.drain(context.Background())
	if len(delivered) != 1 {
		t.Fatalf("deliveries=%v, want the record behind the rejected one to drain", delivered)
	}
	pending, dead := spoolCounts(t, dir)
	if pending != 0 || dead != 1 {
		t.Fatalf("spool pending=%d dead=%d, want the rejected record dead-lettered for the operator", pending, dead)
	}
}

// TestRepeatedReportsForOneJobResolveTheCardOnce pins the resolution cache:
// every heartbeat of one job would otherwise pay a by-job lookup per record.
func TestRepeatedReportsForOneJobResolveTheCardOnce(t *testing.T) {
	var lookups, updates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/calendar/events/by-job/job-42":
			lookups.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_key":"event-42"}`))
		case "/api/v1/agent/calendar/events/event-42/progress":
			updates.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.http = srv.Client()
	dir := t.TempDir()
	reporter, err := NewReporter(client, dir, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := reporter.EnqueueJobProgress("job-42", Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	reporter.drain(context.Background())
	if lookups.Load() != 1 || updates.Load() != 3 {
		t.Fatalf("lookups=%d updates=%d, want 1 lookup for 3 reports of one job", lookups.Load(), updates.Load())
	}
	if pending, dead := spoolCounts(t, dir); pending != 0 || dead != 0 {
		t.Fatalf("pending=%d dead=%d, want an empty spool", pending, dead)
	}
}

// TestSpoolBoundDeadLettersTheOldestRecords pins the disk bound: a cardless job
// heart-beating for hours must not fill the volume, and the reports that
// survive are the newest ones.
func TestSpoolBoundDeadLettersTheOldestRecords(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.http = srv.Client()
	dir := t.TempDir()
	reporter, err := NewReporter(client, dir, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	reporter.maxSpoolEntries = 2
	for i := 0; i < 5; i++ {
		if err := reporter.EnqueueJobProgress("job-cardless", Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond)
	}
	reporter.drain(context.Background())
	pending, dead := spoolCounts(t, dir)
	if pending != 2 || dead != 3 {
		t.Fatalf("pending=%d dead=%d, want the bound (2) kept and the oldest 3 dead-lettered", pending, dead)
	}
}

// TestTransientFailureStopsThePassAndKeepsTheRecord pins the offline contract:
// a 5xx keeps the record (nothing is lost) and the pass stops instead of
// hammering an endpoint the client already retried.
func TestTransientFailureStopsThePassAndKeepsTheRecord(t *testing.T) {
	var updates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/calendar/events/by-job/job-42":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_key":"event-42"}`))
		case "/api/v1/agent/calendar/events/event-42/progress":
			updates.Add(1)
			w.WriteHeader(http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.http = srv.Client()
	dir := t.TempDir()
	reporter, err := NewReporter(client, dir, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.EnqueueJobProgress("job-42", Progress{Kind: "video.create", Status: "RUNNING"}); err != nil {
		t.Fatal(err)
	}
	reporter.drain(context.Background())
	pending, dead := spoolCounts(t, dir)
	if pending != 1 || dead != 0 {
		t.Fatalf("pending=%d dead=%d, want the record retained for the next tick", pending, dead)
	}
	if updates.Load() == 0 {
		t.Fatal("the client never attempted delivery")
	}
}

// spoolCounts reports (pending .json, dead-lettered .json.dead) spool entries.
func spoolCounts(t *testing.T, dir string) (int, int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	pending, dead := 0, 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch filepath.Ext(e.Name()) {
		case ".json":
			pending++
		case ".dead":
			dead++
		}
	}
	return pending, dead
}

func TestReporterDurableJobProgressDrain(t *testing.T) {
	var lookups, updates atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/calendar/events/by-job/job-42":
			lookups.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_key":"event-42"}`))
		case "/api/v1/agent/calendar/events/event-42/progress":
			var body Progress
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode progress: %v", err)
			}
			if body.Status != "RUNNING" {
				t.Errorf("status=%q", body.Status)
			}
			updates.Add(1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client, err := NewClient(srv.URL, "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	client.http = srv.Client()
	dir := t.TempDir()
	reporter, err := NewReporter(client, dir, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err := reporter.EnqueueJobProgress("job-42", Progress{Kind: "script.generate", Status: "RUNNING", Phase: "heartbeat"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("spooled entries=%d err=%v", len(entries), err)
	}
	info, err := os.Stat(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("spool mode=%o", info.Mode().Perm())
	}
	reporter.drain(context.Background())
	entries, err = os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("outbox still has %d items", len(entries))
	}
	if lookups.Load() != 1 || updates.Load() != 1 {
		t.Fatalf("lookup=%d update=%d", lookups.Load(), updates.Load())
	}
}

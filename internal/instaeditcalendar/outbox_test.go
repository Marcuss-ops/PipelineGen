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

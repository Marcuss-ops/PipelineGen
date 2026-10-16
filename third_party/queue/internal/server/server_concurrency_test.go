package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// claimOnce POSTs a claim to the queue for worker and returns the response
// status and (when a job was claimed) its ID. It does not use the shared post
// helper because it is called from concurrent goroutines.
func claimOnce(baseURL, worker string) (status int, id string, err error) {
	resp, err := http.Post(baseURL+"/jobs/claim", "application/json",
		strings.NewReader(`{"worker":"`+worker+`"}`))
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, "", fmt.Errorf("claim status %d", resp.StatusCode)
	}
	var claimed struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&claimed); err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, claimed.ID, nil
}

// TestServerConcurrentClaimExclusive drives many workers through the HTTP
// claim endpoint at once and asserts no job is ever returned to two workers.
func TestServerConcurrentClaimExclusive(t *testing.T) {
	ts := newServer(t)

	const jobs = 100
	for i := 0; i < jobs; i++ {
		if resp := post(t, ts.URL+"/jobs", fmt.Sprintf(`{"id":"job-%03d"}`, i)); resp.StatusCode != http.StatusCreated {
			t.Fatalf("submit %d: got %d", i, resp.StatusCode)
		}
	}

	var mu sync.Mutex
	seen := make(map[string]string) // jobID -> workerID
	var wg sync.WaitGroup
	const workers = 20
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				status, id, err := claimOnce(ts.URL, worker)
				if err != nil {
					t.Errorf("claim(%s): %v", worker, err)
					return
				}
				if status == http.StatusNoContent {
					return
				}
				mu.Lock()
				if prev, dup := seen[id]; dup {
					t.Errorf("job %s claimed by both %s and %s", id, prev, worker)
				}
				seen[id] = worker
				mu.Unlock()
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(seen) != jobs {
		t.Fatalf("want %d distinct claims, got %d", jobs, len(seen))
	}
}

// TestServerConcurrentRenew hammers the renew endpoint from many goroutines on
// the same owned job and asserts every request succeeds and the lease stays
// fresh (the job remains running and is not requeued).
func TestServerConcurrentRenew(t *testing.T) {
	ts := newServer(t)
	post(t, ts.URL+"/jobs", `{"id":"job-1"}`)
	if resp := post(t, ts.URL+"/jobs/claim", `{"worker":"w1"}`); resp.StatusCode != http.StatusOK {
		t.Fatalf("claim: got %d", resp.StatusCode)
	}

	const goroutines = 20
	const renews = 50
	var wg sync.WaitGroup
	errs := make(chan error, goroutines*renews)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < renews; i++ {
				resp, err := http.Post(ts.URL+"/jobs/job-1/renew", "application/json",
					strings.NewReader(`{"worker":"w1"}`))
				if err != nil {
					errs <- err
					return
				}
				status := resp.StatusCode
				resp.Body.Close()
				if status != http.StatusNoContent {
					errs <- fmt.Errorf("renew status %d", status)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("renew: %v", err)
	}

	// The job must still be running (fresh lease), so the queue is empty.
	if resp := post(t, ts.URL+"/jobs/claim", `{"worker":"w2"}`); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 (job still running), got %d", resp.StatusCode)
	}
}

// TestServerConcurrentClaimAndComplete runs the full claim→complete lifecycle
// concurrently across many workers and asserts every job is claimed exactly
// once and reaches a completed state.
func TestServerConcurrentClaimAndComplete(t *testing.T) {
	ts := newServer(t)

	const jobs = 50
	for i := 0; i < jobs; i++ {
		if resp := post(t, ts.URL+"/jobs", fmt.Sprintf(`{"id":"job-%03d"}`, i)); resp.StatusCode != http.StatusCreated {
			t.Fatalf("submit %d: got %d", i, resp.StatusCode)
		}
	}

	var mu sync.Mutex
	seen := make(map[string]bool)
	var wg sync.WaitGroup
	const workers = 10
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker string) {
			defer wg.Done()
			for {
				status, id, err := claimOnce(ts.URL, worker)
				if err != nil {
					t.Errorf("claim(%s): %v", worker, err)
					return
				}
				if status == http.StatusNoContent {
					return
				}
				mu.Lock()
				dup := seen[id]
				seen[id] = true
				mu.Unlock()
				if dup {
					t.Errorf("job %s claimed twice", id)
				}

				resp, err := http.Post(ts.URL+"/jobs/"+id+"/complete", "application/json",
					strings.NewReader(fmt.Sprintf(`{"worker":%q,"data":{"storage_key":"sha-%s","artifact_hash":"sha-%s","size_bytes":1,"content_type":"video/mp4"}}`, worker, id, id)))
				if err != nil {
					t.Errorf("complete(%s): %v", worker, err)
					return
				}
				status = resp.StatusCode
				resp.Body.Close()
				if status != http.StatusNoContent {
					t.Errorf("complete(%s): status %d", worker, status)
				}
			}
		}(fmt.Sprintf("w%d", w))
	}
	wg.Wait()

	if len(seen) != jobs {
		t.Fatalf("want %d distinct claims, got %d", jobs, len(seen))
	}

	resp, err := http.Get(ts.URL + "/jobs/depth")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var stats struct {
		Pending   int `json:"pending"`
		Running   int `json:"running"`
		Completed int `json:"completed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatal(err)
	}
	if stats.Completed != jobs || stats.Pending != 0 || stats.Running != 0 {
		t.Fatalf("want %d completed / 0 pending / 0 running, got %+v", jobs, stats)
	}
}

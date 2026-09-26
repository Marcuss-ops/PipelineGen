package scheduling

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Reporter durably spools progress updates before returning to the renderer.
// Run drains the directory in order; transient delivery failures leave the
// item on disk for a later tick or process restart.
type Reporter struct {
	client   *Client
	dir      string
	interval time.Duration
}

func NewReporter(client *Client, directory string, interval time.Duration) (*Reporter, error) {
	if client == nil {
		return nil, fmt.Errorf("calendar client is required")
	}
	if directory == "" {
		return nil, fmt.Errorf("calendar outbox directory is required")
	}
	if interval <= 0 {
		interval = 2 * time.Second
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create calendar outbox: %w", err)
	}
	return &Reporter{client: client, dir: directory, interval: interval}, nil
}

type progressEnvelope struct {
	EventKey string   `json:"event_key,omitempty"`
	JobID    string   `json:"job_id,omitempty"`
	Progress Progress `json:"progress"`
}

// EnqueueProgress durably stores an update and never waits for an HTTP request.
func (r *Reporter) EnqueueProgress(eventKey string, update Progress) error {
	if eventKey == "" {
		return fmt.Errorf("event key is required")
	}
	data, err := json.Marshal(progressEnvelope{EventKey: eventKey, Progress: update})
	if err != nil {
		return fmt.Errorf("encode calendar progress: %w", err)
	}
	nonce := make([]byte, 8)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), hex.EncodeToString(nonce))
	tmp, err := os.CreateTemp(r.dir, ".pending-*.tmp")
	if err != nil {
		return fmt.Errorf("create calendar outbox record: %w", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("persist calendar progress: %w", err)
	}
	if err = os.Rename(tmpName, filepath.Join(r.dir, name)); err != nil {
		return fmt.Errorf("publish calendar progress: %w", err)
	}
	if d, e := os.Open(r.dir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	ok = true
	return nil
}

// EnqueueJobProgress stores a report by Job Master ID; the drain resolves the
// associated Calendar card later, allowing the card to be created after enqueue.
func (r *Reporter) EnqueueJobProgress(jobID string, update Progress) error {
	if jobID == "" {
		return fmt.Errorf("job id is required")
	}
	data, err := json.Marshal(progressEnvelope{JobID: jobID, Progress: update})
	if err != nil {
		return err
	}
	nonce := make([]byte, 8)
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s.json", time.Now().UnixNano(), hex.EncodeToString(nonce))
	tmp, err := os.CreateTemp(r.dir, ".pending-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err = tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmpName, filepath.Join(r.dir, name)); err != nil {
		return err
	}
	if d, e := os.Open(r.dir); e == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	ok = true
	return nil
}

// Run drains queued records until ctx is cancelled. A failed report is retained
// and retried on subsequent intervals without holding up video work.
func (r *Reporter) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (r *Reporter) drain(ctx context.Context) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".json" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}
		path := filepath.Join(r.dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var item progressEnvelope
		if json.Unmarshal(data, &item) != nil || (item.EventKey == "" && item.JobID == "") {
			_ = os.Rename(path, path+".dead")
			continue
		}
		eventKey := item.EventKey
		if eventKey == "" {
			raw, err := r.client.GetEventByJobID(ctx, item.JobID)
			if err != nil {
				return
			}
			var linked struct {
				EventKey string `json:"event_key"`
			}
			if json.Unmarshal(raw, &linked) != nil || linked.EventKey == "" {
				return
			}
			eventKey = linked.EventKey
		}
		if _, err := r.client.UpdateProgress(ctx, eventKey, item.Progress); err != nil {
			return
		}
		_ = os.Remove(path)
	}
}

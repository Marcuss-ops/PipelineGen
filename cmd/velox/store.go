package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Marcuss-ops/PipelineGen/pkg/atomicwrite"
)

// jobRecord is one persisted submission. It carries everything a later command
// needs: the endpoint, the deterministic Idempotency-Key (so `velox replay`
// reuses it instead of inventing a new one) and the payload path (so replay can
// re-read the exact bytes).
type jobRecord struct {
	JobID          string    `json:"job_id"`
	Endpoint       string    `json:"endpoint"`
	Project        string    `json:"project"`
	IdempotencyKey string    `json:"idempotency_key"`
	PayloadPath    string    `json:"payload_path"`
	PayloadSHA256  string    `json:"payload_sha256"`
	CreatedAt      time.Time `json:"created_at"`
	LastStatus     string    `json:"last_status,omitempty"`
}

// storeFile is the on-disk document. A version field lets a future format
// change be detected rather than silently misread.
type storeFile struct {
	Version int                  `json:"version"`
	Jobs    map[string]jobRecord `json:"jobs"`
}

const storeVersion = 1

// Store is a tiny job-id registry backed by one atomically-written JSON file.
// Job ids used to live in /tmp and expire; a stable path under ~/.velox does
// not, and the atomic writer means a crash cannot leave it truncated.
type Store struct {
	path string
	jobs map[string]jobRecord
}

// OpenStore loads (or initialises) the store at path.
func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, jobs: map[string]jobRecord{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read job store %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var sf storeFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return nil, fmt.Errorf("parse job store %s: %w", path, err)
	}
	if sf.Version != storeVersion {
		return nil, fmt.Errorf("job store %s has unsupported version %d", path, sf.Version)
	}
	if sf.Jobs != nil {
		s.jobs = sf.Jobs
	}
	return s, nil
}

// Put records a job and persists the store atomically.
func (s *Store) Put(rec jobRecord) error {
	s.jobs[rec.JobID] = rec
	return s.Save()
}

// UpdateStatus refreshes the last-seen status of an existing job. Unknown job
// ids are ignored (the store is a convenience, not an authority).
func (s *Store) UpdateStatus(jobID, status string) error {
	rec, ok := s.jobs[jobID]
	if !ok {
		return nil
	}
	rec.LastStatus = status
	s.jobs[jobID] = rec
	return s.Save()
}

// Get returns the record for a job id.
func (s *Store) Get(jobID string) (jobRecord, bool) {
	rec, ok := s.jobs[jobID]
	return rec, ok
}

// List returns the records sorted by creation time (newest first).
func (s *Store) List() []jobRecord {
	out := make([]jobRecord, 0, len(s.jobs))
	for _, rec := range s.jobs {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// Save writes the store atomically (temp + fsync + rename). Directory mode is
// 0700 and file mode 0600: the store names internal job ids, not secrets, but
// there is no reason to make it world-readable.
func (s *Store) Save() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create job store dir: %w", err)
	}
	data, err := json.MarshalIndent(storeFile{Version: storeVersion, Jobs: s.jobs}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode job store: %w", err)
	}
	data = append(data, '\n')
	if err := atomicwrite.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write job store %s: %w", s.path, err)
	}
	return nil
}

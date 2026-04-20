package clipsearch

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type ClipJobStatus string

const (
	ClipJobStatusQueued     ClipJobStatus = "queued"
	ClipJobStatusSearched   ClipJobStatus = "searched"
	ClipJobStatusDownloaded ClipJobStatus = "downloaded"
	ClipJobStatusProcessed  ClipJobStatus = "processed"
	ClipJobStatusUploaded   ClipJobStatus = "uploaded"
	ClipJobStatusDone       ClipJobStatus = "done"
	ClipJobStatusFailed     ClipJobStatus = "failed"
)

type ClipJobCheckpoint struct {
	JobID      string        `json:"job_id"`
	Keyword    string        `json:"keyword"`
	Status     ClipJobStatus `json:"status"`
	Attempts   int           `json:"attempts"`
	LastError  string        `json:"last_error,omitempty"`
	DriveID    string        `json:"drive_id,omitempty"`
	DriveURL   string        `json:"drive_url,omitempty"`
	Filename   string        `json:"filename,omitempty"`
	FolderPath string        `json:"folder_path,omitempty"`
	UpdatedAt  time.Time     `json:"updated_at"`
	CreatedAt  time.Time     `json:"created_at"`
}

func (c ClipJobCheckpoint) IsTerminal() bool {
	return c.Status == ClipJobStatusDone || c.Status == ClipJobStatusFailed
}

type clipJobCheckpointFile struct {
	Version   int                 `json:"version"`
	UpdatedAt time.Time           `json:"updated_at"`
	Jobs      []ClipJobCheckpoint `json:"jobs"`
}

type ClipJobCheckpointStore struct {
	path string
	mu   sync.RWMutex
	jobs map[string]ClipJobCheckpoint
}

func OpenClipJobCheckpointStore(path string) (*ClipJobCheckpointStore, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("checkpoint path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("create checkpoint dir: %w", err)
	}
	s := &ClipJobCheckpointStore{
		path: path,
		jobs: make(map[string]ClipJobCheckpoint),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ClipJobCheckpointStore) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read checkpoint file: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	var file clipJobCheckpointFile
	if err := json.Unmarshal(data, &file); err != nil {
		return fmt.Errorf("decode checkpoint file: %w", err)
	}
	s.jobs = make(map[string]ClipJobCheckpoint, len(file.Jobs))
	for _, job := range file.Jobs {
		if strings.TrimSpace(job.JobID) == "" {
			continue
		}
		s.jobs[job.JobID] = job
	}
	return nil
}

func (s *ClipJobCheckpointStore) SaveOrUpdate(job ClipJobCheckpoint) error {
	if strings.TrimSpace(job.JobID) == "" {
		return fmt.Errorf("job_id required")
	}
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.jobs[job.JobID]
	if !ok {
		if job.CreatedAt.IsZero() {
			job.CreatedAt = now
		}
		if job.Attempts <= 0 {
			job.Attempts = 1
		}
	} else {
		if job.CreatedAt.IsZero() {
			job.CreatedAt = existing.CreatedAt
		}
		if job.Attempts < existing.Attempts {
			job.Attempts = existing.Attempts
		}
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	s.jobs[job.JobID] = job
	return s.saveLocked()
}

func (s *ClipJobCheckpointStore) Transition(jobID string, status ClipJobStatus, errMsg string, result *SearchResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	job, ok := s.jobs[jobID]
	if !ok {
		return fmt.Errorf("job checkpoint not found: %s", jobID)
	}
	job.Status = status
	job.UpdatedAt = time.Now().UTC()
	job.LastError = strings.TrimSpace(errMsg)
	if result != nil {
		job.DriveID = result.DriveID
		job.DriveURL = result.DriveURL
		job.Filename = result.Filename
		job.FolderPath = result.Folder
	}
	s.jobs[jobID] = job
	return s.saveLocked()
}

func (s *ClipJobCheckpointStore) Get(jobID string) (ClipJobCheckpoint, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	job, ok := s.jobs[jobID]
	return job, ok
}

func (s *ClipJobCheckpointStore) GetLatestByKeyword(keyword string) (ClipJobCheckpoint, bool) {
	kw := strings.TrimSpace(strings.ToLower(keyword))
	if kw == "" {
		return ClipJobCheckpoint{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	var found ClipJobCheckpoint
	foundAny := false
	for _, job := range s.jobs {
		if strings.ToLower(strings.TrimSpace(job.Keyword)) != kw {
			continue
		}
		if !foundAny || job.UpdatedAt.After(found.UpdatedAt) {
			found = job
			foundAny = true
		}
	}
	return found, foundAny
}

func (s *ClipJobCheckpointStore) List() []ClipJobCheckpoint {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]ClipJobCheckpoint, 0, len(s.jobs))
	for _, job := range s.jobs {
		out = append(out, job)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	return out
}

func (s *ClipJobCheckpointStore) saveLocked() error {
	jobs := make([]ClipJobCheckpoint, 0, len(s.jobs))
	for _, job := range s.jobs {
		jobs = append(jobs, job)
	}
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].UpdatedAt.After(jobs[j].UpdatedAt)
	})
	payload := clipJobCheckpointFile{
		Version:   1,
		UpdatedAt: time.Now().UTC(),
		Jobs:      jobs,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint file: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write checkpoint temp file: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replace checkpoint file: %w", err)
	}
	return nil
}

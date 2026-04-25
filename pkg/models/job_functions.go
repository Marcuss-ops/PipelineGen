package models

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Clone crea una copia profonda del job
func (j *Job) Clone() *Job {
	data, _ := json.Marshal(j)
	var clone Job
	json.Unmarshal(data, &clone)
	return &clone
}

// IsTerminal restituisce true se lo stato è terminale
func (s JobStatus) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// CanRetry restituisce true se il job può essere riprovato
func (j *Job) CanRetry() bool {
	return j.Retries < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusCancelled)
}

// NewJob crea un nuovo job con valori di default
func NewJob(jobType JobType, payload map[string]interface{}) *Job {
	now := time.Now()
	return &Job{
		ID:         generateID(),
		Type:       jobType,
		Status:     StatusPending,
		Priority:   0,
		CreatedAt:  now,
		UpdatedAt:  now,
		Payload:    payload,
		Retries:    0,
		MaxRetries: 3,
	}
}

// NewJobWithProject crea un nuovo job con project e video name
func NewJobWithProject(jobType JobType, project, videoName string, payload map[string]interface{}) *Job {
	job := NewJob(jobType, payload)
	job.Project = project
	job.VideoName = videoName
	return job
}

// generateID genera un ID univoco per il job
func generateID() string {
	return fmt.Sprintf("job_%d_%s", time.Now().UnixNano(), randomString(8))
}

// randomString genera una stringa casuale crittografica di lunghezza n
func randomString(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// Fallback: should never happen, but avoid panic
		return fmt.Sprintf("%0*x", n, time.Now().UnixNano())
	}
	return hex.EncodeToString(b)[:n]
}
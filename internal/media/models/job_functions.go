package models

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

// Payload helpers for type-safe unmarshaling

// parsePayload is a generic helper to unmarshal job payload into a typed struct.
func parsePayload[T any](j *Job, label string) (*T, error) {
	var p T
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid %s payload: %w", label, err)
	}
	return &p, nil
}

// ParseArtlistRunPayload estrae il payload tipizzato per media.artlist
func (j *Job) ParseArtlistRunPayload() (*ArtlistRunPayload, error) {
	return parsePayload[ArtlistRunPayload](j, "artlist run")
}

// ParseVoiceoverPayload estrae il payload tipizzato per voiceover
func (j *Job) ParseVoiceoverPayload() (*VoiceoverPayload, error) {
	return parsePayload[VoiceoverPayload](j, "voiceover")
}

// ParseScriptGenPayload estrae il payload tipizzato per script_generation
func (j *Job) ParseScriptGenPayload() (*ScriptGenPayload, error) {
	return parsePayload[ScriptGenPayload](j, "script gen")
}

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
	return j.RetryCount < j.MaxRetries && (j.Status == StatusFailed || j.Status == StatusCancelled)
}

// NewJob crea un nuovo job con valori di default
func NewJob(jobType JobType, payload json.RawMessage) *Job {
	now := time.Now()
	return &Job{
		ID:         generateID(),
		Type:       jobType,
		Status:     StatusPending,
		Priority:   0,
		CreatedAt:  now,
		UpdatedAt:  now,
		Payload:    payload,
		RetryCount: 0,
		MaxRetries: 3,
	}
}

// NewJobWithProject crea un nuovo job con project e video name
func NewJobWithProject(jobType JobType, project, videoName string, payload json.RawMessage) *Job {
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

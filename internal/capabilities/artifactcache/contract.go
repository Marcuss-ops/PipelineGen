// Package artifactcache defines the application-facing contract for
// derived artifacts backed by CAS. Cache state is disposable: canonical
// media state remains in the registry and immutable bytes remain in CAS.
package artifactcache

import (
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"encoding/json"
	"errors"
	"io"
	"time"
)

var (
	ErrInvalidKey = errors.New("artifact cache: invalid key")
	ErrNotWired   = errors.New("artifact cache: not wired")
	ErrLeaseLost  = errors.New("artifact cache: lease lost")
	// ErrLeaseBusy is returned when an abandoned/in-flight builder has not
	// released its lease within the caller's bounded wait window. Callers may
	// fall back to recomputing the artifact without blocking a whole batch.
	ErrLeaseBusy = errors.New("artifact cache: lease busy")
)

// Key identifies a deterministic computation. ParametersJSON must be the
// canonical JSON representation of operation parameters; changing any field
// produces a different cache address.
type Key struct {
	SourceSHA256     string
	Operation        string
	ParametersJSON   string
	ProcessorVersion string
}

func (k Key) Digest() (string, error) {
	if k.SourceSHA256 == "" || k.Operation == "" || k.ProcessorVersion == "" {
		return "", ErrInvalidKey
	}
	params := k.ParametersJSON
	if params == "" {
		params = "{}"
	}
	var value any
	if err := json.Unmarshal([]byte(params), &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		SourceSHA256     string `json:"source_sha256"`
		Operation        string `json:"operation"`
		Parameters       any    `json:"parameters"`
		ProcessorVersion string `json:"processor_version"`
	}{k.SourceSHA256, k.Operation, value, k.ProcessorVersion})
	if err != nil {
		return "", err
	}
	sum := digest.SHA256Bytes(canonical)
	return sum, nil
}

type Entry struct {
	CacheKey         string
	SourceSHA256     string
	Operation        string
	ParametersJSON   string
	ProcessorVersion string
	ArtifactSHA256   string
	SizeBytes        int64
	MIMEType         string
	Status           string
	CreatedAt        string
	LastAccessedAt   string
}

// Cache is the narrow port used by Whisper and media processing adapters.
// Lookup updates durable hit/miss counters. Store writes bytes to CAS and
// atomically records the mapping. Open reads immutable cached bytes.
type Cache interface {
	Lookup(context.Context, Key, int64) (*Entry, bool, error)
	Store(context.Context, Key, io.Reader, string, int64) (*Entry, error)
	Open(context.Context, *Entry) (io.ReadCloser, error)
	Invalidate(context.Context, Key) error
	Metrics(context.Context, string) (Metrics, error)
}

// MetricsRecorder is an optional narrow surface for source/download caches
// that need to persist hit/miss outcomes without owning artifact rows.
type MetricsRecorder interface {
	RecordOutcome(context.Context, string, bool, int64, int64) error
}

// Claim is a durable single-builder lease. An acquired claim must be
// completed by Store; an abandoned claim becomes reclaimable after its lease.
type Claim struct {
	Entry    *Entry
	LeaseID  string
	Acquired bool
}

type ClaimStore interface {
	// expectedWorkMS is the caller's known/estimated cost of the build. It is
	// persisted only when a completed artifact is reused, so avoided-work
	// metrics remain tied to the operation that actually owns the estimate.
	Claim(context.Context, Key, time.Duration, int64) (Claim, error)
}

// LeaseStore fences writes and releases abandoned builds. Implementations
// must accept a StoreWithLease only when leaseID still owns the BUILDING row.
type LeaseStore interface {
	StoreWithLease(context.Context, Key, string, io.Reader, string, int64) (*Entry, error)
	ReleaseClaim(context.Context, Key, string, string) error
}

type Metrics struct {
	Operation         string
	HitCount          int64
	MissCount         int64
	InvalidationCount int64
	AvoidedBytes      int64
	AvoidedWorkMS     int64
}

package media

import (
	"context"
	"errors"
)

// RenderBackend identifies the execution backend that rendered a sealed plan.
// It is a REPORTED identity, not a selector: the worker validates the
// artifact's certified backend against the Chronon contract and fails closed
// on any other value.
//
// The contract lives in the kernel because both the cliprender capability and
// the PostgreSQL cache adapter read it. Keeping it here is what lets the
// adapter implement the port without importing the capability (RA-11).
type RenderBackend string

// BackendChrononVulkan is the ONLY permitted render backend: the Chronon3d GPU
// compositor (NVDEC → CUDA/Vulkan surface → render graph → NVENC) executed by
// RenderingGen behind the shared queue boundary.
const BackendChrononVulkan RenderBackend = "chronon_vulkan"

// IsGPUBackend reports whether the backend executes on GPU hardware. Chronon
// is the only render backend and is GPU-native, so it is the single authority
// of "GPU-ness" for the worker's ExecutionSpec.RequireGPU gate.
func (b RenderBackend) IsGPUBackend() bool {
	return b == BackendChrononVulkan
}

// RenderCacheRecord is the durable cache value addressed by fingerprint.
type RenderCacheRecord struct {
	Fingerprint string
	AssetID     string
	StorageKey  string
	ArtifactURL string
	ContentType string
	SHA256      string
	SizeBytes   int64
	DurationSec float64
	Width       uint32
	Height      uint32
	FPSNum      uint32
	FPSDen      uint32
	Backend     RenderBackend
	// DeliveryStatus/DriveFileID come from the joined media_assets row. They
	// answer the second question a cache hit must not confuse with "the bytes
	// exist": is the Drive delivery complete? Empty means not instrumented.
	DeliveryStatus string
	DriveFileID    string
}

// RenderCache is the port for the deterministic render cache.
type RenderCache interface {
	Get(ctx context.Context, fingerprint string) (*RenderCacheRecord, error)
	Put(ctx context.Context, rec *RenderCacheRecord) error
}

// ErrCacheMiss is returned when no cache entry exists for a fingerprint.
var ErrCacheMiss = errors.New("clip.render cache: miss")

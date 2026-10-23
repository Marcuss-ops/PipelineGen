package scriptgeneration

import (
	"fmt"
	"testing"

	kernelasset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/pkg/retry"
)

// TestDeriveErrorCode_AssetContentMismatchIsStableAndNotGeneric pins the durable
// error code for a producer that offers bytes which do not hash to the address
// it named. Before the typed sentinel this failure landed in the generic
// "render" bucket as ENQUEUE_FAILED, so retry bots could not tell a broken
// producer contract apart from an infrastructure incident.
func TestDeriveErrorCode_AssetContentMismatchIsStableAndNotGeneric(t *testing.T) {
	const stage Stage = "RENDER_QUEUE"
	wrapped := fmt.Errorf("render overlay scene-2: %w (HTTP 422: digest mismatch)",
		kernelasset.ErrContentAddressMismatch)
	if got := deriveErrorCode(wrapped, stage); got != "ASSET_CONTENT_MISMATCH" {
		t.Fatalf("deriveErrorCode = %q, want ASSET_CONTENT_MISMATCH", got)
	}
}

func TestDeriveErrorCode_StillPrefersTypedPendingOverMismatch(t *testing.T) {
	const stage Stage = "RENDER_QUEUE"
	err := fmt.Errorf("%w: asset content address mismatch", ErrFinalJobPending)
	if got := deriveErrorCode(err, stage); got != "REMOTE_RENDER_PENDING" {
		t.Fatalf("deriveErrorCode = %q, want REMOTE_RENDER_PENDING", got)
	}
}

// TestDeriveErrorCode_ContentMismatchIsNotTransient guards the retry gate: the
// sentinel carries no RetryableError, so retry.IsTransient must stay false and
// the run must not be replayed against identical, still-invalid bytes.
func TestDeriveErrorCode_ContentMismatchIsNotTransient(t *testing.T) {
	wrapped := fmt.Errorf("asset deadbeef: %w", kernelasset.ErrContentAddressMismatch)
	if retry.IsTransient(wrapped) {
		t.Fatal("a content-address mismatch must not be retried as a transient failure")
	}
}

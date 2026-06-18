package providers

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/deliveries"
)

// S3Provider is a stub provider for future S3/R2/MinIO delivery.
type S3Provider struct{}

// NewS3Provider creates a stub S3 delivery provider.
func NewS3Provider() *S3Provider { return &S3Provider{} }

// Name returns "s3".
func (p *S3Provider) Name() string { return "s3" }

// Deliver returns ErrProviderNotConfigured until S3 support is built.
// Returns *deliveries.Result per the deliveries.Provider contract.
func (p *S3Provider) Deliver(ctx context.Context, artifact deliveries.ArtifactDescriptor, content deliveries.ArtifactReader, dest deliveries.DeliveryDestination) (*deliveries.Result, error) {
	return nil, ErrProviderNotConfigured
}

// ClassifyError returns FailurePermanent for the not-configured error.
func (p *S3Provider) ClassifyError(err error) deliveries.FailureClass {
	return deliveries.FailurePermanent
}

// ErrProviderNotConfigured is returned by stub providers.
var ErrProviderNotConfigured = fmt.Errorf("s3 provider not yet configured")

var _ deliveries.Provider = (*S3Provider)(nil)

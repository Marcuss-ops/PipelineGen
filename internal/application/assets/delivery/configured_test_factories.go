//go:build drivepolicypkgtest

package delivery

import platformdelivery "github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"

func NewDestinationRegistryWithPolicies(policies map[DestinationKey]DestinationPolicy) *DestinationRegistry {
	return platformdelivery.NewDestinationRegistryWithPolicies(policies)
}

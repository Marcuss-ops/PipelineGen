package job

import (
	"fmt"
	"strings"
	"time"
)

// SchedulingDescriptor is the small, immutable scheduling projection of a
// job. Its fields are persisted beside the jobs hot row so worker lanes can
// be selected without reading and decoding the request payload.
type SchedulingDescriptor struct {
	JobType       string    `json:"job_type"`
	ResourceClass string    `json:"resource_class,omitempty"`
	ExecutionLane string    `json:"execution_lane,omitempty"`
	AvailableAt   time.Time `json:"available_at"`
	Priority      int       `json:"priority"`
}

// Validate rejects a descriptor that can disagree with the canonical job row.
func (d SchedulingDescriptor) Validate() error {
	if strings.TrimSpace(d.JobType) == "" {
		return fmt.Errorf("scheduling descriptor job_type is required")
	}
	if d.Priority < 0 {
		return fmt.Errorf("scheduling descriptor priority must be non-negative")
	}
	if d.AvailableAt.IsZero() {
		return fmt.Errorf("scheduling descriptor available_at is required")
	}
	return nil
}

package queue

import (
	"strings"
	"testing"

	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

func TestValidateEnqueueRequest(t *testing.T) {
	cases := []struct {
		name string
		req  *job.EnqueueRequest
		want string
	}{
		{"nil", nil, "enqueue request is nil"},
		{"missing type", &job.EnqueueRequest{}, "job type is required"},
		{"negative priority", &job.EnqueueRequest{Type: "x", Priority: -1}, "priority must be non-negative"},
		{"invalid retries", &job.EnqueueRequest{Type: "x", MaxRetries: -2}, "max_retries must be >= -1"},
		{"valid", &job.EnqueueRequest{Type: "x"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEnqueueRequest(tc.req)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestGenerateJobID(t *testing.T) {
	first, second := GenerateJobID(), GenerateJobID()
	if first == second {
		t.Fatalf("generated duplicate IDs: %q", first)
	}
	if !strings.HasPrefix(first, "job_") || !strings.HasPrefix(second, "job_") {
		t.Fatalf("IDs must use job_ prefix: %q, %q", first, second)
	}
}
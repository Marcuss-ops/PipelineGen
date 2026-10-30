package scriptgeneration

import "testing"

// TestPublishFinalAudioNeeded pins that no run uses the legacy Drive audio
// publisher. Final-job audio is uploaded directly to Objectstore over HTTPS.
func TestPublishFinalAudioNeeded(t *testing.T) {
	cases := []struct {
		name     string
		finalJob bool
	}{
		{name: "final job uses HTTPS handoff", finalJob: true},
		{name: "overlay-only run skips", finalJob: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := GenerateRequest{FinalJob: tc.finalJob}
			if got := publishFinalAudioNeeded(req, nil); got {
				t.Fatalf("publishFinalAudioNeeded(final_job=%t) = true, want false", tc.finalJob)
			}
		})
	}
}

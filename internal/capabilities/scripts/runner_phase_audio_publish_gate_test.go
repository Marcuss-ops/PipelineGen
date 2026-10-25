package scriptgeneration

import "testing"

// TestPublishFinalAudioNeeded pins the surviving consumer contract of the
// master-audio Drive upload: only a run with a remote final job reads the
// published asset. Google Docs, the old consumer of a non-final master, was
// retired on 2026-10-25, so an overlay-only run must skip the upload.
func TestPublishFinalAudioNeeded(t *testing.T) {
	cases := []struct {
		name     string
		finalJob bool
		want     bool
	}{
		{name: "final job publishes", finalJob: true, want: true},
		{name: "overlay-only run skips", finalJob: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := GenerateRequest{FinalJob: tc.finalJob}
			if got := publishFinalAudioNeeded(req, nil); got != tc.want {
				t.Fatalf("publishFinalAudioNeeded(final_job=%t) = %t, want %t", tc.finalJob, got, tc.want)
			}
		})
	}
}

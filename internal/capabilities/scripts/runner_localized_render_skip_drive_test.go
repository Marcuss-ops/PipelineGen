package scriptgeneration

import (
	"context"
	"testing"
)

// TestLocalizedRenderSkipsDriveForFinalJobObjectStore pins the only condition
// under which a localized render may skip Drive: a final job (whose consumer
// reads the object store) AND a configured object-store public URL. Any other
// combination keeps the historical Drive publication.
func TestLocalizedRenderSkipsDriveForFinalJobObjectStore(t *testing.T) {
	cases := []struct {
		name        string
		finalJob    bool
		objectStore bool
		want        bool
	}{
		{name: "final job on object store", finalJob: true, objectStore: true, want: true},
		{name: "final job without object store keeps Drive", finalJob: true, objectStore: false, want: false},
		{name: "non-final job keeps Drive", finalJob: false, objectStore: true, want: false},
		{name: "plain run keeps Drive", finalJob: false, objectStore: false, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.objectStore {
				t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
			} else {
				t.Setenv("OBJECTSTORE_PUBLIC_URL", "")
			}
			if got := localizedRenderSkipsDrive(tc.finalJob); got != tc.want {
				t.Fatalf("localizedRenderSkipsDrive(finalJob=%v, objectStore=%v) = %v, want %v",
					tc.finalJob, tc.objectStore, got, tc.want)
			}
		})
	}
}

// TestPrepareLocalizedRenderInputMarksAFinalJobObjectStoreRender pins the
// wiring from both carriers to the skip flag: the explicit prepare argument
// (final_job_subtitles) and the input fact (the generic enqueue path, which
// passes false) must each mark a configured object-store render.
func TestPrepareLocalizedRenderInputMarksAFinalJobObjectStoreRender(t *testing.T) {
	t.Setenv("OBJECTSTORE_PUBLIC_URL", "https://objects.example")
	cases := []struct {
		name     string
		argument bool
		input    bool
	}{
		{name: "explicit final-job argument", argument: true, input: false},
		{name: "final-job input fact", argument: false, input: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &Runner{}
			in, err := r.prepareLocalizedRenderInput(context.Background(), LocalizedRenderInput{
				RunID: "run-1", SceneID: "scene-1", ClipID: "clip-1", Language: "es", FinalJob: tc.input,
			}, tc.argument)
			if err != nil {
				t.Fatalf("prepareLocalizedRenderInput: %v", err)
			}
			if !in.SkipDrivePublication {
				t.Fatal("a final-job render on the object store must skip Drive publication")
			}
		})
	}
}

package cleanup

import (
	"context"
	"errors"
	"testing"
)

type testReleaser struct {
	calls []string
}

func (r *testReleaser) Release(_ context.Context, resource Resource) error {
	r.calls = append(r.calls, resource.LocalPath)
	if resource.LocalPath == "bad" {
		return errors.New("release failed")
	}
	return nil
}

func TestReleaseAllContinuesAfterError(t *testing.T) {
	releaser := &testReleaser{}
	failures := ReleaseAll(context.Background(), releaser, []Resource{
		{SourceID: "bad-source", LocalPath: "bad"},
		{SourceID: "good-source", LocalPath: "good"},
	})
	if len(failures) != 1 || len(releaser.calls) != 2 {
		t.Fatalf("expected one failure and two calls, failures=%v calls=%v", failures, releaser.calls)
	}
	if failures[0].Resource.SourceID != "bad-source" {
		t.Fatalf("failure resource = %#v, want bad-source", failures[0].Resource)
	}
}

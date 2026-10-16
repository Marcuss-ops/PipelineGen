package adapters

import (
	"context"
	"errors"
	"testing"
)

type stubDurable struct {
	uri string
	err error
}

func (s stubDurable) Resolve(_ context.Context, _ string) (DurableOverlayArtifact, error) {
	if s.err != nil {
		return DurableOverlayArtifact{}, s.err
	}
	return DurableOverlayArtifact{JobID: "j", URI: s.uri}, nil
}

func TestResolveWithDurableFallback(t *testing.T) {
	got, err := ResolveWithDurableFallback(context.Background(), stubDurable{uri: "/durable/a.mp4"}, "j", func() (string, error) {
		return "/local/b.mp4", nil
	})
	if err != nil || got != "/durable/a.mp4" {
		t.Fatalf("durable wins: %q %v", got, err)
	}
	got, err = ResolveWithDurableFallback(context.Background(), stubDurable{err: errors.New("nope")}, "j", func() (string, error) {
		return "/local/b.mp4", nil
	})
	if err != nil || got != "/local/b.mp4" {
		t.Fatalf("fallback to local: %q %v", got, err)
	}
}

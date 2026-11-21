package finaljob

import (
	"context"
	"testing"
)

// TestBeginHandoffScopesFragmentTiming pins the receipt path: allocating a
// handoff scope, injecting it into the context and reading it back through
// Timing must isolate one handoff's work from a concurrent one.
func TestBeginHandoffScopesFragmentTiming(t *testing.T) {
	p := &SegmentPreparer{}
	ctxA, scopeA := p.BeginHandoff(context.Background())
	ctxB, scopeB := p.BeginHandoff(context.Background())
	if scopeA == 0 || scopeB == 0 || scopeA == scopeB {
		t.Fatalf("scopes = %d, %d; want two distinct non-zero handoffs", scopeA, scopeB)
	}
	p.record(ctxA, func(s *finalJobSegmentStats) {
		s.segments++
		s.encodeMS += 1_200
		s.publishMS += 300
		s.bytes += 9_000
	})
	p.record(ctxB, func(s *finalJobSegmentStats) {
		s.segments++
		s.downloadMS += 45
	})

	gotA := p.Timing(scopeA)
	if gotA.Segments != 1 || gotA.EncodeMS != 1_200 || gotA.PublishMS != 300 || gotA.Bytes != 9_000 {
		t.Fatalf("handoff A timing = %+v, want its own encode/publish/bytes", gotA)
	}
	gotB := p.Timing(scopeB)
	if gotB.Segments != 1 || gotB.DownloadMS != 45 || gotB.EncodeMS != 0 {
		t.Fatalf("handoff B timing = %+v, want only its own download", gotB)
	}
	if got := p.Timing(0); got.Segments != 0 || got.Bytes != 0 {
		t.Fatalf("scope 0 timing = %+v, want no attribution", got)
	}
}

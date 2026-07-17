package topfive

import "testing"

func TestBuildCanonicalizesAndRanksMoments(t *testing.T) {
	got, err := Build(Request{ID: "ranking-1", Items: []Moment{
		{Name: "deep reveal", Path: "reveal.mp4", StartMs: 0, EndMs: 3000, Score: 0.4},
		{Name: "Impact now", Path: "impact.mp4", StartMs: 500, EndMs: 2500, Score: 0.9},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Items[0].Name != "IMPACT" || got.Items[1].Name != "DEEP" {
		t.Fatalf("unexpected ranking: %#v", got.Items)
	}
	if got.Title != "TOP 5 MOMENTS" || got.SchemaVersion != SchemaVersion {
		t.Fatalf("unexpected canonical response: %#v", got)
	}
}

func TestBuildRejectsLongMoment(t *testing.T) {
	_, err := Build(Request{ID: "ranking-1", Items: []Moment{{Name: "long", Path: "x.mp4", EndMs: 3001}}})
	if err == nil {
		t.Fatal("expected 3 second limit error")
	}
}

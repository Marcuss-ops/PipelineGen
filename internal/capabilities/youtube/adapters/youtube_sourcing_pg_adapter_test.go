package adapters

import (
	"context"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

// fakePGMediaLookup is the fake media reader backing the sourcing port tests.
type fakePGMediaLookup struct {
	nameID  string
	nameArg string

	ytID     string
	ytArg    string
	ytHasSeg bool
	ytStart  int64
	ytEnd    int64

	urlID  string
	urlArg string

	rec    *pgmedia.MediaAssetRecord
	getErr error
}

func (f *fakePGMediaLookup) FindClipIDByName(_ context.Context, name string) (string, error) {
	f.nameArg = name
	return f.nameID, nil
}

func (f *fakePGMediaLookup) FindClipIDByYouTubeVideoID(_ context.Context, videoID string, hasSegment bool, startSec, endSec float64) (string, error) {
	f.ytArg = videoID
	f.ytHasSeg = hasSegment
	f.ytStart = int64(startSec * 1000)
	f.ytEnd = int64(endSec * 1000)
	return f.ytID, nil
}

func (f *fakePGMediaLookup) FindClipIDBySourceURL(_ context.Context, url string) (string, error) {
	f.urlArg = url
	return f.urlID, nil
}

func (f *fakePGMediaLookup) GetAsset(_ context.Context, _ string) (*pgmedia.MediaAssetRecord, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.rec, nil
}

var _ PGMediaSourcingLookup = (*fakePGMediaLookup)(nil)

func TestSourcingClipStorePGAdapter_Constructor(t *testing.T) {
	if NewSourcingClipStorePGAdapter(nil) != nil {
		t.Fatal("nil media lookup must return a nil adapter")
	}
	if NewSourcingClipStorePGAdapter(&fakePGMediaLookup{}) == nil {
		t.Fatal("non-nil media lookup must return a non-nil adapter")
	}
}

func TestSourcingClipStorePGAdapter_FindByName(t *testing.T) {
	lookup := &fakePGMediaLookup{nameID: "clip-42"}
	adapter := NewSourcingClipStorePGAdapter(lookup)

	id, err := adapter.FindByName(context.Background(), "My Clip")
	if err != nil {
		t.Fatalf("FindByName: %v", err)
	}
	if id != "clip-42" || lookup.nameArg != "My Clip" {
		t.Fatalf("unexpected FindByName result id=%q arg=%q", id, lookup.nameArg)
	}
}

func TestSourcingClipStorePGAdapter_FindExisting_Segment(t *testing.T) {
	lookup := &fakePGMediaLookup{ytID: "seg-1"}
	adapter := NewSourcingClipStorePGAdapter(lookup)

	id, err := adapter.FindExisting(context.Background(), "vid123", "https://youtu.be/vid123", 1.5, 4.0)
	if err != nil {
		t.Fatalf("FindExisting: %v", err)
	}
	if id != "seg-1" {
		t.Fatalf("id = %q, want seg-1", id)
	}
	if lookup.ytArg != "vid123" || !lookup.ytHasSeg || lookup.ytStart != 1500 || lookup.ytEnd != 4000 {
		t.Fatalf("unexpected youtube lookup: arg=%q hasSeg=%v start=%d end=%d",
			lookup.ytArg, lookup.ytHasSeg, lookup.ytStart, lookup.ytEnd)
	}
	if lookup.urlArg != "" {
		t.Fatalf("segment lookups must not fall back to source URL, got %q", lookup.urlArg)
	}
}

func TestSourcingClipStorePGAdapter_FindExisting_NoSegmentFallsBackToURL(t *testing.T) {
	lookup := &fakePGMediaLookup{urlID: "url-9"}
	adapter := NewSourcingClipStorePGAdapter(lookup)

	id, err := adapter.FindExisting(context.Background(), "", "https://youtu.be/abc", 0, 0)
	if err != nil {
		t.Fatalf("FindExisting: %v", err)
	}
	if id != "url-9" || lookup.urlArg != "https://youtu.be/abc" {
		t.Fatalf("unexpected fallback: id=%q urlArg=%q", id, lookup.urlArg)
	}
}

func TestSourcingClipStorePGAdapter_GetClip_MapsRecord(t *testing.T) {
	lookup := &fakePGMediaLookup{rec: &pgmedia.MediaAssetRecord{
		ID:             "clip-7",
		Name:           "Boxing training",
		Filename:       "boxing.mp4",
		Source:         "youtube",
		Category:       "training",
		DurationMS:     12500,
		LocalPath:      "/media/boxing.mp4",
		DriveFileID:    "drive-7",
		DriveLink:      "https://drive.example/7",
		SHA256:         "sha256:abc",
		Tags:           []string{"boxing", "gym"},
		SourceProvider: "youtube",
		SourceVideoID:  "vid-7",
		StartMS:        1500,
		EndMS:          4000,
		MetadataJSON:   `{"clip_summary":"a summary","topics":["a","b"],"speakers":["s1"],"mentioned_people":["p1"],"hook":"watch this"}`,
	}}
	adapter := NewSourcingClipStorePGAdapter(lookup)

	clip, err := adapter.GetClip(context.Background(), "clip-7")
	if err != nil {
		t.Fatalf("GetClip: %v", err)
	}
	if clip == nil {
		t.Fatal("GetClip returned nil for an existing record")
	}
	if clip.ID != "clip-7" || clip.Name != "Boxing training" || clip.Filename != "boxing.mp4" {
		t.Fatalf("unexpected identity: %+v", clip)
	}
	if clip.Source != "youtube" || clip.SourceProvider != "youtube" || clip.SourceVideoID != "vid-7" {
		t.Fatalf("unexpected provenance: %+v", clip)
	}
	if clip.StartSec != 1.5 || clip.EndSec != 4.0 {
		t.Fatalf("segment bounds = %v..%v, want 1.5..4.0", clip.StartSec, clip.EndSec)
	}
	if clip.Duration != 12500000000 {
		t.Fatalf("duration = %v, want 12.5s", clip.Duration)
	}
	if clip.LegacyFileMD5 != "sha256:abc" || clip.DriveFileID != "drive-7" || clip.LocalPath != "/media/boxing.mp4" {
		t.Fatalf("unexpected locators: %+v", clip)
	}
	if len(clip.Tags) != 2 || clip.Tags[0] != "boxing" {
		t.Fatalf("tags = %v", clip.Tags)
	}
	if clip.Summary != "a summary" || clip.Hook != "watch this" {
		t.Fatalf("rich strings not mapped: summary=%q hook=%q", clip.Summary, clip.Hook)
	}
	if len(clip.Topics) != 2 || len(clip.Speakers) != 1 || len(clip.MentionedPeople) != 1 {
		t.Fatalf("rich slices not mapped: topics=%v speakers=%v people=%v", clip.Topics, clip.Speakers, clip.MentionedPeople)
	}
}

func TestSourcingClipStorePGAdapter_GetClip_NotFound(t *testing.T) {
	lookup := &fakePGMediaLookup{getErr: pgmedia.ErrMediaAssetNotFound}
	adapter := NewSourcingClipStorePGAdapter(lookup)

	clip, err := adapter.GetClip(context.Background(), "missing")
	if err != nil {
		t.Fatalf("GetClip: %v", err)
	}
	if clip != nil {
		t.Fatalf("not-found must return (nil, nil), got %+v", clip)
	}
}

func TestSourcingClipStorePGAdapter_SatisfiesPort(t *testing.T) {
	var _ sourcing.ClipStorePort = NewSourcingClipStorePGAdapter(&fakePGMediaLookup{})
}

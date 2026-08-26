package enrichment

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"context"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"testing"
)

type recoveryAssetReader struct{ exists bool }

func (r recoveryAssetReader) Exists(context.Context, string) (bool, error) { return r.exists, nil }

type recoveryHistoricalReader struct{ text *HistoricalText }

func (r recoveryHistoricalReader) FindByAssetID(context.Context, string) (*HistoricalText, error) {
	return r.text, nil
}

type recoveryTracks struct {
	existing map[detail.TextTrackKind]*detail.TextTrack
}

func (r *recoveryTracks) Find(_ context.Context, _ string, _ string, k detail.TextTrackKind) (*detail.TextTrack, error) {
	return r.existing[k], nil
}

type recoveryCommitter struct{ writes []detail.TextTrack }

func (r *recoveryCommitter) CommitRecoveredText(_ context.Context, _ string, _ string, ts []detail.TextTrack, _ string) error {
	r.writes = append(r.writes, ts...)
	return nil
}

func TestRecoveryOnlyMissingAndQueuesTargetedReindex(t *testing.T) {
	tracks := &recoveryTracks{existing: map[detail.TextTrackKind]*detail.TextTrack{detail.TextTrackDescription: {Status: detail.TextTrackReady, TextContent: "better canonical description"}}}
	committer := &recoveryCommitter{}
	svc, err := NewRecoveryService(recoveryAssetReader{true}, recoveryHistoricalReader{&HistoricalText{AssetID: "a", Description: "old", SearchText: "search tokens", Projection: "legacy-v1"}}, tracks, committer)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.RecoverAsset(context.Background(), "a", "en")
	if err != nil {
		t.Fatal(err)
	}
	if got.Recovered != 1 || got.SkippedBetter != 1 || !got.ReindexQueued {
		t.Fatalf("unexpected result: %+v", got)
	}
	if len(committer.writes) != 1 || committer.writes[0].TextKind != detail.TextTrackSearchText {
		t.Fatalf("writes: %+v", committer.writes)
	}
}
func TestRecoveryUnknownAssetDoesNotCreateArtifacts(t *testing.T) {
	tracks := &recoveryTracks{existing: map[detail.TextTrackKind]*detail.TextTrack{}}
	committer := &recoveryCommitter{}
	svc, err := NewRecoveryService(recoveryAssetReader{false}, recoveryHistoricalReader{&HistoricalText{AssetID: "orphan", Description: "do not import"}}, tracks, committer)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.RecoverAsset(context.Background(), "orphan", "en")
	if err != nil {
		t.Fatal(err)
	}
	if got.Recovered != 0 || len(committer.writes) != 0 {
		t.Fatalf("orphan imported: %+v writes=%d", got, len(committer.writes))
	}
}

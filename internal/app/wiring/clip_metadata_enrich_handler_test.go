// Package app — clip_metadata_enrich_handler_test.go pins the durable
// metadata.enrich.requested consumer: it runs the canonical analyzer and
// persists the semantic snapshot + index request through the metadata
// writer, and fails closed on malformed payloads.
package wiring

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	ytmetadata "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/metadata"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	pgmedia "github.com/Marcuss-ops/PipelineGen/internal/platform/postgres/media"
)

type enrichHandlerBuilder struct {
	calls int
	last  youtubetypes.ClipMetadataInput
}

func (b *enrichHandlerBuilder) Build(_ context.Context, in youtubetypes.ClipMetadataInput) (youtubetypes.CanonicalClipMetadata, error) {
	b.calls++
	b.last = in
	return youtubetypes.CanonicalClipMetadata{
		ClipID:        in.ClipID,
		AssetID:       in.ClipID,
		SourceVersion: "v1",
		QualityScore:  0.5,
	}, nil
}

type enrichHandlerWriter struct {
	calls int
}

func (w *enrichHandlerWriter) UpdateClipMetadataAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata) error {
	w.calls++
	return nil
}

func (w *enrichHandlerWriter) UpdateClipMetadataTextsAndRequestIndex(_ context.Context, _ string, _ youtubetypes.CanonicalClipMetadata, _ []detail.TextTrack) error {
	w.calls++
	return nil
}

func newEnrichHandlerTestService(t *testing.T) (*ytmetadata.MetadataService, *enrichHandlerBuilder, *enrichHandlerWriter) {
	t.Helper()
	b := &enrichHandlerBuilder{}
	w := &enrichHandlerWriter{}
	svc, err := ytmetadata.NewMetadataService(ytmetadata.MetadataDeps{
		Builder: b,
		Writer:  w,
		Logger:  zap.NewNop(),
	})
	require.NoError(t, err)
	return svc, b, w
}

func TestClipMetadataEnrichHandler_RunsAnalyzerAndWrites(t *testing.T) {
	svc, builder, writer := newEnrichHandlerTestService(t)
	h, err := newClipMetadataEnrichHandler(svc, zap.NewNop())
	require.NoError(t, err)

	raw, err := json.Marshal(youtubetypes.ClipMetadataInput{
		ClipID:     "yt_enrich_0_10_v1",
		Title:      "Enrich",
		Transcript: "hello world",
	})
	require.NoError(t, err)

	claim := &pgmedia.OutboxClaim{
		Event: pgmedia.OutboxEvent{
			ID:          7,
			EventType:   pgmedia.EventMetadataEnrichRequested,
			AggregateID: "yt_enrich_0_10_v1",
			PayloadJSON: string(raw),
			EventKey:    "metadata-enrich:yt_enrich_0_10_v1:v1:v1",
		},
	}
	require.NoError(t, h.Handle(context.Background(), claim))
	require.Equal(t, 1, builder.calls, "handler must run the analyzer exactly once")
	require.Equal(t, "hello world", builder.last.Transcript, "analyst input must carry the event transcript")
	require.Equal(t, 1, writer.calls, "handler must persist metadata + index atomically")
}

func TestClipMetadataEnrichHandler_MalformedPayloadFailsClosed(t *testing.T) {
	svc, _, _ := newEnrichHandlerTestService(t)
	h, err := newClipMetadataEnrichHandler(svc, zap.NewNop())
	require.NoError(t, err)

	claim := &pgmedia.OutboxClaim{
		Event: pgmedia.OutboxEvent{ID: 9, EventType: pgmedia.EventMetadataEnrichRequested, PayloadJSON: "{"},
	}
	require.Error(t, h.Handle(context.Background(), claim),
		"a malformed payload must surface an error so the worker retries/dead-letters it")
}

func TestNewClipMetadataEnrichHandler_RequiresService(t *testing.T) {
	_, err := newClipMetadataEnrichHandler(nil, zap.NewNop())
	require.Error(t, err, "a nil metadata service must be a construction error")
}

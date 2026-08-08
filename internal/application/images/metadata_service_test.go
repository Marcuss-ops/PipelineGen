package images

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// recordingMetaWriter captures the WriteRequest passed to Write.
type recordingMetaWriter struct {
	req SemanticWriteRequest
}

func (m *recordingMetaWriter) GeneratePayload(ctx context.Context, req SemanticWriteRequest) (*SemanticPayload, string, error) {
	return nil, "", nil
}

func (m *recordingMetaWriter) Write(ctx context.Context, req SemanticWriteRequest) (*SemanticWriteResult, error) {
	m.req = req
	return nil, nil
}

func TestTagImageMetadata_DefaultSourceType_Retrieved(t *testing.T) {
	writer := &recordingMetaWriter{}
	svc := &MetadataService{metaWriter: writer, log: zap.NewNop()}

	ctx := context.WithValue(context.Background(), ImageURLKey, "https://example.com/cat.jpg")
	_, _ = svc.tagImageMetadata(ctx, "A cat", "", "wikipedia", "hash", "/tmp/cat.jpg", 100, 200)

	if writer.req.SourceType != "retrieved" {
		t.Errorf("SourceType = %q, want retrieved", writer.req.SourceType)
	}
	if writer.req.Source != "wikipedia" {
		t.Errorf("Source = %q, want wikipedia", writer.req.Source)
	}
}

func TestTagImageMetadata_DefaultSourceType_Generated(t *testing.T) {
	writer := &recordingMetaWriter{}
	svc := &MetadataService{metaWriter: writer, log: zap.NewNop()}

	ctx := context.WithValue(context.Background(), ImageURLKey, "")
	_, _ = svc.tagImageMetadata(ctx, "A cat", "", "google-slides", "hash", "/tmp/cat.jpg", 100, 200)

	if writer.req.SourceType != "generated" {
		t.Errorf("SourceType = %q, want generated", writer.req.SourceType)
	}
	if writer.req.Source != "google-slides" {
		t.Errorf("Source = %q, want google-slides", writer.req.Source)
	}
}

func TestTagImageMetadata_DefaultSourceType_Upload(t *testing.T) {
	writer := &recordingMetaWriter{}
	svc := &MetadataService{metaWriter: writer, log: zap.NewNop()}

	ctx := context.WithValue(context.Background(), ImageURLKey, "")
	_, _ = svc.tagImageMetadata(ctx, "A cat", "", "upload", "hash", "/tmp/cat.jpg", 100, 200)

	if writer.req.SourceType != "uploaded" {
		t.Errorf("SourceType = %q, want uploaded", writer.req.SourceType)
	}
}

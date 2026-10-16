package service

import (
	"strings"
	"testing"

	"github.com/Marcuss-ops/RenderingGen/queue/internal/model"
)

func TestValidateArtifactRequiresAllPublicationFields(t *testing.T) {
	valid := model.Artifact{
		StorageKey:   "object-key",
		ArtifactHash: strings.Repeat("a", 64),
		SizeBytes:    42,
		ContentType:  "video/mp4",
	}
	tests := []struct {
		name     string
		artifact model.Artifact
		wantErr  string
	}{
		{name: "missing storage key", artifact: model.Artifact{ArtifactHash: valid.ArtifactHash, SizeBytes: valid.SizeBytes, ContentType: valid.ContentType}, wantErr: "storage_key"},
		{name: "blank storage key", artifact: model.Artifact{StorageKey: " \t", ArtifactHash: valid.ArtifactHash, SizeBytes: valid.SizeBytes, ContentType: valid.ContentType}, wantErr: "storage_key"},
		{name: "missing hash", artifact: model.Artifact{StorageKey: valid.StorageKey, SizeBytes: valid.SizeBytes, ContentType: valid.ContentType}, wantErr: "sha256"},
		{name: "blank hash", artifact: model.Artifact{StorageKey: valid.StorageKey, ArtifactHash: " \n", SizeBytes: valid.SizeBytes, ContentType: valid.ContentType}, wantErr: "sha256"},
		{name: "zero size", artifact: model.Artifact{StorageKey: valid.StorageKey, ArtifactHash: valid.ArtifactHash, ContentType: valid.ContentType}, wantErr: "size_bytes"},
		{name: "negative size", artifact: model.Artifact{StorageKey: valid.StorageKey, ArtifactHash: valid.ArtifactHash, SizeBytes: -1, ContentType: valid.ContentType}, wantErr: "size_bytes"},
		{name: "missing content type", artifact: model.Artifact{StorageKey: valid.StorageKey, ArtifactHash: valid.ArtifactHash, SizeBytes: valid.SizeBytes}, wantErr: "content_type"},
		{name: "blank content type", artifact: model.Artifact{StorageKey: valid.StorageKey, ArtifactHash: valid.ArtifactHash, SizeBytes: valid.SizeBytes, ContentType: "  "}, wantErr: "content_type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateArtifact(test.artifact)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("validateArtifact error = %v, want message containing %q", err, test.wantErr)
			}
		})
	}
	if err := validateArtifact(valid); err != nil {
		t.Fatalf("complete artifact rejected: %v", err)
	}
}

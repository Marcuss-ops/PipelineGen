package drivecleanup

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"velox/go-master/internal/repository/clips"
)

func TestNewService(t *testing.T) {
	repo := &clips.Repository{}
	svc := NewService(repo, nil, zap.NewNop(), true)
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
	if !svc.useTrash {
		t.Fatal("expected useTrash to be true")
	}
}

func TestDeleteClipAndDriveFile_EmptyID(t *testing.T) {
	repo := &clips.Repository{}
	svc := NewService(repo, nil, zap.NewNop(), true)

	err := svc.DeleteClipAndDriveFile(context.Background(), "", true)
	if err == nil {
		t.Fatal("expected error for empty clip id")
	}
}

func TestTrashClip_EmptyID(t *testing.T) {
	repo := &clips.Repository{}
	svc := NewService(repo, nil, zap.NewNop(), true)

	err := svc.TrashClip(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty clip id")
	}
}

func TestDeleteClipPermanently_EmptyID(t *testing.T) {
	repo := &clips.Repository{}
	svc := NewService(repo, nil, zap.NewNop(), true)

	err := svc.DeleteClipPermanently(context.Background(), "")
	if err == nil {
		t.Fatal("expected error for empty clip id")
	}
}

// Note: Full integration tests would require a mock Drive service or test server
// For now, we test the validation and initialization logic

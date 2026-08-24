package delivery

import (
	"context"
	"errors"
	"testing"
)

type adminUploadPublisherStub struct {
	request PublishRequest
	result  *PublishResult
	err     error
}

func (s *adminUploadPublisherStub) Publish(_ context.Context, req PublishRequest) (*PublishResult, error) {
	s.request = req
	if s.err != nil {
		return nil, s.err
	}
	return s.result, nil
}

func (s *adminUploadPublisherStub) ResolveFolder(context.Context, PublishRequest) (string, error) {
	return "", nil
}

func TestAdminUploadService_PublishesToExplicitFolder(t *testing.T) {
	stub := &adminUploadPublisherStub{result: &PublishResult{FileID: "file-1"}}
	service, err := NewAdminUploadService(stub)
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.Publish(context.Background(), AdminUploadCommand{
		LocalPath:   "/tmp/input.mp4",
		FolderID:    "folder-1",
		Filename:    "output.mp4",
		Description: "admin upload",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FileID != "file-1" {
		t.Fatalf("FileID = %q, want file-1", result.FileID)
	}
	if stub.request.Destination != DestinationAdmin {
		t.Fatalf("Destination = %q, want %q", stub.request.Destination, DestinationAdmin)
	}
	if stub.request.DestinationFolderID != "folder-1" {
		t.Fatalf("DestinationFolderID = %q, want folder-1", stub.request.DestinationFolderID)
	}
	if stub.request.ConflictPolicy != ConflictOverwrite {
		t.Fatalf("ConflictPolicy = %v, want ConflictOverwrite", stub.request.ConflictPolicy)
	}
}

func TestAdminUploadService_RejectsInvalidCommand(t *testing.T) {
	service, err := NewAdminUploadService(&adminUploadPublisherStub{result: &PublishResult{FileID: "file-1"}})
	if err != nil {
		t.Fatal(err)
	}
	for name, command := range map[string]AdminUploadCommand{
		"missing local path": {FolderID: "folder-1", Filename: "file.mp4"},
		"missing folder":     {LocalPath: "/tmp/file.mp4", Filename: "file.mp4"},
		"missing filename":   {LocalPath: "/tmp/file.mp4", FolderID: "folder-1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Publish(context.Background(), command); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestAdminUploadService_PropagatesPublisherError(t *testing.T) {
	expected := errors.New("publisher failed")
	service, err := NewAdminUploadService(&adminUploadPublisherStub{err: expected})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Publish(context.Background(), AdminUploadCommand{
		LocalPath: "/tmp/file.mp4", FolderID: "folder-1", Filename: "file.mp4",
	})
	if !errors.Is(err, expected) {
		t.Fatalf("error = %v, want wrapped publisher error", err)
	}
}

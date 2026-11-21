package artifacts

import (
	"context"
	"testing"
)

type fakeMediaReader struct {
	get     *MediaRecord
	content *MediaRecord
	phash   string
	all     []*MediaRecord
}

func (f *fakeMediaReader) Get(context.Context, string) (*MediaRecord, error) { return f.get, nil }
func (f *fakeMediaReader) FindByPHash(context.Context, string) (string, error) {
	return f.phash, nil
}
func (f *fakeMediaReader) FindByContentHash(context.Context, string) (*MediaRecord, error) {
	return f.content, nil
}
func (f *fakeMediaReader) GetAllWithDriveFileID(context.Context) ([]*MediaRecord, error) {
	return f.all, nil
}

func TestClipsRegistryDelegatesMediaReadsToTypedReader(t *testing.T) {
	record := &MediaRecord{ID: "asset-1", DriveFileID: "drive-1"}
	reader := &fakeMediaReader{
		get: record, content: record, phash: "asset-1", all: []*MediaRecord{record},
	}
	registry := NewClipsRegistry(nil, nil, nil, WithMediaReader(reader))

	got, err := registry.GetMedia(context.Background(), "asset-1")
	if err != nil || got != record {
		t.Fatalf("GetMedia = %#v, %v", got, err)
	}
	got, err = registry.FindByContentHash(context.Background(), "ABC")
	if err != nil || got != record {
		t.Fatalf("FindByContentHash = %#v, %v", got, err)
	}
	id, err := registry.FindByPHash(context.Background(), "phash")
	if err != nil || id != "asset-1" {
		t.Fatalf("FindByPHash = %q, %v", id, err)
	}
	all, err := registry.GetAllWithDriveFileID(context.Background())
	if err != nil || len(all) != 1 || all[0] != record {
		t.Fatalf("GetAllWithDriveFileID = %#v, %v", all, err)
	}
}

func TestClipsRegistryMediaReadsFailClosedWithoutReader(t *testing.T) {
	registry := NewClipsRegistry(nil, nil, nil)
	if _, err := registry.GetAllWithDriveFileID(context.Background()); err == nil {
		t.Fatal("GetAllWithDriveFileID succeeded without a media reader")
	}
	if _, err := registry.FindByPHash(context.Background(), "phash"); err == nil {
		t.Fatal("FindByPHash succeeded without a media reader")
	}
	if _, err := registry.FindByContentHash(context.Background(), "sha256"); err == nil {
		t.Fatal("FindByContentHash succeeded without a media reader")
	}
	if _, err := registry.GetMedia(context.Background(), "asset-1"); err == nil {
		t.Fatal("GetMedia succeeded without a media reader or details reader")
	}
}

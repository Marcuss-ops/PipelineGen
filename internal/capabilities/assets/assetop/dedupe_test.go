package assetop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.uber.org/zap"
)

type dedupeStoreStub struct {
	records []*AssetRecord
	err     error
}

func (s *dedupeStoreStub) FindExisting(_ context.Context, query ExistingAssetQuery) (*AssetRecord, error) {
	if s.err != nil {
		return nil, s.err
	}
	for _, rec := range s.records {
		switch {
		case query.ID != "" && rec.ID == query.ID:
			return rec, nil
		case query.ContentSHA256 != "" && strings.EqualFold(rec.ContentHash, query.ContentSHA256):
			return rec, nil
		case query.DriveFileID != "" && rec.DriveFileID == query.DriveFileID:
			return rec, nil
		case query.Filename != "" && rec.Filename == query.Filename && (query.Source == "" || rec.Source == query.Source):
			return rec, nil
		}
	}
	return nil, nil
}

func (*dedupeStoreStub) ListWithDriveFileID(context.Context, string) ([]*AssetRecord, error) {
	return nil, nil
}
func (*dedupeStoreStub) MarkDriveMissing(context.Context, string) error  { return nil }
func (*dedupeStoreStub) DeleteAssetRecord(context.Context, string) error { return nil }

func newDedupeService(store AssetRecordStore) *DedupeService {
	return NewDedupeService(store, DuplicatePolicy{
		Enabled:            true,
		CheckByContentHash: true,
		CheckByDriveFileID: true,
		CheckByFilename:    true,
		SkipIfExists:       true,
	}, zap.NewNop())
}

func TestCheckDuplicateSeparatesLogicalAndContentIdentity(t *testing.T) {
	const sha = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

	tests := []struct {
		name       string
		record     *AssetRecord
		query      ExistingAssetQuery
		wantKind   DuplicateKind
		wantSame   bool
		wantRecord string
	}{
		{
			name:       "same asset and same bytes is idempotent",
			record:     &AssetRecord{ID: "asset-a", ContentHash: sha},
			query:      ExistingAssetQuery{ID: "asset-a", ContentSHA256: sha},
			wantKind:   DuplicateSameAsset,
			wantSame:   true,
			wantRecord: "asset-a",
		},
		{
			name:       "same asset with changed bytes is not idempotent",
			record:     &AssetRecord{ID: "asset-a", ContentHash: sha},
			query:      ExistingAssetQuery{ID: "asset-a", ContentSHA256: strings.Repeat("f", 64)},
			wantKind:   DuplicateSameAsset,
			wantSame:   false,
			wantRecord: "asset-a",
		},
		{
			name:       "different assets can share the same bytes",
			record:     &AssetRecord{ID: "asset-a", ContentHash: sha},
			query:      ExistingAssetQuery{ID: "asset-b", ContentSHA256: sha},
			wantKind:   DuplicateSameContent,
			wantSame:   true,
			wantRecord: "asset-a",
		},
		{
			name:     "legacy digest does not establish identity",
			record:   &AssetRecord{ID: "asset-a", LegacyFileMD5: "same-md5"},
			query:    ExistingAssetQuery{ID: "asset-b", LegacyFileMD5: "same-md5"},
			wantKind: DuplicateNone,
		},
		{
			name:       "same Drive file under another asset reuses storage",
			record:     &AssetRecord{ID: "asset-a", DriveFileID: "drive-file", ContentHash: sha},
			query:      ExistingAssetQuery{ID: "asset-b", DriveFileID: "drive-file"},
			wantKind:   DuplicateSameContent,
			wantSame:   true,
			wantRecord: "asset-a",
		},
		{
			name:       "filename is only a hint",
			record:     &AssetRecord{ID: "asset-a", Filename: "clip.mp4", Source: "youtube"},
			query:      ExistingAssetQuery{ID: "asset-b", Filename: "clip.mp4", Source: "youtube"},
			wantKind:   DuplicateFilenameMatch,
			wantRecord: "asset-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := newDedupeService(&dedupeStoreStub{records: []*AssetRecord{tt.record}})
			got, err := service.CheckDuplicate(context.Background(), tt.query)
			if err != nil {
				t.Fatalf("CheckDuplicate: %v", err)
			}
			if got == nil || got.Kind != tt.wantKind || got.SameContent != tt.wantSame {
				t.Fatalf("match = %#v, want kind=%s sameContent=%t", got, tt.wantKind, tt.wantSame)
			}
			gotID := ""
			if got.Record != nil {
				gotID = got.Record.ID
			}
			if gotID != tt.wantRecord {
				t.Fatalf("record ID = %q, want %q", gotID, tt.wantRecord)
			}
		})
	}
}

func TestCheckDuplicateErrorsWhenStoreIsMissingOrUnreadable(t *testing.T) {
	service := newDedupeService(nil)
	if _, err := service.CheckDuplicate(context.Background(), ExistingAssetQuery{ID: "asset-a"}); !errors.Is(err, ErrAssetStoreMissing) {
		t.Fatalf("missing-store error = %v, want ErrAssetStoreMissing", err)
	}

	wantErr := errors.New("catalog unavailable")
	service = newDedupeService(&dedupeStoreStub{err: wantErr})
	if _, err := service.CheckDuplicate(context.Background(), ExistingAssetQuery{ID: "asset-a"}); !errors.Is(err, wantErr) {
		t.Fatalf("store error = %v, want wrapped catalog error", err)
	}
}

func TestDefaultDuplicatePolicyUsesCanonicalContentHash(t *testing.T) {
	policy := DefaultDuplicatePolicy()
	if !policy.CheckByContentHash || policy.CheckByHash {
		t.Fatalf("default hash policy = %#v, want canonical SHA-256 enabled and legacy MD5 disabled", policy)
	}
}

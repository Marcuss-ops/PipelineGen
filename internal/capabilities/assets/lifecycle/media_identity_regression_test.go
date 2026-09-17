package lifecycle

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/artifacts"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/assetop"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/drive"
)

// ─────────────────────────────────────────────────────────────────────────────
// MEDIA-IDENTITY REGRESSION MATRIX (A → D → C)
//
// The uniform rule these tests defend:
//
//	asset_id decides WHAT an asset is;
//	SHA-256  decides WHICH BYTES it is;
//	MD5      decides NOTHING.
//
// Each case below is a DIFFERENT failure mode, and each one has failed in a
// real code path: the clips registry used to overwrite the content address with
// the caller's MD5 (A), the Drive publish used to accept an empty verification
// signal (D), and the dedupe used to collapse two distinct logical assets that
// merely shared their bytes (C).
//
//	1. same id + same bytes        → no second upload (idempotent skip)
//	2. different id + same bytes   → two logical assets, ONE physical identity
//	3. same id + changed bytes     → MUST NOT be reported as a duplicate
//	4. MD5 matches another record  → MUST NOT dedup when the SHA-256 differs
//	5. upload bytes ≠ expected sha → publish fails
//	6. Drive size ≠ expected size  → publish fails
//
// Case 2 carries the mutation-strength requirement: it is the case that stops a
// future refactor from turning content addressing back into asset collapsing.
// ─────────────────────────────────────────────────────────────────────────────

const regressionLegacyMD5 = "7f83b1657ff1fc53b92dc18148a1d65dfa135e2f"

// identityStoreStub is a query-aware AssetRecordStore. It answers ONLY from the
// field the query actually named, and it counts the queries that could only be
// answered by the legacy MD5 tier — so a regression that reintroduces MD5-based
// dedup fails loudly instead of passing on a coincidental stub.
type identityStoreStub struct {
	records []*assetop.AssetRecord
	// legacyTierQueries counts queries that carried a legacy digest without a
	// logical or content identity. Any non-zero value means a decision tried to
	// resolve an asset by MD5.
	legacyTierQueries int
}

func (s *identityStoreStub) FindExisting(_ context.Context, query assetop.ExistingAssetQuery) (*assetop.AssetRecord, error) {
	if strings.TrimSpace(query.LegacyFileMD5) != "" && query.ID == "" && query.ContentSHA256 == "" {
		s.legacyTierQueries++
	}
	for _, rec := range s.records {
		switch {
		case query.ID != "" && rec.ID == query.ID:
			return rec, nil
		case query.ContentSHA256 != "" && strings.EqualFold(strings.TrimSpace(rec.ContentHash), strings.TrimSpace(query.ContentSHA256)):
			return rec, nil
		case query.DriveFileID != "" && rec.DriveFileID == query.DriveFileID:
			return rec, nil
		case query.Filename != "" && rec.Filename == query.Filename && (query.Source == "" || rec.Source == query.Source):
			return rec, nil
		}
	}
	return nil, nil
}

func (s *identityStoreStub) ListWithDriveFileID(context.Context, string) ([]*assetop.AssetRecord, error) {
	return nil, nil
}
func (s *identityStoreStub) MarkDriveMissing(context.Context, string) error  { return nil }
func (s *identityStoreStub) DeleteAssetRecord(context.Context, string) error { return nil }

func (s *identityStoreStub) Upsert(ctx context.Context, rec *artifacts.MediaRecord) error {
	return nil
}
func (s *identityStoreStub) Get(context.Context, string) (*artifacts.MediaRecord, error) {
	return nil, nil
}

var _ AssetRecordStore = (*identityStoreStub)(nil)

// identityFinalizerStub records the exact MediaRecord the canonical commit
// boundary received, in order.
type identityFinalizerStub struct {
	records []*artifacts.MediaRecord
}

func (f *identityFinalizerStub) Finalize(_ context.Context, rec *artifacts.MediaRecord, _ artifacts.FinalizeOptions) (*artifacts.FinalizeResult, error) {
	copied := *rec
	f.records = append(f.records, &copied)
	return &artifacts.FinalizeResult{OK: true, Status: rec.Status, Record: rec}, nil
}

var _ Finalizer = (*identityFinalizerStub)(nil)

// identityPublisherStub records every PublishRequest, so the verification
// signals the Drive uploader will enforce can be asserted directly.
type identityPublisherStub struct {
	requests []delivery.PublishRequest
	err      error
}

func (p *identityPublisherStub) Publish(_ context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error) {
	p.requests = append(p.requests, req)
	if p.err != nil {
		return nil, p.err
	}
	return &delivery.PublishResult{FileID: "drive-new", WebViewLink: "https://drive.test/new"}, nil
}

func (p *identityPublisherStub) ResolveFolder(context.Context, delivery.PublishRequest) (string, error) {
	return "", nil
}

var _ delivery.Publisher = (*identityPublisherStub)(nil)

// ── fixtures ────────────────────────────────────────────────────────────────

func regressionArtifact(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact.mp4")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write regression artifact: %v", err)
	}
	return path
}

// regressionSHA is computed with the standard library, never with the code
// under test, so an expectation cannot be satisfied by a matching bug.
func regressionSHA(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func newIdentityService(store *identityStoreStub, finalizer *identityFinalizerStub, publisher *identityPublisherStub) *Service {
	return NewService(ServiceDeps{Store: store, Finalizer: finalizer, Publisher: publisher}, Config{
		DuplicatePolicy: assetop.DuplicatePolicy{Enabled: true, CheckByContentHash: true, SkipIfExists: true},
		UploadPolicy:    assetop.UploadPolicy{Enabled: true},
		PersistPolicy:   assetop.PersistPolicy{SaveToAssetRegistry: true},
	})
}

func identityInput(id, localPath string) *FinalizeInput {
	return &FinalizeInput{
		ID: id, LocalPath: localPath, Filename: filepath.Base(localPath),
		Kind: AssetKindVideo, Source: "youtube", Destination: delivery.DestinationYouTubeClip,
		RequireDrive: true,
	}
}

// committedIDs flattens the ids of every record the canonical commit boundary
// received, in order.
func committedIDs(records []*artifacts.MediaRecord) []string {
	ids := make([]string, 0, len(records))
	for _, rec := range records {
		ids = append(ids, rec.ID)
	}
	return ids
}

// ── case 1 ──────────────────────────────────────────────────────────────────

// CASE 1 — same asset id + same bytes → no second upload.
func TestMediaIdentity_Case1_SameAssetSameBytesIsAnIdempotentSkip(t *testing.T) {
	const content = "case-1 bytes"
	path := regressionArtifact(t, content)
	sha := regressionSHA(content)

	store := &identityStoreStub{records: []*assetop.AssetRecord{{
		ID: "asset-A", ContentHash: sha,
		DriveFileID: "drive-A", DriveLink: "https://drive.test/A", DownloadLink: "https://drive.test/A/dl",
	}}}
	finalizer := &identityFinalizerStub{}
	publisher := &identityPublisherStub{}

	result, err := newIdentityService(store, finalizer, publisher).ProcessAsset(context.Background(), identityInput("asset-A", path), regressionLegacyMD5)
	if err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	if result.Status != "skipped_duplicate" {
		t.Fatalf("status = %q, want skipped_duplicate", result.Status)
	}
	if len(publisher.requests) != 0 {
		t.Errorf("publisher calls = %d, want 0 (the bytes are already uploaded)", len(publisher.requests))
	}
	if len(finalizer.records) != 0 {
		t.Errorf("commit calls = %d, want 0 for an idempotent retry", len(finalizer.records))
	}
	if result.DriveFileID != "drive-A" {
		t.Errorf("DriveFileID = %q, want the existing identity drive-A", result.DriveFileID)
	}
	if result.ContentHash != sha {
		t.Errorf("ContentHash = %q, want the byte identity %q", result.ContentHash, sha)
	}
}

// ── case 2 (mutation-strength) ──────────────────────────────────────────────

// CASE 2 — different asset id + same bytes → TWO logical assets sharing ONE
// physical content identity.
//
// This is the case the whole design exists for, so it asserts the separation
// rather than just the outcome: the second asset must still be created, its own
// id must reach the commit boundary, and the shared content address must be
// identical to the first asset's. A refactor that "optimises" case 2 back into
// a skip (content addressing degenerating into asset collapsing) fails here.
func TestMediaIdentity_Case2_DifferentAssetSameBytesCreatesASecondLogicalAsset(t *testing.T) {
	const content = "case-2 shared bytes"
	path := regressionArtifact(t, content)
	sha := regressionSHA(content)

	store := &identityStoreStub{records: []*assetop.AssetRecord{{
		ID: "asset-A", ContentHash: sha,
		DriveFileID: "drive-A", DriveLink: "https://drive.test/A",
	}}}
	finalizer := &identityFinalizerStub{}
	publisher := &identityPublisherStub{}

	result, err := newIdentityService(store, finalizer, publisher).ProcessAsset(context.Background(), identityInput("asset-B", path), regressionLegacyMD5)
	if err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}

	// It must NOT be reported as a duplicate of A.
	if result.Status == "skipped_duplicate" {
		t.Fatalf("status = skipped_duplicate: the second logical asset was collapsed into the first (bytes are shared, the ASSET is not)")
	}
	if result.Status != "processed" {
		t.Fatalf("status = %q, want processed", result.Status)
	}
	if result.ReusedAssetID != "asset-A" {
		t.Errorf("ReusedAssetID = %q, want asset-A (the bytes were already stored)", result.ReusedAssetID)
	}

	// The new logical asset must reach the commit boundary under its OWN id.
	if len(finalizer.records) == 0 {
		t.Fatal("no record was committed: the second logical asset never existed")
	}
	last := finalizer.records[len(finalizer.records)-1]
	if last.ID != "asset-B" {
		t.Fatalf("committed id = %q, want asset-B: the asset id is the logical identity and must not be replaced by the content owner's id", last.ID)
	}
	for _, id := range committedIDs(finalizer.records) {
		if id == "asset-A" {
			t.Fatalf("committed ids = %v: an asset-A row was written for an asset-B publish", committedIDs(finalizer.records))
		}
	}

	// One physical content identity: the shared address is what makes the blob
	// reusable, and it is also the proof that nothing rewrote it per-asset.
	if last.ContentHash != sha {
		t.Errorf("committed ContentHash = %q, want the shared byte identity %q", last.ContentHash, sha)
	}

	// The second asset gets its own publish: a Drive file lives in the folder
	// layout of the asset that published it, so it is not relinked to A.
	if len(publisher.requests) != 1 {
		t.Fatalf("publisher calls = %d, want 1 for the new logical asset", len(publisher.requests))
	}
	if got := publisher.requests[0].AssetID; got != "asset-B" {
		t.Errorf("published AssetID = %q, want asset-B", got)
	}
}

// ── case 3 ──────────────────────────────────────────────────────────────────

// CASE 3 — same asset id + CHANGED bytes → must not be silenced as a duplicate.
func TestMediaIdentity_Case3_SameAssetChangedBytesIsNotADuplicate(t *testing.T) {
	oldBytes, newBytes := "case-3 old bytes", "case-3 new bytes"
	path := regressionArtifact(t, newBytes)
	oldSHA, newSHA := regressionSHA(oldBytes), regressionSHA(newBytes)

	store := &identityStoreStub{records: []*assetop.AssetRecord{{
		ID: "asset-A", ContentHash: oldSHA,
		DriveFileID: "drive-A", DriveLink: "https://drive.test/A",
	}}}
	finalizer := &identityFinalizerStub{}
	publisher := &identityPublisherStub{}

	result, err := newIdentityService(store, finalizer, publisher).ProcessAsset(context.Background(), identityInput("asset-A", path), regressionLegacyMD5)
	if err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	if result.Status == "skipped_duplicate" {
		t.Fatal("status = skipped_duplicate: the replacement bytes were silently discarded")
	}
	if result.Status != "processed" {
		t.Fatalf("status = %q, want processed", result.Status)
	}
	if len(finalizer.records) == 0 {
		t.Fatal("nothing was committed for the changed bytes")
	}
	last := finalizer.records[len(finalizer.records)-1]
	if last.ContentHash != newSHA {
		t.Errorf("committed ContentHash = %q, want the NEW byte identity %q", last.ContentHash, newSHA)
	}
	if last.ContentHash == oldSHA {
		t.Error("committed ContentHash still states the old bytes: the record no longer describes its own content")
	}
	if len(publisher.requests) != 1 {
		t.Errorf("publisher calls = %d, want 1 (the new bytes must be uploaded)", len(publisher.requests))
	}
}

// ── case 4 ──────────────────────────────────────────────────────────────────

// CASE 4 — another record's legacy MD5 matches, but the SHA-256 differs → the
// MD5 tier must NOT dedup.
func TestMediaIdentity_Case4_MatchingLegacyMD5NeverDeduplicatesWhenSHADiffers(t *testing.T) {
	aBytes, bBytes := "case-4 asset A bytes", "case-4 asset B bytes"
	path := regressionArtifact(t, bBytes)
	aSHA, bSHA := regressionSHA(aBytes), regressionSHA(bBytes)

	// asset-A carries the SAME legacy digest the caller hands us for asset-B.
	// Pre-fix, that shared MD5 was the dedup key and asset-B was skipped.
	store := &identityStoreStub{records: []*assetop.AssetRecord{{
		ID: "asset-A", ContentHash: aSHA, LegacyFileMD5: regressionLegacyMD5,
	}}}
	finalizer := &identityFinalizerStub{}
	publisher := &identityPublisherStub{}

	result, err := newIdentityService(store, finalizer, publisher).ProcessAsset(context.Background(), identityInput("asset-B", path), regressionLegacyMD5)
	if err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	if result.Status == "skipped_duplicate" {
		t.Fatal("status = skipped_duplicate: a matching legacy MD5 decided asset identity")
	}
	if result.Status != "processed" {
		t.Fatalf("status = %q, want processed", result.Status)
	}
	if store.legacyTierQueries != 0 {
		t.Errorf("legacy MD5-tier lookups = %d, want 0: no decision may read MD5", store.legacyTierQueries)
	}
	last := finalizer.records[len(finalizer.records)-1]
	if last.ID != "asset-B" || last.ContentHash != bSHA {
		t.Errorf("committed record = {id:%q content:%q}, want {asset-B, %s}", last.ID, last.ContentHash, bSHA)
	}
	if len(publisher.requests) != 1 {
		t.Errorf("publisher calls = %d, want 1 for the distinct asset", len(publisher.requests))
	}
}

// ── case 5 / case 6 ─────────────────────────────────────────────────────────

// CASE 5 — the upload's bytes do not match the expected SHA-256 → publish fails.
// CASE 6 — the Drive-side size does not match the expected size → publish fails.
//
// Both are enforced by the post-upload verifier, which only runs when the
// request carries ExpectedSize/ExpectedSHA256. So each case asserts BOTH halves:
// the request carries the REAL byte identity (otherwise the verifier silently
// skips its check), and the verifier's rejection fails the publish.
func TestMediaIdentity_Case5And6_UploadVerificationIsThreadedAndFailsClosed(t *testing.T) {
	const content = "case-5-6 bytes that must be verified after upload"
	path := regressionArtifact(t, content)
	sha := regressionSHA(content)
	size := int64(len(content))

	// (a) a successful publish must carry the real verification signals.
	successStore := &identityStoreStub{}
	successFinalizer := &identityFinalizerStub{}
	successPublisher := &identityPublisherStub{}
	if _, err := newIdentityService(successStore, successFinalizer, successPublisher).ProcessAsset(context.Background(), identityInput("asset-verify", path), regressionLegacyMD5); err != nil {
		t.Fatalf("ProcessAsset: %v", err)
	}
	if len(successPublisher.requests) != 1 {
		t.Fatalf("publisher calls = %d, want 1", len(successPublisher.requests))
	}
	req := successPublisher.requests[0]
	if req.ContentHash != sha {
		t.Errorf("PublishRequest.ContentHash = %q, want ExpectedSHA256 %q (the SHA-256 of the bytes)", req.ContentHash, sha)
	}
	if req.SizeBytes != size {
		t.Errorf("PublishRequest.SizeBytes = %d, want ExpectedSize %d (the real byte count)", req.SizeBytes, size)
	}
	if req.ContentHash == regressionLegacyMD5 {
		t.Error("PublishRequest.ContentHash is the legacy MD5: the verifier would compare Drive's SHA-256 against an MD5 and never match")
	}

	for _, tc := range []struct {
		name string
		err  error
	}{
		{name: "sha256 mismatch", err: fmt.Errorf("delivery: publish: %w", drive.ErrDriveFileSHA256Mismatch)},
		{name: "size mismatch", err: fmt.Errorf("delivery: publish: %w", drive.ErrDriveFileSizeMismatch)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &identityStoreStub{}
			finalizer := &identityFinalizerStub{}
			publisher := &identityPublisherStub{err: tc.err}

			result, err := newIdentityService(store, finalizer, publisher).ProcessAsset(context.Background(), identityInput("asset-verify-fail", path), regressionLegacyMD5)
			if !errors.Is(err, ErrDriveUploadFailed) {
				t.Fatalf("err = %v, want ErrDriveUploadFailed", err)
			}
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want the verifier's cause %v", err, tc.err)
			}
			if result == nil || result.OK || result.Status == "processed" {
				t.Fatalf("result = %#v, want a non-success result: an unverified upload is not a published asset", result)
			}
			// The failure must still be recorded so the upload stays recoverable.
			if len(finalizer.records) < 2 {
				t.Fatalf("commit calls = %d, want pending + failed recovery", len(finalizer.records))
			}
			last := finalizer.records[len(finalizer.records)-1]
			if last.PublishStatus != "PUBLISH_FAILED" || last.Status != "delivery_pending" {
				t.Errorf("recovery record = {%s, %s}, want {PUBLISH_FAILED, delivery_pending}", last.PublishStatus, last.Status)
			}
		})
	}
}

// ── Fix D preflight ─────────────────────────────────────────────────────────

// TestMediaIdentity_UnreadableLocalBytesRefuseAnUnverifiableUpload pins the
// fail-closed half of D: when the local bytes an upload is supposed to verify
// cannot even be hashed, the publish is refused instead of proceeding with an
// empty ExpectedSHA256/ExpectedSize (which the uploader reads as "skip the
// check").
func TestMediaIdentity_UnreadableLocalBytesRefuseAnUnverifiableUpload(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.mp4")
	store := &identityStoreStub{}
	finalizer := &identityFinalizerStub{}
	publisher := &identityPublisherStub{}

	result, err := newIdentityService(store, finalizer, publisher).ProcessAsset(context.Background(), identityInput("asset-unreadable", missing), regressionLegacyMD5)
	if !errors.Is(err, ErrDriveUploadFailed) {
		t.Fatalf("err = %v, want ErrDriveUploadFailed", err)
	}
	if result == nil || result.OK {
		t.Fatalf("result = %#v, want a non-success result", result)
	}
	if len(publisher.requests) != 0 {
		t.Errorf("publisher calls = %d, want 0: an unverifiable upload must never be attempted", len(publisher.requests))
	}
	if len(finalizer.records) != 0 {
		t.Errorf("commit calls = %d, want 0: nothing may be recorded as pending for an unverifiable upload", len(finalizer.records))
	}
	// The refusal names the unverifiable bytes rather than a generic failure.
	if !strings.Contains(err.Error(), "unverifiable") {
		t.Errorf("err = %v, want the refusal to name the unverifiable upload", err)
	}
}

// TestMediaIdentity_EmptyContentAddressIsNeverPublished pins the second half of
// the same rule for a zero-length artifact: size 0 is not a valid ExpectedSize
// (the verifier treats 0 as "skip"), so the publish is refused.
func TestMediaIdentity_EmptyContentAddressIsNeverPublished(t *testing.T) {
	empty := regressionArtifact(t, "")
	publisher := &identityPublisherStub{}

	_, err := newIdentityService(&identityStoreStub{}, &identityFinalizerStub{}, publisher).ProcessAsset(context.Background(), identityInput("asset-empty", empty), regressionLegacyMD5)
	if !errors.Is(err, ErrDriveUploadFailed) {
		t.Fatalf("err = %v, want ErrDriveUploadFailed for a zero-byte artifact", err)
	}
	if len(publisher.requests) != 0 {
		t.Errorf("publisher calls = %d, want 0", len(publisher.requests))
	}
}

package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// stubClipIndexer is a test-only ClipIndexer that records the last request
// and returns a configurable error.
type stubClipIndexer struct {
	lastClip ClipRecord
	lastHash string
	err      error
}

func (s *stubClipIndexer) EnqueueAndIndex(_ context.Context, clip ClipRecord, contentHash string) error {
	s.lastClip = clip
	s.lastHash = contentHash
	return s.err
}

// ── Test 1: happy path — all fields flow through, indexer succeeds ────────

func TestPersistClipAndIndex_HappyPath(t *testing.T) {
	stub := &stubClipIndexer{}

	cmd := PersistClipCommand{
		ClipID:          "yt_dQw4w9WgXcQ_a1b2c3d4",
		Name:            "My Video",
		Filename:        "dQw4w9WgXcQ - My Video.mp4",
		Source:          "youtube-manual",
		Category:        "sports",
		Tags:            []string{"boxing", "training"},
		DurationSec:     30,
		LocalPath:       "/tmp/clip.mp4",
		LegacyFileMD5:   "a1b2c3d4e5f6a7b8",
		DriveLink:       "https://drive.google.com/file/d/xyz123/view",
		DriveFileID:     "xyz123",
		Summary:         "A boxing training video",
		Topics:          []string{"boxing"},
		Speakers:        []string{"Coach Smith"},
		MentionedPeople: []string{"Mike Tyson"},
		Hook:            "Watch this knockout!",
	}

	err := PersistClipAndIndex(context.Background(), stub, cmd)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Verify all command fields flowed through to the record.
	clip := stub.lastClip
	if clip.ID != cmd.ClipID {
		t.Errorf("expected ID %q, got %q", cmd.ClipID, clip.ID)
	}
	if clip.Name != cmd.Name {
		t.Errorf("expected Name %q, got %q", cmd.Name, clip.Name)
	}
	if clip.Filename != cmd.Filename {
		t.Errorf("expected Filename %q, got %q", cmd.Filename, clip.Filename)
	}
	if clip.Source != cmd.Source {
		t.Errorf("expected Source %q, got %q", cmd.Source, clip.Source)
	}
	if clip.Category != cmd.Category {
		t.Errorf("expected Category %q, got %q", cmd.Category, clip.Category)
	}
	if len(clip.Tags) != 2 || clip.Tags[0] != "boxing" || clip.Tags[1] != "training" {
		t.Errorf("expected Tags [boxing training], got %v", clip.Tags)
	}
	if clip.Duration.Seconds() != float64(cmd.DurationSec) {
		t.Errorf("expected Duration %ds, got %v", cmd.DurationSec, clip.Duration)
	}
	if clip.LocalPath != cmd.LocalPath {
		t.Errorf("expected LocalPath %q, got %q", cmd.LocalPath, clip.LocalPath)
	}
	if clip.LegacyFileMD5 != cmd.LegacyFileMD5 {
		t.Errorf("expected LegacyFileMD5 %q, got %q", cmd.LegacyFileMD5, clip.LegacyFileMD5)
	}
	if clip.DriveLink != cmd.DriveLink {
		t.Errorf("expected DriveLink %q, got %q", cmd.DriveLink, clip.DriveLink)
	}
	if clip.DriveFileID != cmd.DriveFileID {
		t.Errorf("expected DriveFileID %q, got %q", cmd.DriveFileID, clip.DriveFileID)
	}
	if clip.Summary != cmd.Summary {
		t.Errorf("expected Summary %q, got %q", cmd.Summary, clip.Summary)
	}
	if len(clip.Topics) != 1 || clip.Topics[0] != "boxing" {
		t.Errorf("expected Topics [boxing], got %v", clip.Topics)
	}
	if len(clip.Speakers) != 1 || clip.Speakers[0] != "Coach Smith" {
		t.Errorf("expected Speakers [Coach Smith], got %v", clip.Speakers)
	}
	if len(clip.MentionedPeople) != 1 || clip.MentionedPeople[0] != "Mike Tyson" {
		t.Errorf("expected MentionedPeople [Mike Tyson], got %v", clip.MentionedPeople)
	}
	if clip.Hook != cmd.Hook {
		t.Errorf("expected Hook %q, got %q", cmd.Hook, clip.Hook)
	}

	// The content hash should be passed through verbatim.
	if stub.lastHash != cmd.LegacyFileMD5 {
		t.Errorf("expected contentHash %q, got %q", cmd.LegacyFileMD5, stub.lastHash)
	}
}

// ── Test 2: nil indexer → fail-closed error ──────────────────────────────

func TestPersistClipAndIndex_NilIndexer_ReturnsError(t *testing.T) {
	cmd := PersistClipCommand{
		ClipID:    "yt_test_id",
		LocalPath: "/tmp/clip.mp4",
	}

	err := PersistClipAndIndex(context.Background(), nil, cmd)
	if err == nil {
		t.Fatal("expected non-nil error for nil indexer")
	}
	if !strings.Contains(err.Error(), "indexer is nil") {
		t.Errorf("expected 'indexer is nil' in error, got %v", err)
	}
	// The error must mention QDRANT so audit-grep finds it.
	if !strings.Contains(err.Error(), "QDRANT") {
		t.Errorf("expected 'QDRANT' audit-pin in error, got %v", err)
	}
}

// ── Test 3: indexer error → wrapped with usecase prefix + errors.Is ──────

func TestPersistClipAndIndex_IndexerError_WrapsWithPrefix(t *testing.T) {
	sentinel := errors.New("media_assets: UNIQUE constraint failed: id")
	stub := &stubClipIndexer{err: sentinel}

	cmd := PersistClipCommand{
		ClipID:    "yt_test_id",
		LocalPath: "/tmp/clip.mp4",
	}

	err := PersistClipAndIndex(context.Background(), stub, cmd)
	if err == nil {
		t.Fatal("expected non-nil error when indexer fails")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("expected errors.Is(err, sentinel) to be true, got %v", err)
	}
	if !strings.Contains(err.Error(), "usecase.PersistClipAndIndex") {
		t.Errorf("expected error to wrap with usecase prefix, got %v", err)
	}
}

// ── Test 4: defensive copy — mutating cmd.Tags after call does not mutate the stored clip ──

func TestPersistClipAndIndex_DefensiveCopy_TagsNotAliased(t *testing.T) {
	stub := &stubClipIndexer{}

	tags := []string{"boxing", "training"}
	topics := []string{"sports"}
	speakers := []string{"Coach"}
	mentioned := []string{"Athlete"}

	cmd := PersistClipCommand{
		ClipID:          "yt_test_id",
		Tags:            tags,
		Topics:          topics,
		Speakers:        speakers,
		MentionedPeople: mentioned,
	}

	_ = PersistClipAndIndex(context.Background(), stub, cmd)

	// Mutate the original slices after the call.
	// The use case must have made a defensive copy — the stored clip should
	// reflect the pre-call values, not the mutated ones.
	tags[0] = "MUTATED_TAG"
	topics[0] = "MUTATED_TOPIC"
	speakers[0] = "MUTATED_SPEAKER"
	mentioned[0] = "MUTATED_PERSON"

	// The stored clip must still have the original values.
	clip := stub.lastClip
	if len(clip.Tags) != 2 || clip.Tags[0] != "boxing" || clip.Tags[1] != "training" {
		t.Errorf("defensive copy failed for Tags: got %v", clip.Tags)
	}
	if len(clip.Topics) != 1 || clip.Topics[0] != "sports" {
		t.Errorf("defensive copy failed for Topics: got %v", clip.Topics)
	}
	if len(clip.Speakers) != 1 || clip.Speakers[0] != "Coach" {
		t.Errorf("defensive copy failed for Speakers: got %v", clip.Speakers)
	}
	if len(clip.MentionedPeople) != 1 || clip.MentionedPeople[0] != "Athlete" {
		t.Errorf("defensive copy failed for MentionedPeople: got %v", clip.MentionedPeople)
	}
}
